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
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Timeout mayor para evitar expiraciones en búsquedas lentas de GitHub.
var httpClient = &http.Client{Timeout: 60 * time.Second}

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

// UpdateCommentsKarmaByHash actualiza el karma en los comentarios que contienen el hash, preservando el cuerpo.
func UpdateCommentsKarmaByHash(hash string, karma int) error {
	token := os.Getenv("GITHUB_TOKEN")
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
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "gitGost")

		resp, err = httpClient.Do(req)
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

	re := regexp.MustCompile(fmt.Sprintf(`(?m)goster-%s · karma \(\d+\) · \[report\]\(([^)]+)\)`, regexp.QuoteMeta(hash)))

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
		creq.Header.Set("Authorization", "token "+token)
		creq.Header.Set("Accept", "application/vnd.github+json")
		creq.Header.Set("User-Agent", "gitGost")

		var cresp *http.Response
		delay := time.Second
		for attempt := 0; attempt < 3; attempt++ {
			cresp, err = httpClient.Do(creq)
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
			legend := fmt.Sprintf("goster-%s · karma (%d) · [report](%s)", hash, karma, link)
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
			preq.Header.Set("Authorization", "token "+token)
			preq.Header.Set("Content-Type", "application/json")

			presp, err := httpClient.Do(preq)
			if err != nil {
				continue
			}
			presp.Body.Close()
		}
	}

	return nil
}

// DeleteCommentsByHash busca y elimina comentarios que contengan el hash proporcionado
func DeleteCommentsByHash(hash string) error {
	token := os.Getenv("GITHUB_TOKEN")
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
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "gitGost")

		resp, err = httpClient.Do(req)
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
		creq.Header.Set("Authorization", "token "+token)
		creq.Header.Set("Accept", "application/vnd.github+json")
		creq.Header.Set("User-Agent", "gitGost")
		cresp, err := httpClient.Do(creq)
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
			preq.Header.Set("Authorization", "token "+token)
			preq.Header.Set("Accept", "application/vnd.github+json")
			preq.Header.Set("User-Agent", "gitGost")

			presp, err := httpClient.Do(preq)
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

// CreateAnonymousIssue crea una issue usando el bot autenticado
func CreateAnonymousIssue(owner, repo, title, body string, labels []string) (string, int, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", 0, fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues", owner, repo)

	issueBody := fmt.Sprintf("%s\n\n---\n\n*This is an anonymous contribution made via [gitGost](https://gitgost.fly.dev).*\n\n*The original author's identity has been anonymized to protect their privacy.*", body)

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

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
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

