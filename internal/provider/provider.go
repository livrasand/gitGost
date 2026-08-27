package provider

import (
	"sort"
	"time"
)

type Ref struct {
	Ref string
	SHA string
}

type RepoPolicy struct {
	DenyAll bool
}

type MRStatus struct {
	State     string  `json:"state"`
	Title     string  `json:"title"`
	Number    int     `json:"number"`
	Comments  int     `json:"comments"`
	UpdatedAt string  `json:"updated_at"`
	ETag      string  `json:"etag,omitempty"`
	Events    []Event `json:"events"`
}

type Event struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type"`
	Author      string `json:"author"`
	Body        string `json:"body,omitempty"`
	CreatedAt   string `json:"created_at"`
	Label       string `json:"label,omitempty"`
	StateReason string `json:"state_reason,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	ReviewState string `json:"review_state,omitempty"`
	Target      string `json:"target,omitempty"`
	TargetURL   string `json:"target_url,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

// Summarize fills e.Summary with a short human-readable description of the
// event so clients (CLI, ntfy notifications) can render a GitHub-like timeline
// without knowing provider-specific event names.
func (e *Event) Summarize() {
	if e.Summary != "" {
		return
	}
	who := e.Author
	if who == "" {
		who = "alguien"
	}
	switch e.Type {
	case "commented":
		e.Summary = who + " comentó"
	case "committed":
		e.Summary = who + " hizo push de un commit"
	case "reviewed":
		switch e.ReviewState {
		case "approved":
			e.Summary = who + " aprobó los cambios"
		case "changes_requested":
			e.Summary = who + " solicitó cambios"
		default:
			e.Summary = who + " revisó el código"
		}
	case "line-commented":
		e.Summary = who + " comentó una línea del diff"
	case "cross-referenced":
		if e.Target != "" {
			e.Summary = who + " referenció desde " + e.Target
		} else {
			e.Summary = who + " añadió una referencia cruzada"
		}
	case "labeled":
		e.Summary = who + " añadió la etiqueta " + e.Label
	case "unlabeled":
		e.Summary = who + " quitó la etiqueta " + e.Label
	case "closed":
		if e.StateReason == "completed" || e.StateReason == "" {
			e.Summary = who + " cerró como completado"
		} else {
			e.Summary = who + " cerró (" + e.StateReason + ")"
		}
	case "reopened":
		e.Summary = who + " reabrió"
	case "merged":
		e.Summary = who + " fusionó"
	case "assigned":
		e.Summary = who + " fue asignado"
	case "unassigned":
		e.Summary = who + " dejó de estar asignado"
	case "review_requested":
		e.Summary = who + " solicitó una revisión"
	case "review_request_removed":
		e.Summary = who + " retiró la solicitud de revisión"
	case "milestoned":
		e.Summary = who + " asignó al hito " + e.Target
	case "demilestoned":
		e.Summary = who + " quitó del hito " + e.Target
	case "head_ref_force_pushed":
		e.Summary = who + " forzó un push en la rama fuente"
	case "base_ref_force_pushed":
		e.Summary = who + " forzó un push en la rama base"
	case "head_ref_deleted":
		e.Summary = who + " eliminó la rama fuente"
	case "head_ref_restored":
		e.Summary = who + " restauró la rama fuente"
	case "converted_to_draft":
		e.Summary = who + " convirtió a borrador"
	case "ready_for_review":
		e.Summary = who + " marcó listo para revisión"
	case "auto_merge_enabled":
		e.Summary = who + " activó el auto-merge"
	case "auto_merge_disabled":
		e.Summary = who + " desactivó el auto-merge"
	case "connected":
		e.Summary = who + " vinculó con " + e.Target
	case "referenced":
		e.Summary = who + " referenció este cambio desde un commit"
	case "opened", "system":
		e.Summary = who + " actualizó el estado"
	default:
		e.Summary = who + ": " + e.Type
	}
}

// SortEvents orders events chronologically (oldest first). Events with an
// unparsable or empty timestamp keep their relative order at the end.
func SortEvents(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		ti, erri := time.Parse(time.RFC3339, events[i].CreatedAt)
		tj, errj := time.Parse(time.RFC3339, events[j].CreatedAt)
		if erri != nil || errj != nil {
			return false
		}
		return ti.Before(tj)
	})
}

type Provider interface {
	ForkRepo(owner, repo string) (forkOwner string, err error)
	CreateMR(owner, repo, branch, forkOwner, commitMessage string) (url string, err error)
	GetRefs(owner, repo string) ([]Ref, error)
	GetExistingMR(owner, repo, forkOwner, branchName string) (mrURL string, branchExists bool, err error)
	CloseMRByURL(mrURL string) error
	GetRepoPolicy(owner, repo string) (*RepoPolicy, error)
	IsRepoVerified(owner, repo string) bool
	CloneURL(owner, repo string) string
	PushURL(forkOwner, repo string) string
	TokenEnvVar() string
	Name() string
	CreateAnonymousIssue(owner, repo, title, body string, labels []string) (string, int, error)
	CreateAnonymousComment(owner, repo string, number int, body string) (string, error)
	CreateAnonymousPRComment(owner, repo string, number int, body string) (string, error)
	CreateAnonymousDiscussionComment(owner, repo string, number int, body string) (string, error)
	GetMRStatus(owner, repo string, number int) (*MRStatus, error)
}
