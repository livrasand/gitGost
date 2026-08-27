package github

import (
	"strings"

	"github.com/livrasand/gitGost/internal/github"
	"github.com/livrasand/gitGost/internal/provider"
)

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

func (p *GitHubProvider) CreateAnonymousDiscussionComment(owner, repo string, number int, body string) (string, error) {
	return github.CreateAnonymousDiscussionComment(owner, repo, number, body)
}

func (p *GitHubProvider) GetMRStatus(owner, repo string, number int) (*provider.MRStatus, error) {
	state, title, comments, updatedAt, err := github.FetchPRInfo(owner, repo, number)
	if err != nil {
		return nil, err
	}

	events, newETag, _, err := github.FetchPRTimeline(owner, repo, number, "")
	if err != nil {
		return &provider.MRStatus{
			State: state, Title: title, Number: number,
			Comments: comments, UpdatedAt: updatedAt, Events: []provider.Event{},
		}, nil
	}

	providerEvents := make([]provider.Event, 0, len(events))
	for i := range events {
		e := &events[i]
		ev := provider.Event{
			Type:        e.Event,
			Author:      e.Author(),
			Body:        e.Body,
			CreatedAt:   e.CreatedAt,
			StateReason: e.StateReason,
			CommitSHA:   shortSHA(e.CommitID),
		}
		switch e.Event {
		case "labeled", "unlabeled":
			if e.Label != nil {
				ev.Label = e.Label.Name
			}
		case "reviewed":
			switch strings.ToLower(e.State) {
			case "approved":
				ev.ReviewState = "approved"
			case "changes_requested":
				ev.ReviewState = "changes_requested"
			default:
				ev.ReviewState = "commented"
			}
		}
		ev.Target, ev.TargetURL = e.TargetInfo()
		ev.Summarize()
		providerEvents = append(providerEvents, ev)
	}
	provider.SortEvents(providerEvents)

	return &provider.MRStatus{
		State: state, Title: title, Number: number,
		Comments: comments, UpdatedAt: updatedAt,
		ETag:   newETag,
		Events: providerEvents,
	}, nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
