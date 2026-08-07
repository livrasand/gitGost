package codeberg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/livrasand/gitGost/internal/provider"
)

const (
	apiBase = "https://codeberg.org/api/v1"
	host    = "codeberg.org"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

type CodebergProvider struct{}

func New() *CodebergProvider {
	return &CodebergProvider{}
}

func codebergToken() string {
	return os.Getenv("CODEBERG_TOKEN")
}

func authHeader(req *http.Request) {
	t := codebergToken()
	if t != "" {
		req.Header.Set("Authorization", "token "+t)
	}
}

func repoPath(owner, repo string) string {
	return apiBase + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

func (p *CodebergProvider) Name() string {
	return "Codeberg"
}

func (p *CodebergProvider) TokenEnvVar() string {
	return "CODEBERG_TOKEN"
}

func (p *CodebergProvider) CloneURL(owner, repo string) string {
	return fmt.Sprintf("https://%s/%s/%s.git", host, owner, repo)
}

func (p *CodebergProvider) PushURL(forkOwner, repo string) string {
	return fmt.Sprintf("https://%s/%s/%s.git", host, forkOwner, repo)
}

func (p *CodebergProvider) currentUser() (string, error) {
	if codebergToken() == "" {
		return "", fmt.Errorf("CODEBERG_TOKEN not set")
	}

	req, err := http.NewRequest("GET", apiBase+"/user", nil)
	if err != nil {
		return "", err
	}
	authHeader(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get Codeberg user: %s", resp.Status)
	}

	var user struct {
		Login    string `json:"login"`
		UserName string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", err
	}
	if user.Login != "" {
		return user.Login, nil
	}
	if user.UserName != "" {
		return user.UserName, nil
	}
	return "", fmt.Errorf("could not get Codeberg username")
}

func (p *CodebergProvider) getDefaultBranch(owner, repo string) (string, error) {
	req, err := http.NewRequest("GET", repoPath(owner, repo), nil)
	if err != nil {
		return "", err
	}
	authHeader(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get Codeberg repo info: %s", resp.Status)
	}

	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.DefaultBranch == "" {
		return "main", nil
	}
	return r.DefaultBranch, nil
}

func (p *CodebergProvider) ForkRepo(owner, repo string) (string, error) {
	forkOwner, err := p.currentUser()
	if err != nil {
		return "", err
	}

	checkReq, err := http.NewRequest("GET", repoPath(forkOwner, repo), nil)
	if err != nil {
		return "", err
	}
	authHeader(checkReq)
	checkResp, err := httpClient.Do(checkReq)
	if err != nil {
		return "", err
	}
	checkResp.Body.Close()
	if checkResp.StatusCode == http.StatusOK {
		return forkOwner, nil
	}

	forkReq, err := http.NewRequest("POST", repoPath(owner, repo)+"/forks", nil)
	if err != nil {
		return "", err
	}
	authHeader(forkReq)
	forkReq.Header.Set("Content-Type", "application/json")

	forkResp, err := httpClient.Do(forkReq)
	if err != nil {
		return "", err
	}
	defer forkResp.Body.Close()

	if forkResp.StatusCode != http.StatusOK && forkResp.StatusCode != http.StatusCreated && forkResp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("failed to fork Codeberg repo: %s", forkResp.Status)
	}

	if forkResp.StatusCode == http.StatusAccepted {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(1 * time.Second)
			pollReq, _ := http.NewRequest("GET", repoPath(forkOwner, repo), nil)
			authHeader(pollReq)
			pollResp, err := httpClient.Do(pollReq)
			if err != nil {
				continue
			}
			pollResp.Body.Close()
			if pollResp.StatusCode == http.StatusOK {
				return forkOwner, nil
			}
		}
	}

	return forkOwner, nil
}

func (p *CodebergProvider) CreateMR(owner, repo, branch, forkOwner, commitMessage string) (string, error) {
	if codebergToken() == "" {
		return "", fmt.Errorf("CODEBERG_TOKEN not set")
	}

	base, err := p.getDefaultBranch(owner, repo)
	if err != nil {
		return "", err
	}

	title := "Anonymous contribution via gitGost"
	body := commitMessage
	if idx := strings.Index(commitMessage, "\n"); idx > 0 {
		title = strings.TrimSpace(commitMessage[:idx])
		body = strings.TrimSpace(commitMessage[idx+1:])
	} else if strings.TrimSpace(commitMessage) != "" {
		title = strings.TrimSpace(commitMessage)
		body = ""
	}

	body += "\n\n---\n\n*This is an anonymous contribution made via [gitGost](https://gitgost.livrasand.com).*\n\n*The original author's identity has been anonymized to protect their privacy. This is a service account that allows real humans to contribute anonymously.*"

	payload := map[string]interface{}{
		"title": title,
		"head":  forkOwner + ":" + branch,
		"base":  base,
		"body":  body,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", repoPath(owner, repo)+"/pulls", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	authHeader(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("failed to create Codeberg pull request: %s", resp.Status)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.HTMLURL != "" {
		return result.HTMLURL, nil
	}
	return fmt.Sprintf("https://%s/%s/%s/pulls/%d", host, owner, repo, result.Number), nil
}

