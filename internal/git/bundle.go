package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// La URL proviene de la API (entrada del usuario). Se revalida aquí, en el
	// punto donde se convierte en argumento de git, para que ninguna ruta de
	// llamada pueda inyectar opciones (p. ej. --upload-pack=...) aunque el
	// separador -- dejara de estar presente.
	safeURL, err := safeCloneURL(url)
	if err != nil {
		return "", "", err
	}
	// El separador -- evita que la URL se interprete como opción de git incluso
	// si la validación superior cambiara; el argumento es la URL reconstruida
	// del parseo validado, nunca el raw del usuario.
	if err := runGit(ctx, "", "clone", "--mirror", "--depth="+strconv.Itoa(bundleChunkSize), "--", safeURL, repoDir); err != nil {
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

	// En clones shallow git omite las tags que apuntan a commits fuera del rango
	// vigente y no las vuelve a pedir al profundizar; sin este fetch explícito el
	// bundle (--all) saldría sin esas tags para el cliente.
	if err := runGit(ctx, repoDir, "fetch", "--tags", "origin"); err != nil {
		return "", "", fmt.Errorf("traer tags de origin: %w", err)
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

// repoURLPattern valida y captura una URL https de repositorio permitida:
// host en {github.com, gitlab.com, codeberg.org} (case-insensitive), puerto
// opcional, y exactamente dos segmentos de ruta owner/repo con caracteres
// permitidos. El resultado reconstruido nunca incluye userinfo, query,
// fragment ni segmentos extra.
var repoURLPattern = regexp.MustCompile(`(?i)^https://(github\.com|gitlab\.com|codeberg\.org)(?::\d+)?/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/?$`)

// safeCloneURL valida la URL del usuario y devuelve su forma normalizada.
// La URL que llega a git se reconstruye exclusivamente desde los grupos
// validados por la expresión regular; el raw del usuario nunca se usa
// directamente como argumento de comando.
func safeCloneURL(raw string) (string, error) {
	m := repoURLPattern.FindStringSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("URL de repositorio inválida: %q", raw)
	}
	host := strings.ToLower(m[1])
	owner, repo := m[2], m[3]
	if strings.Contains(owner, "..") || strings.Contains(repo, "..") {
		return "", fmt.Errorf("URL de repositorio inválida: %q", raw)
	}
	return fmt.Sprintf("https://%s/%s/%s", host, owner, repo), nil
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