// CreateAnonymousComment publica un comentario en la issue
func CreateAnonymousComment(owner, repo string, number int, body string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
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

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
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

// CreateAnonymousPRComment publica un comentario general en un Pull Request
func CreateAnonymousPRComment(owner, repo string, number int, body string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	// PR comments use the same issues comments endpoint
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

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := httpClient.Do(req)
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

// GetSha returns the SHA of the ref
func (r *Ref) GetSha() string {
	return r.Object.Sha
}

// ForkRepo creates a fork of the repository for the authenticated user
func ForkRepo(owner, repo string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	// Check if fork already exists
	userURL := "https://api.github.com/user"
	req, err := http.NewRequest("GET", userURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+token)

	resp, err := http.DefaultClient.Do(req)
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

	// Check if fork already exists
	forkURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", forkOwner, repo)
	req, err = http.NewRequest("GET", forkURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+token)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	if resp.StatusCode == 200 {
		// Fork already exists
		fmt.Printf("DEBUG: Fork already exists: %s/%s\n", forkOwner, repo)
		return forkOwner, nil
	}

	// Create fork
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/forks", owner, repo)
	req, err = http.NewRequest("POST", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
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

// ClosePRByURL closes an open PR given its GitHub html_url.
// The URL format is: https://github.com/{owner}/{repo}/pull/{number}
func ClosePRByURL(prURL string) error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN not set")
	}

	// Parse owner, repo, number from the PR URL
	// Expected: https://github.com/<owner>/<repo>/pull/<number>
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
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := httpClient.Do(req)
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
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo)

	prBody := fmt.Sprintf("%s\n\n---\n\n*This is an anonymous contribution made via [gitGost](https://gitgost.fly.dev).*\n\n*The original author's identity has been anonymized to protect their privacy.*", commitMessage)

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

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
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
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/refs", owner, repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "token "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 409 {
		// Repository is empty, return empty refs
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

// RepoPolicy contiene las directivas de configuración leídas desde .gitgost.yml del repositorio destino.
type RepoPolicy struct {
	DenyAll bool `yaml:"DENY_ALL"`
}

// GetRepoPolicy descarga .gitgost.yml desde el branch por defecto del repositorio y retorna la política.
// Si el archivo no existe o no puede leerse, retorna una política permisiva (sin restricciones).
func GetRepoPolicy(owner, repo string) (*RepoPolicy, error) {
	token := os.Getenv("GITHUB_TOKEN")
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

	resp, err := httpClient.Do(req)
	if err != nil {
		return &RepoPolicy{}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Archivo no existe: política permisiva por defecto
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
		// GitHub devuelve el contenido en base64 con saltos de línea
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

// IsRepoVerified checks if the repository has a .gitgost.yml file indicating support for anonymous contributions
func IsRepoVerified(owner, repo string) bool {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/.gitgost.yml", owner, repo)
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// GeneratePRHash genera un hash deterministico de 8 caracteres basado en owner/repo/branch.
// Esto permite que el mismo branch siempre produzca el mismo pr-hash, sin almacenar estado.
func GeneratePRHash(owner, repo, branch string) string {
	input := fmt.Sprintf("%s/%s/%s", owner, repo, branch)
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:8]
}

// PRTimelineEvent representa un evento individual del timeline de un PR.
// Solo contiene los campos que nos interesan.
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

// ExtractPRNumber extrae el numero de PR de una URL de GitHub.
// Formato esperado: https://github.com/{owner}/{repo}/pull/{number}
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

// FetchPRTimeline obtiene el timeline de un PR desde la API de GitHub.
// Usa ETag/If-None-Match para evitar datos innecesarios.
// Retorna los eventos, el nuevo ETag, si hubo cambios, o error.
func FetchPRTimeline(owner, repo string, number int, etag string) (events []PRTimelineEvent, newETag string, changed bool, err error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, "", false, fmt.Errorf("GITHUB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/timeline?per_page=100", owner, repo, number)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()

	newETag = resp.Header.Get("ETag")

	if resp.StatusCode == http.StatusNotModified {
		return nil, newETag, false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, "", false, err
	}

	// GitHub puede devolver null en lugar de array
	if events == nil {
		events = []PRTimelineEvent{}
	}

	return events, newETag, true, nil
}

// FetchPRInfo obtiene informacion basica del PR (state, title, comments count).
// Usa el endpoint de issues ya que los PRs son issues.
func FetchPRInfo(owner, repo string, number int) (state, title string, comments int, updatedAt string, err error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", "", 0, "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d", owner, repo, number)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", "", 0, "", err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", 0, "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var result struct {
		State     string `json:"state"`
		Title     string `json:"title"`
		Comments  int    `json:"comments"`
		UpdatedAt string `json:"updated_at"`
		// El campo pull_request existe solo si es un PR
		PullRequest *struct{} `json:"pull_request,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", 0, "", err
	}

	return result.State, result.Title, result.Comments, result.UpdatedAt, nil
}

// GetExistingPR busca si existe un PR abierto desde forkOwner:branchName hacia owner/repo.
// Retorna la URL del PR, si la rama existe en el fork, y cualquier error.
func GetExistingPR(owner, repo, forkOwner, branchName string) (string, bool, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", false, fmt.Errorf("GITHUB_TOKEN not set")
	}

	// Verificar si la rama existe en el fork
	branchURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/branches/%s", forkOwner, repo, branchName)
	req, err := http.NewRequest("GET", branchURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false, err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// La rama no existe en el fork
		return "", false, nil
	}

	// La rama existe; buscar el PR abierto asociado
	head := fmt.Sprintf("%s:%s", forkOwner, branchName)
	prListURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=open&head=%s&per_page=1",
		owner, repo, url.QueryEscape(head))

	req, err = http.NewRequest("GET", prListURL, nil)
	if err != nil {
		return "", true, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err = httpClient.Do(req)
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
		// Rama existe pero el PR fue cerrado/mergeado; retornar rama existente sin URL de PR
		return "", true, nil
	}

	return prs[0].HTMLURL, true, nil
}

// PRTimelineEvent representa un evento individual del timeline de un PR.
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

// ExtractPRNumber extrae el numero de PR de una URL de GitHub.
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

// FetchPRTimeline obtiene el timeline de un PR desde la API de GitHub con ETag.
func FetchPRTimeline(owner, repo string, number int, etag string) (events []PRTimelineEvent, newETag string, changed bool, err error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, "", false, fmt.Errorf("GITHUB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/timeline?per_page=100", owner, repo, number)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()

	newETag = resp.Header.Get("ETag")

	if resp.StatusCode == http.StatusNotModified {
		return nil, newETag, false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, "", false, err
	}

	if events == nil {
		events = []PRTimelineEvent{}
	}

	return events, newETag, true, nil
}

// FetchPRInfo obtiene informacion basica del PR (state, title, comments).
func FetchPRInfo(owner, repo string, number int) (state, title string, comments int, updatedAt string, err error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", "", 0, "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d", owner, repo, number)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", "", 0, "", err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gitGost")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", 0, "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var result struct {
		State       string    `json:"state"`
		Title       string    `json:"title"`
		Comments    int       `json:"comments"`
		UpdatedAt   string    `json:"updated_at"`
		PullRequest *struct{} `json:"pull_request,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", 0, "", err
	}

	return result.State, result.Title, result.Comments, result.UpdatedAt, nil
}
