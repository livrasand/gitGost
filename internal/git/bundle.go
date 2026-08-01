package git

import (
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
// Devuelve la ruta del bundle y la rama por defecto del remoto.
func CreateBundle(url, workDir string) (bundlePath, defaultBranch string, err error) {
	repoDir := filepath.Join(workDir, "repo")
	if err := runGit("", "clone", "--mirror", "--depth="+strconv.Itoa(bundleChunkSize), url, repoDir); err != nil {
		return "", "", fmt.Errorf("clonar %s: %w", url, err)
	}

	// Profundizar por capas hasta cubrir la historia completa (patrón del
	// cliente de Fase 1, sin filtro de blobs: el bundle final debe incluir
	// todo el contenido para que el checkout del cliente funcione sin red).
	block := 0
	for repoShallow(repoDir) {
		block++
		prevShallow := shallowFile(repoDir)
		prev := revCount(repoDir)
		if err := runGit(repoDir, "fetch", "--deepen="+strconv.Itoa(bundleChunkSize), "origin"); err != nil {
			return "", "", fmt.Errorf("profundizar repo (bloque %d): %w", block, err)
		}
		if !repoShallow(repoDir) {
			break
		}
		// Los shallow points de ramas/tags cortas se resuelven sin añadir
		// commits: si nada cambió, el servidor no profundiza más por capas.
		if shallowFile(repoDir) == prevShallow && revCount(repoDir) == prev {
			break
		}
	}
	if repoShallow(repoDir) {
		if err := runGit(repoDir, "fetch", "--unshallow", "origin"); err != nil {
			return "", "", fmt.Errorf("completar historia: %w", err)
		}
	}

	// Rama por defecto: en un clon --mirror el HEAD local apunta a la ref remota.
	branch, err := gitOutput(repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("determinar rama por defecto: %w", err)
	}

	bundlePath = filepath.Join(workDir, "repo.bundle")
	if err := runGit(repoDir, "bundle", "create", bundlePath, "--all"); err != nil {
		return "", "", fmt.Errorf("crear bundle: %w", err)
	}
	return bundlePath, strings.TrimSpace(branch), nil
}

// shallowFile devuelve el contenido actual de .git/shallow, o vacío si el repo
// ya no es shallow.
func shallowFile(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".git", "shallow"))
	if err != nil {
		return ""
	}
	return string(data)
}

// repoShallow indica si el repo sigue con historia parcial.
func repoShallow(dir string) bool {
	out, err := gitOutput(dir, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

// revCount cuenta los commits visibles en todas las refs del repo.
func revCount(dir string) int {
	out, err := gitOutput(dir, "rev-list", "--count", "--all")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// runGit ejecuta git; si falla, el error incluye el mensaje real de stderr.
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
	}
	return err
}

// gitOutput ejecuta git y devuelve su salida combinada.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return string(out), fmt.Errorf("%w: %s", err, msg)
		}
	}
	return string(out), err
}
