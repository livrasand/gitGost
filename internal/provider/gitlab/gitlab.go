package gitlab

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

var httpClient = &http.Client{Timeout: 60 * time.Second}

// ExtractMRIID extrae el IID de un MR de una URL de GitLab.
// Formato: https://gitlab.com/{owner}/{repo}/-/merge_requests/{iid}
func ExtractMRIID(mrURL string) int {
	trimmed := strings.TrimPrefix(mrURL, "https://gitlab.com/")
	parts := strings.Split(trimmed, "/-/merge_requests/")
	if len(parts) != 2 {
		return 0
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return n
}

// GitLabProvider implements provider.Provider for GitLab.
type GitLabProvider struct{}

func New() *GitLabProvider {
	return &GitLabProvider{}
}

func (p *GitLabProvider) Name() string {
	return "GitLab"
}

func (p *GitLabProvider) TokenEnvVar() string {
	return "GITLAB_TOKEN"
}

func (p *GitLabProvider) CloneURL(owner, repo string) string {
	return "https://gitlab.com/" + owner + "/" + repo + ".git"
}

func (p *GitLabProvider) PushURL(forkOwner, repo string) string {
	return "https://gitlab.com/" + forkOwner + "/" + repo + ".git"
}

// projectID returns the URL-encoded "namespace/project" identifier used by GitLab API.
func projectID(owner, repo string) string {
	return url.PathEscape(owner + "/" + repo)
}

func token() string {
	return os.Getenv("GITLAB_TOKEN")
}

func authHeader(req *http.Request) {
	t := token()
	if t != "" {
		req.Header.Set("PRIVATE-TOKEN", t)
	}
}

// ForkRepo forks owner/repo into the authenticated user's namespace.
// Returns the fork owner (username of the token holder).
func (p *GitLabProvider) ForkRepo(owner, repo string) (string, error) {
	t := token()
	if t == "" {
		return "", fmt.Errorf("GITLAB_TOKEN not set")
	}

	// Get current user login
	userReq, err := http.NewRequest("GET", "https://gitlab.com/api/v4/user", nil)
	if err != nil {
		return "", err
	}
	authHeader(userReq)
	userResp, err := httpClient.Do(userReq)
	if err != nil {
		return "", err
	}
	defer userResp.Body.Close()

	var user struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&user); err != nil {
		return "", err
	}
	if user.Username == "" {
		return "", fmt.Errorf("could not get GitLab username")
	}

	forkOwner := user.Username

	// Check if fork already exists
	checkURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s", projectID(forkOwner, repo))
	checkReq, err := http.NewRequest("GET", checkURL, nil)
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

	// Create fork
	forkURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/fork", projectID(owner, repo))
	forkReq, err := http.NewRequest("POST", forkURL, nil)
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

	if forkResp.StatusCode != http.StatusCreated && forkResp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("failed to fork GitLab project: %s", forkResp.Status)
	}

	return forkOwner, nil
}

