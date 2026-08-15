package github

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/livrasand/gitGost/internal/tokenpool"
	"gopkg.in/yaml.v3"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

// githubDo executes a request with the given token and marks the token as
// rate-limited when GitHub returns 403/429 (rate limit exceeded).
func githubDo(req *http.Request, token string) (*http.Response, error) {
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := httpClient.Do(req)
	if err == nil && resp != nil && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) {
		tokenpool.MarkGitHubRateLimited(token, resp.Header.Get("X-RateLimit-Reset"))
	}
	return resp, err
}

type Ref struct {
	Ref    string `json:"ref"`
	Object struct {
		Sha string `json:"sha"`
	} `json:"object"`
}

func isTimeout(err error) bool {
	if err, ok := err.(net.Error); ok && err.Timeout() {
		return true
	}
	return false
}

func UpdateCommentsKarmaByHash(hash string, karma int) error {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN not set")
	}

	query := url.QueryEscape(fmt.Sprintf("goster-%s in:comments", hash))
	searchURL := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=10", query)
	var resp *http.Response
	var err error
	delay := time.Second
	for attempt := 0; attempt < 3; attempt++ {
		var req *http.Request
		req, err = http.NewRequest("GET", searchURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "gitGost")

		resp, err = githubDo(req, token)
		if err == nil {
			break
		}
		if !isTimeout(err) || attempt == 2 {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("search failed: %s", resp.Status)
	}

	var result struct {
		Items []struct {
			Number        int    `json:"number"`
			RepositoryURL string `json:"repository_url"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	re := regexp.MustCompile(fmt.Sprintf(`(?m)goster-%s \u00b7 karma \(\d+\) \u00b7 \[report\]\(([^)]+)\)`, regexp.QuoteMeta(hash)))

	for _, item := range result.Items {
		parts := strings.Split(item.RepositoryURL, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]
		commentsURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, item.Number)

		creq, err := http.NewRequest("GET", commentsURL, nil)
		if err != nil {
			continue
		}
		creq.Header.Set("Accept", "application/vnd.github+json")
		creq.Header.Set("User-Agent", "gitGost")

		var cresp *http.Response
		delay := time.Second
		for attempt := 0; attempt < 3; attempt++ {
			cresp, err = githubDo(creq, token)
			if err == nil {
				break
			}
			if !isTimeout(err) || attempt == 2 {
				break
			}
			time.Sleep(delay)
			delay *= 2
		}
		if err != nil || cresp == nil {
			continue
		}
		if cresp.StatusCode != http.StatusOK {
			cresp.Body.Close()
			continue
		}

		var comments []struct {
			ID   int    `json:"id"`
			Body string `json:"body"`
		}
		if err := json.NewDecoder(cresp.Body).Decode(&comments); err != nil {
			cresp.Body.Close()
			continue
		}
		cresp.Body.Close()

		for _, cmt := range comments {
			if !strings.Contains(cmt.Body, hash) {
				continue
			}
			link := "#"
			if m := re.FindStringSubmatch(cmt.Body); len(m) == 2 {
				link = m[1]
			}
			legend := fmt.Sprintf("goster-%s \u00b7 karma (%d) \u00b7 [report](%s)", hash, karma, link)
			newBody := re.ReplaceAllString(cmt.Body, legend)
			if newBody == cmt.Body {
				continue
			}

			payload := map[string]string{"body": newBody}
			jsonData, err := json.Marshal(payload)
			if err != nil {
				continue
			}

			patchURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/comments/%d", owner, repo, cmt.ID)
			preq, err := http.NewRequest("PATCH", patchURL, bytes.NewBuffer(jsonData))
			if err != nil {
				continue
			}
			preq.Header.Set("Content-Type", "application/json")

			presp, err := githubDo(preq, token)
			if err != nil {
				continue
			}
			presp.Body.Close()
		}
	}

	return nil
}

func DeleteCommentsByHash(hash string) error {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN not set")
	}
	query := url.QueryEscape(fmt.Sprintf("goster-%s in:comments", hash))
	searchURL := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=20", query)

	var resp *http.Response
	var err error
	delay := time.Second
	for attempt := 0; attempt < 3; attempt++ {
		var req *http.Request
		req, err = http.NewRequest("GET", searchURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "gitGost")

		resp, err = githubDo(req, token)
		if err == nil {
			break
		}
		if !isTimeout(err) || attempt == 2 {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("search failed: %s", resp.Status)
	}

	var result struct {
		Items []struct {
			Number        int    `json:"number"`
			RepositoryURL string `json:"repository_url"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	for _, item := range result.Items {
		parts := strings.Split(item.RepositoryURL, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]
		commentsURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, item.Number)
		creq, err := http.NewRequest("GET", commentsURL, nil)
		if err != nil {
			continue
		}
		creq.Header.Set("Accept", "application/vnd.github+json")
		creq.Header.Set("User-Agent", "gitGost")
		cresp, err := githubDo(creq, token)
		if err != nil {
			continue
		}
		if cresp.StatusCode != http.StatusOK {
			cresp.Body.Close()
			continue
		}
		var comments []struct {
			ID   int    `json:"id"`
			Body string `json:"body"`
		}
		if err := json.NewDecoder(cresp.Body).Decode(&comments); err != nil {
			cresp.Body.Close()
			continue
		}
		cresp.Body.Close()
		for _, cmt := range comments {
			if !strings.Contains(cmt.Body, hash) {
				continue
			}
			deleteURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/comments/%d", owner, repo, cmt.ID)
			preq, err := http.NewRequest("DELETE", deleteURL, nil)
			if err != nil {
				continue
			}
			preq.Header.Set("Accept", "application/vnd.github+json")
			preq.Header.Set("User-Agent", "gitGost")

			presp, err := githubDo(preq, token)
			if err != nil {
				continue
			}
			presp.Body.Close()
			if presp.StatusCode != http.StatusNoContent {
				continue
			}
		}
	}

	return nil
}

