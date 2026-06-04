package github

import (
	"github.com/livrasand/gitGost/internal/github"
	"github.com/livrasand/gitGost/internal/provider"
)

// GitHubProvider implements provider.Provider using the existing github package.
// All real logic lives in internal/github/pr.go — this is a thin adapter.
type GitHubProvider struct{}

func New() *GitHubProvider {
	return &GitHubProvider{}
}

func (p *GitHubProvider) ForkRepo(owner, repo string) (string, error) {
	return github.ForkRepo(owner, repo)
}

func (p *GitHubProvider) CreateMR(owner, repo, branch, forkOwner, commitMessage string) (string, error) {
	return github.CreatePR(owner, repo, branch, forkOwner, commitMessage)
}

func (p *GitHubProvider) GetRefs(owner, repo string) ([]provider.Ref, error) {
	ghRefs, err := github.GetRefs(owner, repo)
	if err != nil {
		return nil, err
	}
	refs := make([]provider.Ref, len(ghRefs))
	for i, r := range ghRefs {
		refs[i] = provider.Ref{Ref: r.Ref, SHA: r.GetSha()}
	}
	return refs, nil
}

func (p *GitHubProvider) GetExistingMR(owner, repo, forkOwner, branchName string) (string, bool, error) {
	return github.GetExistingPR(owner, repo, forkOwner, branchName)
}

func (p *GitHubProvider) CloseMRByURL(mrURL string) error {
	return github.ClosePRByURL(mrURL)
}

func (p *GitHubProvider) GetRepoPolicy(owner, repo string) (*provider.RepoPolicy, error) {
	ghPolicy, err := github.GetRepoPolicy(owner, repo)
	if err != nil {
		return nil, err
	}
	if ghPolicy == nil {
		return &provider.RepoPolicy{}, nil
	}
	return &provider.RepoPolicy{DenyAll: ghPolicy.DenyAll}, nil
}

func (p *GitHubProvider) IsRepoVerified(owner, repo string) bool {
	return github.IsRepoVerified(owner, repo)
}

func (p *GitHubProvider) CloneURL(owner, repo string) string {
	return "https://github.com/" + owner + "/" + repo + ".git"
}

func (p *GitHubProvider) PushURL(forkOwner, repo string) string {
	return "https://github.com/" + forkOwner + "/" + repo + ".git"
}

func (p *GitHubProvider) TokenEnvVar() string {
	return "GITHUB_TOKEN"
}

func (p *GitHubProvider) Name() string {
	return "GitHub"
}

func (p *GitHubProvider) CreateAnonymousIssue(owner, repo, title, body string, labels []string) (string, int, error) {
	return github.CreateAnonymousIssue(owner, repo, title, body, labels)
}

func (p *GitHubProvider) CreateAnonymousComment(owner, repo string, number int, body string) (string, error) {
	return github.CreateAnonymousComment(owner, repo, number, body)
}

func (p *GitHubProvider) CreateAnonymousPRComment(owner, repo string, number int, body string) (string, error) {
	return github.CreateAnonymousPRComment(owner, repo, number, body)
}
