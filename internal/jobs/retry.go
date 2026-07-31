package jobs

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Configuración de reintentos automáticos ante fallos de red.
var (
	retryMax  = 3
	retryBase = 2 * time.Second
)

// isRetryable decide si un error de git merece reintento. Los errores
// definitivos (repo inexistente, autenticación, destino ocupado) no se
// reintentan; los de red/transporte sí.
func isRetryable(err error) bool {
	lower := strings.ToLower(err.Error())

	noRetry := []string{
		"not found",
		"repository not found",
		"does not appear to be a git repository",
		"authentication failed",
		"access denied",
		"permission denied",
		"already exists",
		"denied",
		"invalid refspec",
		"couldn't find remote ref",
		"401",
		"403",
		"404",
	}
	for _, n := range noRetry {
		if strings.Contains(lower, n) {
			return false
		}
	}

	retry := []string{
		"unable to access",
		"could not resolve host",
		"connection reset",
		"connection refused",
		"connection timed out",
		"operation timed out",
		"timed out",
		"early eof",
		"eof",
		"rpc failed",
		"remote end hung up",
		"invalid index-pack output",
		"fetch-pack:",
		"tls handshake",
		"network is unreachable",
		"no route to host",
		"broken pipe",
		"502",
		"503",
		"504",
		"http error",
	}
	for _, r := range retry {
		if strings.Contains(lower, r) {
			return true
		}
	}
	return false
}

// execGitWithRetry ejecuta un comando git capturando su stderr (progreso) y lo
// reintenta con backoff exponencial si el fallo es de red.
func execGitWithRetry(s *Store, job *Job, dir string, args ...string) error {
	var lastErr error
	backoff := retryBase
	for attempt := 0; attempt <= retryMax; attempt++ {
		if attempt > 0 {
			_ = s.SetState(job.ID, StateRetrying)
			_ = s.SetProgress(job.ID, fmt.Sprintf(
				"Reintentando en %s (intento %d/%d)...", backoff, attempt, retryMax))
			time.Sleep(backoff)
			backoff *= 2
		}
		lastErr = runGitCapture(dir, progressFn(s, job.ID), args...)
		if lastErr == nil {
			return nil
		}
		if !isRetryable(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

// runGitCapture ejecuta git y entrega cada línea de stderr al callback de progreso.
// Las barras de progreso usan \r, así que una "línea" puede ser enorme:
// se entrega solo el final, que es el estado visible más reciente.
func runGitCapture(dir string, progress func(string), args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	lastLine := ""
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || progress == nil {
			continue
		}
		if len(line) > 160 {
			line = line[len(line)-160:]
		}
		lastLine = line
		progress(line)
	}

	if err := scanner.Err(); err != nil {
		// Si el stderr deja de leerse, git puede bloquearse en el pipe: se aborta.
		_ = cmd.Process.Kill()
		return err
	}
	if err := cmd.Wait(); err != nil {
		// Incluir el último mensaje de git (p. ej. "unable to access") para que
		// isRetryable clasifique el fallo; el error de Wait solo dice "exit 128".
		if lastLine != "" {
			return fmt.Errorf("%w: %s", err, lastLine)
		}
		return err
	}
	return nil
}