// CreateMR creates a Merge Request from forkOwner:branch → owner/repo:main.
func (p *GitLabProvider) CreateMR(owner, repo, branch, forkOwner, commitMessage string) (string, error) {
	t := token()
	if t == "" {
		return "", fmt.Errorf("GITLAB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/merge_requests", projectID(owner, repo))

	mrBody := fmt.Sprintf("%s\n\n---\n\n*This is an anonymous contribution made via [gitGost](https://gitgost.fly.dev).*\n\n*The original author's identity has been anonymized to protect their privacy.*", commitMessage)

	payload := map[string]interface{}{
		"source_branch":       branch,
		"target_branch":       "main",
		"title":               "Anonymous contribution via gitGost",
		"description":         mrBody,
		"source_project_id":   fmt.Sprintf("%s/%s", forkOwner, repo),
		"allow_collaboration": true,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
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

	if resp.StatusCode != http.StatusCreated {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return "", fmt.Errorf("failed to create GitLab MR: %s", resp.Status)
	}

	var result struct {
		WebURL string `json:"web_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.WebURL, nil
}

// GetRefs returns all branches for the given GitLab project.
// Works without a token for public repos.
func (p *GitLabProvider) GetRefs(owner, repo string) ([]provider.Ref, error) {
	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/branches", projectID(owner, repo))
	req, err := http.NewRequest("GET", apiURL, nil)
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
		return nil, fmt.Errorf("failed to get GitLab refs: %s", resp.Status)
	}

	var branches []struct {
		Name   string `json:"name"`
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&branches); err != nil {
		return nil, err
	}

	refs := make([]provider.Ref, len(branches))
	for i, b := range branches {
		refs[i] = provider.Ref{
			Ref: "refs/heads/" + b.Name,
			SHA: b.Commit.ID,
		}
	}
	return refs, nil
}

// GetExistingMR checks if an open MR exists from forkOwner:branchName.
func (p *GitLabProvider) GetExistingMR(owner, repo, forkOwner, branchName string) (string, bool, error) {
	t := token()
	if t == "" {
		return "", false, fmt.Errorf("GITLAB_TOKEN not set")
	}

	// Check if branch exists in fork
	branchURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/branches/%s",
		projectID(forkOwner, repo), url.PathEscape(branchName))
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

	// Branch exists; search for open MR
	mrListURL := fmt.Sprintf(
		"https://gitlab.com/api/v4/projects/%s/merge_requests?state=opened&source_branch=%s&per_page=1",
		projectID(owner, repo), url.QueryEscape(branchName),
	)
	mrReq, err := http.NewRequest("GET", mrListURL, nil)
	if err != nil {
		return "", true, err
	}
	authHeader(mrReq)

	mrResp, err := httpClient.Do(mrReq)
	if err != nil {
		return "", true, err
	}
	defer mrResp.Body.Close()

	if mrResp.StatusCode != http.StatusOK {
		return "", true, fmt.Errorf("failed to list GitLab MRs: %s", mrResp.Status)
	}

	var mrs []struct {
		WebURL string `json:"web_url"`
	}
	if err := json.NewDecoder(mrResp.Body).Decode(&mrs); err != nil {
		return "", true, err
	}

	if len(mrs) == 0 {
		return "", true, nil
	}
	return mrs[0].WebURL, true, nil
}

// CloseMRByURL closes a GitLab MR given its web URL.
// Expected format: https://gitlab.com/{owner}/{repo}/-/merge_requests/{iid}
func (p *GitLabProvider) CloseMRByURL(mrURL string) error {
	t := token()
	if t == "" {
		return fmt.Errorf("GITLAB_TOKEN not set")
	}

	// Parse: https://gitlab.com/<owner>/<repo>/-/merge_requests/<iid>
	trimmed := strings.TrimPrefix(mrURL, "https://gitlab.com/")
	parts := strings.Split(trimmed, "/-/merge_requests/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid GitLab MR URL: %s", mrURL)
	}
	projectPath := parts[0]
	iid := parts[1]

	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/merge_requests/%s",
		url.PathEscape(projectPath), iid)

	payload, err := json.Marshal(map[string]string{"state_event": "close"})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", apiURL, bytes.NewBuffer(payload))
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to close GitLab MR %s: status %s", mrURL, resp.Status)
	}
	return nil
}

// GetRepoPolicy reads .gitgost.yml from a GitLab repository.
func (p *GitLabProvider) GetRepoPolicy(owner, repo string) (*provider.RepoPolicy, error) {
	t := token()
	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/files/.gitgost.yml/raw",
		projectID(owner, repo))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return &provider.RepoPolicy{}, nil
	}
	if t != "" {
		authHeader(req)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return &provider.RepoPolicy{}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &provider.RepoPolicy{}, nil
	}

	// Parse DENY_ALL from raw YAML content
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	content := buf.String()

	// Simple check: avoid importing yaml just for one field
	if strings.Contains(content, "DENY_ALL: true") {
		return &provider.RepoPolicy{DenyAll: true}, nil
	}
	return &provider.RepoPolicy{}, nil
}

// CreateAnonymousIssue creates an issue on a GitLab project.
func (p *GitLabProvider) CreateAnonymousIssue(owner, repo, title, body string, labels []string) (string, int, error) {
	t := token()
	if t == "" {
		return "", 0, fmt.Errorf("GITLAB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/issues", projectID(owner, repo))

	payload := map[string]interface{}{
		"title":       title,
		"description": body,
	}
	if len(labels) > 0 {
		payload["labels"] = strings.Join(labels, ",")
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", 0, err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
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

	if resp.StatusCode == http.StatusForbidden {
		return "", 0, fmt.Errorf("issues are disabled or not accessible in this GitLab repository")
	}
	if resp.StatusCode != http.StatusCreated {
		return "", 0, fmt.Errorf("failed to create GitLab issue: %s", resp.Status)
	}

	var result struct {
		IID    int    `json:"iid"`
		WebURL string `json:"web_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, err
	}
	return result.WebURL, result.IID, nil
}

// CreateAnonymousComment posts a note (comment) on a GitLab issue.
func (p *GitLabProvider) CreateAnonymousComment(owner, repo string, number int, body string) (string, error) {
	t := token()
	if t == "" {
		return "", fmt.Errorf("GITLAB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/issues/%d/notes", projectID(owner, repo), number)

	jsonData, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
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

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("failed to create GitLab issue note: %s", resp.Status)
	}

	var result struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return fmt.Sprintf("https://gitlab.com/%s/%s/-/issues/%d#note_%d", owner, repo, number, result.ID), nil
}

// CreateAnonymousDiscussionComment is not supported by GitLab; returns an error.
func (p *GitLabProvider) CreateAnonymousDiscussionComment(owner, repo string, number int, body string) (string, error) {
	return "", fmt.Errorf("GitLab does not support GitHub Discussions")
}

// CreateAnonymousPRComment posts a note (comment) on a GitLab merge request.
func (p *GitLabProvider) CreateAnonymousPRComment(owner, repo string, number int, body string) (string, error) {
	t := token()
	if t == "" {
		return "", fmt.Errorf("GITLAB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/merge_requests/%d/notes", projectID(owner, repo), number)

	jsonData, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
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

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("failed to create GitLab MR note: %s", resp.Status)
	}

	var result struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return fmt.Sprintf("https://gitlab.com/%s/%s/-/merge_requests/%d#note_%d", owner, repo, number, result.ID), nil
}

// GetMRStatus obtiene el estado actual de un MR desde GitLab.
func (p *GitLabProvider) GetMRStatus(owner, repo string, number int) (*provider.MRStatus, error) {
	t := token()
	if t == "" {
		return nil, fmt.Errorf("GITLAB_TOKEN not set")
	}

	pid := projectID(owner, repo)

	// Obtener info del MR
	mrURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/merge_requests/%d", pid, number)
	mrReq, _ := http.NewRequest("GET", mrURL, nil)
	authHeader(mrReq)
	mrResp, err := httpClient.Do(mrReq)
	if err != nil {
		return nil, err
	}
	defer mrResp.Body.Close()

	if mrResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab API returned status %d", mrResp.StatusCode)
	}

	var mr struct {
		State     string `json:"state"`
		Title     string `json:"title"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.NewDecoder(mrResp.Body).Decode(&mr); err != nil {
		return nil, err
	}

	// nextNotePageURL extrae la URL de la pagina siguiente del header Link de GitLab.
	nextNotePageURL := func(linkHeader string) string {
		if linkHeader == "" {
			return ""
		}
		for _, part := range strings.Split(linkHeader, ",") {
			part = strings.TrimSpace(part)
			if strings.Contains(part, `rel="next"`) {
				start := strings.Index(part, "<")
				end := strings.Index(part, ">")
				if start != -1 && end != -1 {
					return part[start+1 : end]
				}
			}
		}
		return ""
	}

	type note struct {
		ID     int    `json:"id"`
		Body   string `json:"body"`
		Author struct {
			Username string `json:"username"`
		} `json:"author"`
		CreatedAt string `json:"created_at"`
		System    bool   `json:"system"`
	}

	var allNotes []note
	notesURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/merge_requests/%d/notes?per_page=100&sort=desc", pid, number)
	for notesURL != "" {
		notesReq, _ := http.NewRequest("GET", notesURL, nil)
		authHeader(notesReq)
		notesResp, err := httpClient.Do(notesReq)
		if err != nil {
			// Si falla la primera pagina, retornar sin eventos
			if len(allNotes) == 0 {
				return &provider.MRStatus{
					State: mr.State, Title: mr.Title, Number: number,
					Comments: 0, UpdatedAt: mr.UpdatedAt, Events: []provider.Event{},
				}, nil
			}
			break
		}

		var page []note
		if err := json.NewDecoder(notesResp.Body).Decode(&page); err != nil {
			notesResp.Body.Close()
			if len(allNotes) == 0 {
				page = []note{}
			} else {
				break
			}
		}
		notesResp.Body.Close()

		allNotes = append(allNotes, page...)
		notesURL = nextNotePageURL(notesResp.Header.Get("Link"))
	}

	comments := 0
	events := make([]provider.Event, 0, len(allNotes))
	for _, n := range allNotes {
		eventType := "comment"
		if n.System {
			eventType = "system"
		}
		events = append(events, provider.Event{
			Type: eventType, Author: n.Author.Username,
			Body: n.Body, CreatedAt: n.CreatedAt,
		})
		if !n.System {
			comments++
		}
	}

	return &provider.MRStatus{
		State: mr.State, Title: mr.Title, Number: number,
		Comments: comments, UpdatedAt: mr.UpdatedAt, Events: events,
	}, nil
}

// IsRepoVerified checks if the GitLab repo has a .gitgost.yml file.
func (p *GitLabProvider) IsRepoVerified(owner, repo string) bool {
	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/files/.gitgost.yml/raw",
		projectID(owner, repo))
	resp, err := http.Get(apiURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
