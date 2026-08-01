package jobs

import "time"

const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StatePaused    = "paused"
	StateRetrying  = "retrying"
	StateCompleted = "completed"
	StateFailed    = "failed"
)

type Job struct {
	ID        int64
	Operation string
	URL       string
	Origin    string
	Target    string
	CWD       string
	Args      []string
	State     string
	Progress  string
	Error     string
	PID       int
	CreatedAt time.Time
	UpdatedAt time.Time
}
