package jobs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// chunkSize es el número de commits por bloque en la descarga por capas.
// Sobrescribible en tests para validar el resume con varias capas.
var chunkSize = 500

// runClone decide el flujo de descarga: Fase 2 (bundle con Range Requests y
// resume por bytes vía Openbin) si el servidor lo soporta; si no, Fase 1
// (descarga por capas con resume entre bloques).
func runClone(s *Store, job *Job) error {
	if err := runCloneBundle(s, job); err != nil {
		if errors.Is(err, errRemoteJobsUnsupported) {
			_ = s.SetProgress(job.ID, "El servidor no soporta jobs remotos; usando descarga por capas...")
			return runCloneLayered(s, job)
		}
		return err
	}
	return nil
}

// runCloneLayered (Fase 1) descarga un repositorio por capas: inicializa el
// repo, descarga la historia en bloques de chunkSize commits (--depth/--deepen)
// y materializa la rama por defecto. Cada bloque completado es un checkpoint:
// si la conexión se pierde, al reanudar solo se descargan los bloques pendientes.
func runCloneLayered(s *Store, job *Job) error {
	dir := job.Target
	if dir == "" {
		return fmt.Errorf("job de clone sin directorio destino")
	}

	// Inicializar el repo si no existe (resume = repo ya inicializado).
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err := runGit("", "init", "-q", dir); err != nil {
			return fmt.Errorf("inicializar repo: %w", err)
		}
	}

	// Asegurar el remote origin con la URL original del usuario (o la efectiva
	// si no hay original): el repo resultante debe ser un repo Git normal.
	originURL := job.Origin
	if originURL == "" {
		originURL = job.URL
	}
	_ = runGit(dir, "remote", "remove", "origin")
	if err := runGit(dir, "remote", "add", "origin", originURL); err != nil {
		return fmt.Errorf("configurar remote origin: %w", err)
	}

	// Rama por defecto del remoto.
	ls, err := gitOutputWithRetry(s, job, dir, "ls-remote", "--symref", job.URL, "HEAD")
	if err != nil {
		return fmt.Errorf("descubrir rama por defecto: %w", err)
	}
	branch := parseDefaultBranch(ls)
	if branch == "" {
		return fmt.Errorf("no se pudo determinar la rama por defecto de %s", job.URL)
	}

	// Descarga por capas de todas las ramas. Con --filter=blob:none los packs
	// solo contienen commits y árboles: cada bloque es pequeño y resistente a
	// cortes, y los blobs se materializan bajo demanda durante el checkout.
	refspec := "+refs/heads/*:refs/remotes/origin/*"
	if repoShallow(dir) {
		_ = s.SetProgress(job.ID, "Reanudando desde el checkpoint existente...")
	} else {
		_ = s.SetProgress(job.ID, "Descargando primer bloque...")
		if err := execGitWithRetry(s, job, dir,
			"fetch", "--depth="+strconv.Itoa(chunkSize), "--filter=blob:none", job.URL, refspec); err != nil {
			return err
		}
	}

	// Profundizar hasta alcanzar la historia completa. Los shallow points de
	// ramas/tags cortas se resuelven sin añadir commits al grafo, así que el
	// "sin progreso" real es: el shallow file no cambió Y el count tampoco.
	block := 0
	for repoShallow(dir) {
		block++
		prevShallow := shallowFile(dir)
		prev := gitRevCount(dir)
		if err := execGitWithRetry(s, job, dir,
			"fetch", "--deepen="+strconv.Itoa(chunkSize), "--filter=blob:none", job.URL, refspec); err != nil {
			return err
		}
		count := gitRevCount(dir)
		_ = s.SetProgress(job.ID, fmt.Sprintf("Bloque %d: %d commits descargados", block, count))
		if !repoShallow(dir) {
			break
		}
		if shallowFile(dir) == prevShallow && count == prev {
			// El servidor no profundiza más por capas: se completa con unshallow.
			break
		}
	}

	// Cierre: si quedaron shallow points sin resolver, --unshallow completa toda
	// la historia restante en una sola petición (ligera con --filter=blob:none).
	if repoShallow(dir) {
		_ = s.SetProgress(job.ID, "Completando historia restante...")
		if err := execGitWithRetry(s, job, dir,
			"fetch", "--unshallow", "--filter=blob:none", job.URL, refspec); err != nil {
			return err
		}
	}

	// Materializar la rama por defecto con tracking.
	if err := runGit(dir, "checkout", "-b", branch, "--track", "origin/"+branch); err != nil {
		// La rama local ya existía (resume tras el checkout): solo cambiar a ella.
		if err := runGit(dir, "checkout", branch); err != nil {
			return fmt.Errorf("crear rama local %s: %w", branch, err)
		}
	}
	return nil
}

// shallowFile devuelve el contenido actual del archivo .git/shallow (los
// boundaries pendientes de profundizar), o vacío si el repo no es shallow.
func shallowFile(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".git", "shallow"))
	if err != nil {
		return ""
	}
	return string(data)
}

// repoShallow indica si el repo sigue con historia parcial (descarga por capas en curso).
func repoShallow(dir string) bool {
	out, err := gitOutput(dir, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

// gitRevCount cuenta los commits visibles en todas las refs del repo.
func gitRevCount(dir string) int {
	out, err := gitOutput(dir, "rev-list", "--count", "--all")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// parseDefaultBranch extrae la rama por defecto de la salida de
// `git ls-remote --symref <url> HEAD` ("ref: refs/heads/main\tHEAD").
func parseDefaultBranch(ls string) string {
	for _, line := range strings.Split(ls, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ref: refs/heads/") && strings.HasSuffix(line, "\tHEAD") {
			return strings.TrimSuffix(strings.TrimPrefix(line, "ref: refs/heads/"), "\tHEAD")
		}
	}
	return ""
}

// gitOutput ejecuta git y devuelve su salida combinada. Si git falla, el error
// incluye el mensaje real de stderr (p. ej. "unable to access") para que
// isRetryable pueda clasificar el fallo como de red y no como error genérico.
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

// gitOutputWithRetry como gitOutput pero con reintentos ante fallos de red.
func gitOutputWithRetry(s *Store, job *Job, dir string, args ...string) (string, error) {
	var (
		out string
		err error
	)
	backoff := retryBase
	for attempt := 0; attempt <= retryMax; attempt++ {
		if attempt > 0 {
			_ = s.SetState(job.ID, StateRetrying)
			_ = s.SetProgress(job.ID, fmt.Sprintf(
				"Reintentando en %s (intento %d/%d)...", backoff, attempt, retryMax))
			time.Sleep(backoff)
			backoff *= 2
		}
		out, err = gitOutput(dir, args...)
		if err == nil {
			return out, nil
		}
		if !isRetryable(err) {
			return out, err
		}
	}
	return out, err
}

// runGit ejecuta git sin capturar salida.
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}