func CreateAnonymousIssue(owner, repo, title, body string, labels []string) (string, int, error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return "", 0, fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues", owner, repo)

	issueBody := fmt.Sprintf("%s\n\n---\n\n*This is an anonymous contribution made via [gitGost](https://gitgost.livrasand.com).\n\n*The original author's identity has been anonymized to protect their privacy. This is a service account that allows real humans to contribute anonymously.*", body)

	payload := map[string]interface{}{
		"title":  title,
		"body":   issueBody,
		"labels": labels,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", 0, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", 0, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := githubDo(req, token)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return "", 0, fmt.Errorf("failed to create issue: %s", resp.Status)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, err
	}

	return result.HTMLURL, result.Number, nil
}

func CreateAnonymousComment(owner, repo string, number int, body string) (string, error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, number)

	payload := map[string]string{"body": body}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := githubDo(req, token)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return "", fmt.Errorf("failed to create comment: %s", resp.Status)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.HTMLURL, nil
}

func CreateAnonymousPRComment(owner, repo string, number int, body string) (string, error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, number)

	payload := map[string]string{"body": body}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := githubDo(req, token)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return "", fmt.Errorf("failed to create PR comment: %s", resp.Status)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.HTMLURL, nil
}

func CreateAnonymousDiscussionComment(owner, repo string, number int, body string) (string, error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	idQuery := fmt.Sprintf(`{
		repository(owner: %q, name: %q) {
			discussion(number: %d) { id }
		}
	}`, owner, repo, number)
	idPayload := map[string]string{"query": idQuery}
	idJSON, err := json.Marshal(idPayload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewBuffer(idJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "bearer "+token)

	resp, err := githubDo(req, token)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to resolve discussion id: %s", resp.Status)
	}

	var idResp struct {
		Data struct {
			Repository struct {
				Discussion struct {
					ID string `json:"id"`
				} `json:"discussion"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&idResp); err != nil {
		return "", err
	}
	if len(idResp.Errors) > 0 {
		return "", fmt.Errorf("graphql: %s", idResp.Errors[0].Message)
	}
	discussionID := idResp.Data.Repository.Discussion.ID
	if discussionID == "" {
		return "", fmt.Errorf("discussion not found")
	}

	mutation := fmt.Sprintf(`mutation {
		addDiscussionComment(input: {discussionId: %q, body: %q}) {
			comment { url }
		}
	}`, discussionID, body)
	mutPayload := map[string]string{"query": mutation}
	mutJSON, err := json.Marshal(mutPayload)
	if err != nil {
		return "", err
	}

	mreq, err := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewBuffer(mutJSON))
	if err != nil {
		return "", err
	}
	mreq.Header.Set("Content-Type", "application/json")
	mreq.Header.Set("Authorization", "bearer "+token)

	mresp, err := githubDo(mreq, token)
	if err != nil {
		return "", err
	}
	defer mresp.Body.Close()

	if mresp.StatusCode != 200 {
		return "", fmt.Errorf("failed to create discussion comment: %s", mresp.Status)
	}

	var mutResp struct {
		Data struct {
			AddDiscussionComment struct {
				Comment struct {
					URL string `json:"url"`
				} `json:"comment"`
			} `json:"addDiscussionComment"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(mresp.Body).Decode(&mutResp); err != nil {
		return "", err
	}
	if len(mutResp.Errors) > 0 {
		return "", fmt.Errorf("graphql: %s", mutResp.Errors[0].Message)
	}

	return mutResp.Data.AddDiscussionComment.Comment.URL, nil
}

func (r *Ref) GetSha() string {
	return r.Object.Sha
}

func ForkRepo(owner, repo string) (string, error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	userURL := "https://api.github.com/user"
	req, err := http.NewRequest("GET", userURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+token)

	resp, err := githubDo(req, token)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var user map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", err
	}

	forkOwner, ok := user["login"].(string)
	if !ok {
		return "", fmt.Errorf("could not get user login")
	}

	forkURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", forkOwner, repo)
	req, err = http.NewRequest("GET", forkURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+token)

	resp, err = githubDo(req, token)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	if resp.StatusCode == 200 {
		fmt.Printf("DEBUG: Fork already exists: %s/%s\n", forkOwner, repo)
		return forkOwner, nil
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/forks", owner, repo)
	req, err = http.NewRequest("POST", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = githubDo(req, token)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 202 {
		return "", fmt.Errorf("failed to create fork: %s", resp.Status)
	}

	fmt.Printf("DEBUG: Fork created: %s/%s\n", forkOwner, repo)
	return forkOwner, nil
}

func ClosePRByURL(prURL string) error {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN not set")
	}

	parts := strings.Split(strings.TrimPrefix(prURL, "https://github.com/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return fmt.Errorf("invalid PR URL: %s", prURL)
	}
	owner := parts[0]
	repo := parts[1]
	number := parts[3]

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%s", owner, repo, number)
	payload, err := json.Marshal(map[string]string{"state": "closed"})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PATCH", apiURL, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := githubDo(req, token)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to close PR %s: status %s", prURL, resp.Status)
	}
	return nil
}

func CreatePR(owner, repo, branch, forkOwner, commitMessage string) (string, error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo)

	prBody := fmt.Sprintf("%s\n\n---\n\n*This is an anonymous contribution made via [gitGost](https://gitgost.livrasand.com).\n\n*The original author's identity has been anonymized to protect their privacy. This is a service account that allows real humans to contribute anonymously.*", commitMessage)

	data := map[string]interface{}{
		"title": "Anonymous contribution via gitGost",
		"head":  fmt.Sprintf("%s:%s", forkOwner, branch),
		"base":  "main",
		"body":  prBody,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := githubDo(req, token)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		fmt.Printf("DEBUG: PR creation failed: %s, response: %+v\n", resp.Status, errResp)
		return "", fmt.Errorf("Failed to create PR: %s", resp.Status)
	}

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return "", err
	}

	prURL, ok := result["html_url"].(string)
	if !ok {
		return "", fmt.Errorf("Invalid response from GitHub")
	}

	return prURL, nil
}

