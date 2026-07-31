package jobs

import (
	"fmt"
)

// maxErrorLen limita el tamaño del mensaje de error persistido en la cola.
const maxErrorLen = 500

// Run ejecuta la operación Git de un job (invocado por `git gost run <id>`,
// normalmente como proceso en background) y actualiza su estado en el store.
// Los fallos de red se reintentan automáticamente (estado retrying); para el
// clone, la descarga es por capas y reanuda desde el último checkpoint.
func Run(s *Store, id int64) error {
	job, err := s.Get(id)
	if err != nil {
		return fmt.Errorf("obtener job %d: %w", id, err)
	}
	if job.State == StateCompleted || job.State == StateFailed {
		return fmt.Errorf("el job %d ya terminó (%s)", id, job.State)
	}
	if err := s.SetState(id, StateRunning); err != nil {
		return err
	}

	var runErr error
	switch job.Operation {
	case "clone":
		runErr = runClone(s, job)
	default: // fetch, pull, push
		runErr = runGitOperation(s, job)
	}

	if runErr != nil {
		_ = s.SetState(id, StateFailed)
		_ = s.SetError(id, truncate(runErr.Error(), maxErrorLen))
		return runErr
	}
	return s.SetState(id, StateCompleted)
}

// runGitOperation ejecuta fetch/pull/push en el repositorio actual con
// reintentos automáticos ante fallos de red.
func runGitOperation(s *Store, job *Job) error {
	args := append([]string{job.Operation}, job.Args...)
	return execGitWithRetry(s, job, job.CWD, args...)
}

// progressFn devuelve el callback que persiste una línea de progreso en la cola.
func progressFn(s *Store, id int64) func(string) {
	return func(line string) {
		_ = s.SetProgress(id, line)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
