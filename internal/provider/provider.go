package provider

// Ref represents a git reference (branch or tag) with its SHA.
type Ref struct {
	Ref string
	SHA string
}

// RepoPolicy contains the directives read from .gitgost.yml in the target repository.
type RepoPolicy struct {
	DenyAll bool
}

// MRStatus contiene el estado actual de un Merge/Pull Request.
type MRStatus struct {
	State     string  `json:"state"`
	Title     string  `json:"title"`
	Number    int     `json:"number"`
	Comments  int     `json:"comments"`
	UpdatedAt string  `json:"updated_at"`
	ETag      string  `json:"etag,omitempty"`
	Events    []Event `json:"events"`
}

// Event representa un evento en el timeline de un MR/PR.
type Event struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Author    string `json:"author"`
	Body      string `json:"body,omitempty"`
	CreatedAt string `json:"created_at"`
}

// Provider abstracts the operations needed to anonymize contributions
// across different git hosting platforms (GitHub, GitLab, etc.).
type Provider interface {
	// ForkRepo creates (or returns existing) fork of owner/repo.
	// Returns the fork owner login/username.
	ForkRepo(owner, repo string) (forkOwner string, err error)

	// CreateMR creates a Merge/Pull Request from forkOwner:branch → owner/repo:main.
	// Returns the HTML URL of the created MR/PR.
	CreateMR(owner, repo, branch, forkOwner, commitMessage string) (url string, err error)

	// GetRefs returns all refs (branches/tags) for the given repository.
	GetRefs(owner, repo string) ([]Ref, error)

	// GetExistingMR checks if an open MR/PR already exists from forkOwner:branchName.
	// Returns (mrURL, branchExists, error).
	GetExistingMR(owner, repo, forkOwner, branchName string) (mrURL string, branchExists bool, err error)

	// CloseMRByURL closes an open MR/PR given its HTML URL.
	CloseMRByURL(mrURL string) error

	// GetRepoPolicy reads the .gitgost.yml policy file from the repository.
	GetRepoPolicy(owner, repo string) (*RepoPolicy, error)

	// IsRepoVerified checks if the repository has a .gitgost.yml file.
	IsRepoVerified(owner, repo string) bool

	// CloneURL returns the HTTPS clone URL for a repository.
	CloneURL(owner, repo string) string

	// PushURL returns the HTTPS push URL for a forked repository.
	PushURL(forkOwner, repo string) string

	// TokenEnvVar returns the name of the environment variable that holds the token.
	TokenEnvVar() string

	// Name returns a human-readable name for the provider (e.g. "GitHub", "GitLab").
	Name() string

	// CreateAnonymousIssue creates an issue and returns (issueURL, issueNumber, error).
	CreateAnonymousIssue(owner, repo, title, body string, labels []string) (string, int, error)

	// CreateAnonymousComment posts a comment on an issue and returns the comment URL.
	CreateAnonymousComment(owner, repo string, number int, body string) (string, error)

	// CreateAnonymousPRComment posts a comment on a PR/MR and returns the comment URL.
	CreateAnonymousPRComment(owner, repo string, number int, body string) (string, error)

	// CreateAnonymousDiscussionComment posts a comment on a GitHub Discussion and returns the comment URL.
	CreateAnonymousDiscussionComment(owner, repo string, number int, body string) (string, error)

	// GetMRStatus obtiene el estado actual de un MR/PR incluyendo su timeline de eventos.
	GetMRStatus(owner, repo string, number int) (*MRStatus, error)
}