func (p *CodebergProvider) GetRefs(owner, repo string) ([]provider.Ref, error) {
	req, err := http.NewRequest("GET", repoPath(owner, repo)+"/git/refs", nil)
	if err != nil {
		return nil, err
	}
	authHeader(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get Codeberg refs: %s", resp.Status)
	}

	var refs []struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refs); err != nil {
		return nil, err
	}

	out := make([]provider.Ref, 0, len(refs))
	for _, r := range refs {
		out = append(out, provider.Ref{Ref: r.Ref, SHA: r.Object.SHA})
	}
	return out, nil
}

func (p *CodebergProvider) GetExistingMR(owner, repo, forkOwner, branchName string) (string, bool, error) {
	if codebergToken() == "" {
		return "", false, fmt.Errorf("CODEBERG_TOKEN not set")
	}

	branchURL := repoPath(forkOwner, repo) + "/branches/" + url.PathEscape(branchName)
	branchReq, err := http.NewRequest("GET", branchURL, nil)
	if err != nil {
		return "", false, err
	}
	authHeader(branchReq)
	branchResp, err := httpClient.Do(branchReq)
	if err != nil {
		return "", false, err
	}
	branchResp.Body.Close()
	if branchResp.StatusCode != http.StatusOK {
		return "", false, nil
	}

	base, err := p.getDefaultBranch(owner, repo)
	if err != nil {
		return "", true, err
	}

	lookupURL := repoPath(owner, repo) + "/pulls/" + url.PathEscape(base) + "/" + url.PathEscape(forkOwner+":"+branchName)
	lookupReq, err := http.NewRequest("GET", lookupURL, nil)
	if err != nil {
		return "", true, err
	}
	authHeader(lookupReq)
	lookupResp, err := httpClient.Do(lookupReq)
	if err != nil {
		return "", true, err
	}
	defer lookupResp.Body.Close()

	if lookupResp.StatusCode == http.StatusOK {
		var pr struct {
			State   string `json:"state"`
			HTMLURL string `json:"html_url"`
		}
		if err := json.NewDecoder(lookupResp.Body).Decode(&pr); err != nil {
			return "", true, err
		}
		if pr.State == "open" {
			return pr.HTMLURL, true, nil
		}
		return "", true, nil
	}

	listURL := repoPath(owner, repo) + "/pulls?state=open&limit=100"
	listReq, err := http.NewRequest("GET", listURL, nil)
	if err != nil {
		return "", true, err
	}
	authHeader(listReq)
	listResp, err := httpClient.Do(listReq)
	if err != nil {
		return "", true, err
	}
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		return "", true, fmt.Errorf("failed to list Codeberg pull requests: %s", listResp.Status)
	}

	var prs []struct {
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			Label string `json:"label"`
		} `json:"head"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&prs); err != nil {
		return "", true, err
	}

	headLabel := forkOwner + ":" + branchName
	for _, pr := range prs {
		if pr.State == "open" && pr.Head.Label == headLabel {
			return pr.HTMLURL, true, nil
		}
	}
	return "", true, nil
}

func (p *CodebergProvider) CloseMRByURL(mrURL string) error {
	if codebergToken() == "" {
		return fmt.Errorf("CODEBERG_TOKEN not set")
	}

	owner, repo, number, err := parsePullURL(mrURL)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(map[string]string{"state": "closed"})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PATCH", repoPath(owner, repo)+"/pulls/"+strconv.Itoa(number), bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	authHeader(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to close Codeberg pull request: %s", resp.Status)
	}
	return nil
}

func ExtractPRNumber(mrURL string) int {
	_, _, n, err := parsePullURL(mrURL)
	if err != nil {
		return 0
	}
	return n
}

func parsePullURL(mrURL string) (owner, repo string, number int, err error) {
	u, err := url.Parse(mrURL)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid Codeberg URL: %s", mrURL)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pulls" {
		return "", "", 0, fmt.Errorf("invalid Codeberg pull request URL: %s", mrURL)
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid Codeberg pull request number: %s", mrURL)
	}
	return parts[0], parts[1], n, nil
}

func (p *CodebergProvider) GetRepoPolicy(owner, repo string) (*provider.RepoPolicy, error) {
	req, err := http.NewRequest("GET", repoPath(owner, repo)+"/raw/.gitgost.yml", nil)
	if err != nil {
		return &provider.RepoPolicy{}, nil
	}
	authHeader(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return &provider.RepoPolicy{}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &provider.RepoPolicy{}, nil
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	content := buf.String()

	if strings.Contains(content, "DENY_ALL: true") {
		return &provider.RepoPolicy{DenyAll: true}, nil
	}
	return &provider.RepoPolicy{}, nil
}

func (p *CodebergProvider) IsRepoVerified(owner, repo string) bool {
	req, err := http.NewRequest("GET", repoPath(owner, repo)+"/raw/.gitgost.yml", nil)
	if err != nil {
		return false
	}
	authHeader(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *CodebergProvider) CreateAnonymousIssue(owner, repo, title, body string, labels []string) (string, int, error) {
	if codebergToken() == "" {
		return "", 0, fmt.Errorf("CODEBERG_TOKEN not set")
	}

	payload := map[string]interface{}{
		"title": title,
		"body":  body,
	}
	if len(labels) > 0 {
		labelIDs, err := p.getLabelIDs(owner, repo, labels)
		if err != nil {
			return "", 0, err
		}
		if len(labelIDs) > 0 {
			payload["labels"] = labelIDs
		}
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", 0, err
	}

	req, err := http.NewRequest("POST", repoPath(owner, repo)+"/issues", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", 0, err
	}
	authHeader(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", 0, fmt.Errorf("failed to create Codeberg issue: %s", resp.Status)
	}

	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, err
	}
	return result.HTMLURL, result.Number, nil
}

func (p *CodebergProvider) getLabelIDs(owner, repo string, labels []string) ([]int64, error) {
	req, err := http.NewRequest("GET", repoPath(owner, repo)+"/labels?limit=50", nil)
	if err != nil {
		return nil, err
	}
	authHeader(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get Codeberg issue labels: %s", resp.Status)
	}

	var available []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&available); err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(labels))
	for _, requested := range labels {
		requested = strings.TrimSpace(requested)
		if requested == "" {
			continue
		}
		for _, label := range available {
			if strings.EqualFold(label.Name, requested) {
				ids = append(ids, label.ID)
				break
			}
		}
	}
	return ids, nil
}

func (p *CodebergProvider) CreateAnonymousComment(owner, repo string, number int, body string) (string, error) {
	return p.createIssueComment(owner, repo, number, body)
}

func (p *CodebergProvider) CreateAnonymousPRComment(owner, repo string, number int, body string) (string, error) {
	return p.createIssueComment(owner, repo, number, body)
}

func (p *CodebergProvider) createIssueComment(owner, repo string, number int, body string) (string, error) {
	if codebergToken() == "" {
		return "", fmt.Errorf("CODEBERG_TOKEN not set")
	}

	payload := map[string]string{"body": body}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", repoPath(owner, repo)+"/issues/"+strconv.Itoa(number)+"/comments", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	authHeader(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("failed to create Codeberg comment: %s", resp.Status)
	}

	var result struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.HTMLURL != "" {
		return result.HTMLURL, nil
	}
	return fmt.Sprintf("https://%s/%s/%s/issues/%d#issuecomment-%d", host, owner, repo, number, result.ID), nil
}

func (p *CodebergProvider) CreateAnonymousDiscussionComment(owner, repo string, number int, body string) (string, error) {
	return "", fmt.Errorf("codeberg does not support GitHub-style Discussions")
}

func (p *CodebergProvider) GetMRStatus(owner, repo string, number int) (*provider.MRStatus, error) {
	if codebergToken() == "" {
		return nil, fmt.Errorf("CODEBERG_TOKEN not set")
	}

	prURL := repoPath(owner, repo) + "/pulls/" + strconv.Itoa(number)
	prReq, err := http.NewRequest("GET", prURL, nil)
	if err != nil {
		return nil, err
	}
	authHeader(prReq)

	prResp, err := httpClient.Do(prReq)
	if err != nil {
		return nil, err
	}
	defer prResp.Body.Close()

	if prResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get Codeberg pull request: %s", prResp.Status)
	}

	var pr struct {
		State     string `json:"state"`
		Title     string `json:"title"`
		Number    int    `json:"number"`
		Comments  int    `json:"comments"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.NewDecoder(prResp.Body).Decode(&pr); err != nil {
		return nil, err
	}

	commentsURL := repoPath(owner, repo) + "/issues/" + strconv.Itoa(number) + "/comments"
	commentsReq, err := http.NewRequest("GET", commentsURL, nil)
	if err != nil {
		return nil, err
	}
	authHeader(commentsReq)

	commentsResp, err := httpClient.Do(commentsReq)
	if err != nil {
		return &provider.MRStatus{
			State: pr.State, Title: pr.Title, Number: number,
			Comments: pr.Comments, UpdatedAt: pr.UpdatedAt, Events: []provider.Event{},
		}, nil
	}
	defer commentsResp.Body.Close()

	var events []provider.Event
	if commentsResp.StatusCode == http.StatusOK {
		var raw []struct {
			ID        int64  `json:"id"`
			Body      string `json:"body"`
			CreatedAt string `json:"created_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.NewDecoder(commentsResp.Body).Decode(&raw); err == nil {
			events = make([]provider.Event, 0, len(raw))
			for _, c := range raw {
				events = append(events, provider.Event{
					Type:      "comment",
					Author:    c.User.Login,
					Body:      c.Body,
					CreatedAt: c.CreatedAt,
				})
			}
		}
	}

	return &provider.MRStatus{
		State: pr.State, Title: pr.Title, Number: number,
		Comments: pr.Comments, UpdatedAt: pr.UpdatedAt,
		ETag:   commentsResp.Header.Get("ETag"),
		Events: events,
	}, nil
}