func GetRefs(owner, repo string) ([]Ref, error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/refs", owner, repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := githubDo(req, token)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 409 {
		return []Ref{}, nil
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Failed to get refs: %s", resp.Status)
	}

	var refs []Ref
	err = json.NewDecoder(resp.Body).Decode(&refs)
	if err != nil {
		return nil, err
	}

	return refs, nil
}

type RepoPolicy struct {
	DenyAll bool `yaml:"DENY_ALL"`
}

func GetRepoPolicy(owner, repo string) (*RepoPolicy, error) {
	token := tokenpool.NextGitHubToken()
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/.gitgost.yml", owner, repo)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return &RepoPolicy{}, nil
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := githubDo(req, token)
	if err != nil {
		return &RepoPolicy{}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &RepoPolicy{}, nil
	}

	var fileResp struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fileResp); err != nil {
		return &RepoPolicy{}, nil
	}

	var raw []byte
	if fileResp.Encoding == "base64" {
		cleaned := strings.ReplaceAll(fileResp.Content, "\n", "")
		raw, err = base64.StdEncoding.DecodeString(cleaned)
		if err != nil {
			return &RepoPolicy{}, nil
		}
	} else {
		raw = []byte(fileResp.Content)
	}

	var policy RepoPolicy
	if err := yaml.Unmarshal(raw, &policy); err != nil {
		return &RepoPolicy{}, nil
	}

	return &policy, nil
}

