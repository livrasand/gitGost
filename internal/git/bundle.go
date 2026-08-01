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
	// gitClone valida la URL en el punto exacto donde se convierte en
	// argumento de git, evitando que ninguna ruta de llamada inyecte opciones
	// (p. ej. --upload-pack=...) aunque el separador -- dejara de estar presente.
	if err := gitClone(ctx, url, repoDir); err != nil {
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
	// Barrera explícita para análisis estático: la URL debe coincidir con la
	// expresión regular permitida antes de ser reconstruida. Los grupos
	// capturados contienen solo los caracteres permitidos por el patrón.
	if !repoURLPattern.MatchString(raw) {
		return "", fmt.Errorf("URL de repositorio inválida: %q", raw)
	}
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

// gitClone clona rawURL validada en dest con --mirror y shallow depth fijo.
// Es el único comando git que recibe input directo del usuario; por eso se
// aísla, valida la URL con safeCloneURL y construye exec.CommandContext con
// argumentos explícitos en lugar de un variádico genérico.
func gitClone(ctx context.Context, rawURL, dest string) error {
	safeURL, err := safeCloneURL(rawURL)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git",
		"clone",
		"--mirror",
		"--depth="+strconv.Itoa(bundleChunkSize),
		"--",
		safeURL,
		dest,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// runGit ejecuta git; si falla, el error incluye el mensaje real de stderr.
// El contexto permite matar el subproceso si el job remoto excede su timeout.
// Ningún caller pasa input de usuario a través de args; todos los argumentos
// son literales o paths derivados de workDir creado por os.MkdirTemp.
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
