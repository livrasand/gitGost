package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// bundleChunkSize es el número de commits por bloque de la descarga por capas
// del servidor (mismo criterio que el cliente de Fase 1).
const bundleChunkSize = 500

// CreateBundle descarga el repositorio remoto por capas en workDir y crea un
// bundle completo (commits, árboles y blobs) que git puede clonar sin red.
// Devuelve la ruta del bundle y la rama por defecto del remoto. El contexto
// acota la duración total de los subprocesos git (evita workers colgados).
func CreateBundle(ctx context.Context, url, workDir string) (bundlePath, defaultBranch string, err error) {
	repoDir := filepath.Join(workDir, "repo")
	// El separador -- evita que la URL (controlada por el usuario) se interprete
	// como opción de git (p. ej. --upload-pack=...) aunque no pase validación.
	if err := runGit(ctx, "", "clone", "--mirror", "--depth="+strconv.Itoa(bundleChunkSize), "--", url, repoDir); err != nil {
		return "", "", fmt.Errorf("clonar %s: %w", url, err)
	}

	// Profundizar por capas hasta cubrir la historia completa (patrón del
	// cliente de Fase 1, sin filtro de blobs: el bundle final debe incluir
	// todo el contenido para que el checkout del cliente funcione sin red).
	block := 0
	for repoShallow(ctx, repoDir) {
		block++
		prevShallow := shallowFile(repoDir)
		prev := revCount(ctx, repoDir)
		if err := runGit(ctx, repoDir, "fetch", "--deepen="+strconv.Itoa(bundleChunkSize), "origin"); err != nil {
			return "", "", fmt.Errorf("profundizar repo (bloque %d): %w", block, err)
		}
		if !repoShallow(ctx, repoDir) {
			break
		}
		// Los shallow points de ramas/tags cortas se resuelven sin añadir
		// commits: si nada cambió, el servidor no profundiza más por capas.
		if shallowFile(repoDir) == prevShallow && revCount(ctx, repoDir) == prev {
			break
		}
	}
	if repoShallow(ctx, repoDir) {
		if err := runGit(ctx, repoDir, "fetch", "--unshallow", "origin"); err != nil {
			return "", "", fmt.Errorf("completar historia: %w", err)
		}
	}

	// Rama por defecto: en un clon --mirror el HEAD local apunta a la ref remota.
	branch, err := gitOutput(ctx, repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("determinar rama por defecto: %w", err)
	}

	bundlePath = filepath.Join(workDir, "repo.bundle")
	if err := runGit(ctx, repoDir, "bundle", "create", bundlePath, "--all"); err != nil {
		return "", "", fmt.Errorf("crear bundle: %w", err)
	}
	return bundlePath, strings.TrimSpace(branch), nil
}

// shallowFile devuelve el contenido actual del marker de shallow, o vacío si el
// repo ya no es shallow. Soporta repos bare (clone --mirror: <dir>/shallow) y
// repos normales (<dir>/.git/shallow); devolver vacío si ninguno puede leerse
// mantiene el guard de "sin progreso" operativo para los clonados mirror.
func shallowFile(dir string) string {
	for _, p := range []string{
		filepath.Join(dir, "shallow"),
		filepath.Join(dir, ".git", "shallow"),
	} {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

// repoShallow indica si el repo sigue con historia parcial.
func repoShallow(ctx context.Context, dir string) bool {
	out, err := gitOutput(ctx, dir, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

// revCount cuenta los commits visibles en todas las refs del repo.
func revCount(ctx context.Context, dir string) int {
	out, err := gitOutput(ctx, dir, "rev-list", "--count", "--all")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// runGit ejecuta git; si falla, el error incluye el mensaje real de stderr.
// El contexto permite matar el subproceso si el job remoto excede su timeout.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
	}
	return err
}

// gitOutput ejecuta git y devuelve su stdout limpio; el stderr solo se incorpora
// al error cuando el comando falla (los warnings de git no contaminan el valor).
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), fmt.Errorf("%w: %s", err, msg)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}