func IsRepoVerified(owner, repo string) bool {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/.gitgost.yml", owner, repo)
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func GeneratePRHash(owner, repo, branch string) string {
	input := fmt.Sprintf("%s/%s/%s", owner, repo, branch)
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:8]
}

type PRTimelineEvent struct {
	Event     string `json:"event"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body,omitempty"`
	User      *struct {
		Login string `json:"login"`
	} `json:"user,omitempty"`
	State string `json:"state,omitempty"`
	Label *struct {
		Name string `json:"name"`
	} `json:"label,omitempty"`
}

func ExtractPRNumber(prURL string) int {
	parts := strings.Split(strings.TrimPrefix(prURL, "https://github.com/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return 0
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return 0
	}
	return n
}

func nextPageURL(linkHeader string) string {
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

func FetchPRTimeline(owner, repo string, number int, etag string) (events []PRTimelineEvent, newETag string, changed bool, err error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return nil, "", false, fmt.Errorf("GITHUB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/timeline?per_page=100", owner, repo, number)

	for apiURL != "" {
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return nil, "", false, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "gitGost")
		if etag != "" && newETag == "" {
			req.Header.Set("If-None-Match", etag)
		}

		resp, err := githubDo(req, token)
		if err != nil {
			return nil, "", false, err
		}

		if newETag == "" {
			newETag = resp.Header.Get("ETag")
		}

		if resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			return nil, newETag, false, nil
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, "", false, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
		}

		var page []PRTimelineEvent
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, "", false, err
		}
		resp.Body.Close()

		if page != nil {
			events = append(events, page...)
		}

		apiURL = nextPageURL(resp.Header.Get("Link"))
	}

	if events == nil {
		events = []PRTimelineEvent{}
	}

	return events, newETag, true, nil
}

