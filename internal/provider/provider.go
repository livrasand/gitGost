package provider

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
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Author    string `json:"author"`
	Body      string `json:"body,omitempty"`
	CreatedAt string `json:"created_at"`
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
