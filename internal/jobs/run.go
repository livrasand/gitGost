package jobs

import (
	"fmt"
)

const maxErrorLen = 500

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
	default:
		runErr = runGitOperation(s, job)
	}

	if runErr != nil {
		_ = s.SetState(id, StateFailed)
		_ = s.SetError(id, truncate(runErr.Error(), maxErrorLen))
		return runErr
	}
	return s.SetState(id, StateCompleted)
}

func runGitOperation(s *Store, job *Job) error {
	args := append([]string{job.Operation}, job.Args...)
	return execGitWithRetry(s, job, job.CWD, args...)
}

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
