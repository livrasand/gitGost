package jobs

import "time"

// Estados posibles de un job en la cola local.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StatePaused    = "paused"
	StateRetrying  = "retrying"
	StateCompleted = "completed"
	StateFailed    = "failed"
)

// Job representa una operación Git gestionada por la cola local de gitGost.
type Job struct {
	ID        int64
	Operation string // clone | fetch | pull | push
	URL       string // URL efectiva (reescrita al servidor gitGost cuando aplica)
	Origin    string // URL original del repositorio (para el remote origin del repo resultante)
	Target    string // directorio destino para clone; vacío para el resto
	CWD       string // directorio de trabajo de la operación
	Args      []string
	State     string
	Progress  string // última línea de salida de git (para watch)
	Error     string
	PID       int // PID del proceso en background; 0 si no aplica
	CreatedAt time.Time
	UpdatedAt time.Time
}
