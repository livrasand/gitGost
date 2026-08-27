package gitgost

import (
	"github.com/livrasand/gitGost/internal/provider"
)

type GitGostProvider struct{}

func New() *GitGostProvider {
	return &GitGostProvider{}
}

func (p *GitGostProvider) ForkRepo(owner, repo string) (string, error) {
	return "", nil
}

func (p *GitGostProvider) CreateMR(owner, repo, branch, forkOwner, commitMessage string) (string, error) {
	return "", nil
}

func (p *GitGostProvider) GetRefs(owner, repo string) ([]provider.Ref, error) {
	return nil, nil
}

func (p *GitGostProvider) GetExistingMR(owner, repo, forkOwner, branchName string) (string, bool, error) {
	return "", false, nil
}

func (p *GitGostProvider) CloseMRByURL(mrURL string) error {
	return nil
}

func (p *GitGostProvider) GetRepoPolicy(owner, repo string) (*provider.RepoPolicy, error) {
	return &provider.RepoPolicy{}, nil
}

func (p *GitGostProvider) IsRepoVerified(owner, repo string) bool {
	return false
}

func (p *GitGostProvider) CloneURL(owner, repo string) string {
	return ""
}

func (p *GitGostProvider) PushURL(forkOwner, repo string) string {
	return ""
}

func (p *GitGostProvider) TokenEnvVar() string {
	return ""
}

func (p *GitGostProvider) Name() string {
	return "gitGost"
}

func (p *GitGostProvider) CreateAnonymousIssue(owner, repo, title, body string, labels []string) (string, int, error) {
	return "", 0, nil
}

func (p *GitGostProvider) CreateAnonymousComment(owner, repo string, number int, body string) (string, error) {
	return "", nil
}

func (p *GitGostProvider) CreateAnonymousPRComment(owner, repo string, number int, body string) (string, error) {
	return "", nil
}

func (p *GitGostProvider) CreateAnonymousDiscussionComment(owner, repo string, number int, body string) (string, error) {
	return "", nil
}

func (p *GitGostProvider) GetMRStatus(owner, repo string, number int) (*provider.MRStatus, error) {
	return nil, nil
}