func FetchPRInfo(owner, repo string, number int) (state, title string, comments int, updatedAt string, err error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return "", "", 0, "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, number)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", "", 0, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := githubDo(req, token)
	if err != nil {
		return "", "", 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", 0, "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var result struct {
		State          string  `json:"state"`
		Title          string  `json:"title"`
		ReviewComments int     `json:"review_comments"`
		UpdatedAt      string  `json:"updated_at"`
		MergedAt       *string `json:"merged_at"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", 0, "", err
	}

	state = result.State
	if result.MergedAt != nil {
		state = "merged"
	}

	return state, result.Title, result.ReviewComments, result.UpdatedAt, nil
}

func GetExistingPR(owner, repo, forkOwner, branchName string) (string, bool, error) {
	token := tokenpool.NextGitHubToken()
	if token == "" {
		return "", false, fmt.Errorf("GITHUB_TOKEN not set")
	}

	branchURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/branches/%s", forkOwner, repo, branchName)
	req, err := http.NewRequest("GET", branchURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := githubDo(req, token)
	if err != nil {
		return "", false, err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, nil
	}

	head := fmt.Sprintf("%s:%s", forkOwner, branchName)
	prListURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=open&head=%s&per_page=1",
		owner, repo, url.QueryEscape(head))

	req, err = http.NewRequest("GET", prListURL, nil)
	if err != nil {
		return "", true, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err = githubDo(req, token)
	if err != nil {
		return "", true, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", true, fmt.Errorf("failed to list PRs: %s", resp.Status)
	}

	var prs []struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return "", true, err
	}

	if len(prs) == 0 {
		return "", true, nil
	}

	return prs[0].HTMLURL, true, nil
}

// IssueTemplate represents a GitHub issue template
// Supports both Markdown templates (.md) and Issue Forms (.yml/.yaml)
type IssueTemplate struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Content     string           `json:"content"` // For markdown templates
	Body        []IssueFormField `json:"body"`    // For issue forms
	About       string           `json:"about"`
	Labels      []string         `json:"labels"`
	Assignees   []string         `json:"assignees"`
	Title       string           `json:"title"`
	Filename    string           `json:"filename"`
	Type        string           `json:"type"` // "markdown" or "form"
}

// IssueFormField represents a field in a GitHub Issue Form
type IssueFormField struct {
	Type        string               `json:"type"` // input, textarea, dropdown, checkboxes, markdown
	ID          string               `json:"id"`
	Attributes  IssueFormAttributes  `json:"attributes"`
	Validations IssueFormValidations `json:"validations"`
}

// IssueFormAttributes holds the attributes for a form field
type IssueFormAttributes struct {
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Placeholder string   `json:"placeholder"`
	Value       string   `json:"value"`
	Required    bool     `json:"required"`
	Options     []string `json:"options"`
	Multiple    bool     `json:"multiple"`
}

// IssueFormValidations holds validation rules for a form field
type IssueFormValidations struct {
	Required bool `json:"required"`
}

// IssueTemplatesResponse represents the response from GitHub's issue templates API
type IssueTemplatesResponse struct {
	Templates          []IssueTemplate `json:"templates"`
	BlankIssuesEnabled bool            `json:"blank_issues_enabled"`
	ContactLinks       []ContactLink   `json:"contact_links"`
}

type ContactLink struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	About string `json:"about"`
}

type githubContentItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	DownloadURL string `json:"download_url"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
}

type issueTemplatesConfig struct {
	BlankIssuesEnabled *bool         `yaml:"blank_issues_enabled"`
	ContactLinks       []ContactLink `yaml:"contact_links"`
}

func githubContentsURL(owner, repo, p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, strings.Join(parts, "/"))
}

func githubGet(owner, repo, p, token string) (*http.Response, error) {
	apiURL := githubContentsURL(owner, repo, p)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")
	resp, err := githubDo(req, token)
	if err != nil {
		return nil, err
	}
	if token != "" && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		resp.Body.Close()
		req, err = http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "gitGost")
		resp, err = githubDo(req, "")
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func githubGetContentItem(owner, repo, p, token string) (*githubContentItem, error) {
	resp, err := githubGet(owner, repo, p, token)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch %s: %s", p, resp.Status)
	}
	var item githubContentItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

func githubGetContentList(owner, repo, p, token string) ([]githubContentItem, error) {
	resp, err := githubGet(owner, repo, p, token)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list %s: %s", p, resp.Status)
	}
	var items []githubContentItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func githubDecodeContent(item *githubContentItem) string {
	if item == nil {
		return ""
	}
	if item.Encoding == "base64" {
		cleaned := strings.ReplaceAll(item.Content, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(cleaned)
		if err == nil {
			return string(decoded)
		}
	}
	return item.Content
}

func issueTemplateName(filename string) string {
	name := strings.TrimSuffix(filename, ".md")
	name = strings.TrimSuffix(name, ".markdown")
	name = strings.TrimSuffix(name, ".yml")
	name = strings.TrimSuffix(name, ".yaml")
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	return strings.TrimSpace(name)
}

func parseMarkdownIssueTemplate(item githubContentItem, raw string) IssueTemplate {
	tmpl := IssueTemplate{
		Filename: item.Name,
		Type:     "markdown",
		Name:     issueTemplateName(item.Name),
		Content:  strings.TrimSpace(raw),
	}

	content := strings.TrimSpace(raw)
	if strings.HasPrefix(content, "---\n") {
		if end := strings.Index(content[4:], "\n---\n"); end >= 0 {
			front := content[4 : 4+end]
			body := strings.TrimLeft(content[4+end+5:], "\r\n")
			var meta struct {
				Name        string   `yaml:"name"`
				Description string   `yaml:"description"`
				About       string   `yaml:"about"`
				Title       string   `yaml:"title"`
				Labels      []string `yaml:"labels"`
				Assignees   []string `yaml:"assignees"`
			}
			if err := yaml.Unmarshal([]byte(front), &meta); err == nil {
				if meta.Name != "" {
					tmpl.Name = meta.Name
				}
				tmpl.Description = meta.Description
				tmpl.About = meta.About
				tmpl.Title = meta.Title
				tmpl.Labels = meta.Labels
				tmpl.Assignees = meta.Assignees
			}
			tmpl.Content = body
		}
	}
	if tmpl.Description == "" {
		tmpl.Description = tmpl.About
	}
	return tmpl
}

func parseIssueFormTemplate(item githubContentItem, raw string) IssueTemplate {
	tmpl := IssueTemplate{}
	if err := yaml.Unmarshal([]byte(raw), &tmpl); err != nil {
		tmpl.Name = issueTemplateName(item.Name)
	}
	tmpl.Filename = item.Name
	tmpl.Type = "form"
	if tmpl.Name == "" {
		tmpl.Name = issueTemplateName(item.Name)
	}
	if tmpl.About == "" {
		tmpl.About = tmpl.Description
	}
	return tmpl
}

func parseIssueTemplatesConfig(raw string) (*issueTemplatesConfig, error) {
	var cfg issueTemplatesConfig
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetIssueTemplates fetches the issue templates for a repository
func GetIssueTemplates(owner, repo string) (*IssueTemplatesResponse, error) {
	token := tokenpool.NextGitHubToken()
	items, err := githubGetContentList(owner, repo, ".github/ISSUE_TEMPLATE", token)
	if err != nil {
		return nil, err
	}

	result := &IssueTemplatesResponse{
		Templates:          []IssueTemplate{},
		BlankIssuesEnabled: true,
		ContactLinks:       []ContactLink{},
	}

	for _, item := range items {
		if item.Type != "file" {
			continue
		}
		name := strings.ToLower(item.Name)
		if name == "config.yml" || name == "config.yaml" {
			cfgItem, err := githubGetContentItem(owner, repo, item.Path, token)
			if err != nil || cfgItem == nil {
				continue
			}
			cfg, err := parseIssueTemplatesConfig(githubDecodeContent(cfgItem))
			if err != nil {
				continue
			}
			if cfg.BlankIssuesEnabled != nil {
				result.BlankIssuesEnabled = *cfg.BlankIssuesEnabled
			}
			if len(cfg.ContactLinks) > 0 {
				result.ContactLinks = cfg.ContactLinks
			}
			continue
		}

		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".markdown") && !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}

		contentItem, err := githubGetContentItem(owner, repo, item.Path, token)
		if err != nil || contentItem == nil {
			continue
		}
		raw := githubDecodeContent(contentItem)
		if raw == "" {
			continue
		}

		if strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".markdown") {
			result.Templates = append(result.Templates, parseMarkdownIssueTemplate(item, raw))
			continue
		}
		result.Templates = append(result.Templates, parseIssueFormTemplate(item, raw))
	}

	return result, nil
}

