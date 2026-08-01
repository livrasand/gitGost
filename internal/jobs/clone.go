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

var chunkSize = 500

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

func runCloneLayered(s *Store, job *Job) error {
	dir := job.Target
	if dir == "" {
		return fmt.Errorf("job de clone sin directorio destino")
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err := runGit("", "init", "-q", dir); err != nil {
			return fmt.Errorf("inicializar repo: %w", err)
		}
	}

	originURL := job.Origin
	if originURL == "" {
		originURL = job.URL
	}
	_ = runGit(dir, "remote", "remove", "origin")
	if err := runGit(dir, "remote", "add", "origin", originURL); err != nil {
		return fmt.Errorf("configurar remote origin: %w", err)
	}

	ls, err := gitOutputWithRetry(s, job, dir, "ls-remote", "--symref", "--", job.URL, "HEAD")
	if err != nil {
		return fmt.Errorf("descubrir rama por defecto: %w", err)
	}
	branch := parseDefaultBranch(ls)
	if branch == "" {
		return fmt.Errorf("no se pudo determinar la rama por defecto de %s", job.URL)
	}

	refspec := "+refs/heads/*:refs/remotes/origin/*"
	if repoShallow(dir) {
		_ = s.SetProgress(job.ID, "Reanudando desde el checkpoint existente...")
	} else {
		_ = s.SetProgress(job.ID, "Descargando primer bloque...")
		if err := execGitWithRetry(s, job, dir,
			"fetch", "--depth="+strconv.Itoa(chunkSize), "--filter=blob:none", "--", job.URL, refspec); err != nil {
			return err
		}
	}

	block := 0
	for repoShallow(dir) {
		block++
		prevShallow := shallowFile(dir)
		prev := gitRevCount(dir)
		if err := execGitWithRetry(s, job, dir,
			"fetch", "--deepen="+strconv.Itoa(chunkSize), "--filter=blob:none", "--", job.URL, refspec); err != nil {
			return err
		}
		count := gitRevCount(dir)
		_ = s.SetProgress(job.ID, fmt.Sprintf("Bloque %d: %d commits descargados", block, count))
		if !repoShallow(dir) {
			break
		}
		if shallowFile(dir) == prevShallow && count == prev {
			break
		}
	}

	if repoShallow(dir) {
		_ = s.SetProgress(job.ID, "Completando historia restante...")
		if err := execGitWithRetry(s, job, dir,
			"fetch", "--unshallow", "--filter=blob:none", "--", job.URL, refspec); err != nil {
			return err
		}
	}

	if err := runGit(dir, "checkout", "-b", branch, "--track", "origin/"+branch); err != nil {
		if err := runGit(dir, "checkout", branch); err != nil {
			return fmt.Errorf("crear rama local %s: %w", branch, err)
		}
	}
	return nil
}

func shallowFile(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".git", "shallow"))
	if err != nil {
		return ""
	}
	return string(data)
}

func repoShallow(dir string) bool {
	out, err := gitOutput(dir, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

func gitRevCount(dir string) int {
	out, err := gitOutput(dir, "rev-list", "--count", "--all")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func parseDefaultBranch(ls string) string {
	for _, line := range strings.Split(ls, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ref: refs/heads/") && strings.HasSuffix(line, "\tHEAD") {
			return strings.TrimSuffix(strings.TrimPrefix(line, "ref: refs/heads/"), "\tHEAD")
		}
	}
	return ""
}

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

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}