// RenderIssueFormBody renders the issue form fields into a markdown body
func RenderIssueFormBody(template *IssueTemplate, values map[string]string) string {
	var body strings.Builder

	if template.About != "" {
		body.WriteString(template.About)
		body.WriteString("\n\n")
	}

	for _, field := range template.Body {
		value := values[field.ID]
		if value == "" && field.Attributes.Value != "" {
			value = field.Attributes.Value
		}

		if field.Type == "markdown" {
			if field.Attributes.Value != "" {
				body.WriteString(field.Attributes.Value)
				body.WriteString("\n\n")
			}
			continue
		}

		label := field.Attributes.Label
		if label == "" {
			label = field.ID
		}

		body.WriteString(fmt.Sprintf("### %s\n\n", label))

		if value != "" {
			body.WriteString(value)
		} else if field.Attributes.Placeholder != "" {
			body.WriteString(fmt.Sprintf("*%s*", field.Attributes.Placeholder))
		} else {
			body.WriteString("_(no input provided)_")
		}
		body.WriteString("\n\n")
	}

	return body.String()
}

// RenderMarkdownTemplateBody renders a markdown template with user values
func RenderMarkdownTemplateBody(template *IssueTemplate, values map[string]string) string {
	content := template.Content
	for key, value := range values {
		placeholder := fmt.Sprintf("{{%s}}", key)
		content = strings.ReplaceAll(content, placeholder, value)
	}
	return content
}
