package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

type Ref struct {
	Ref    string `json:"ref"`
	Object struct {
		Sha string `json:"sha"`
	} `json:"object"`
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

func CreatePR(owner, repo, branch, forkOwner, commitMessage string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo)

	prBody := fmt.Sprintf("%s\n\n---\n\n*This is an anonymous contribution made via [gitGost](https://github.com/livrasand/gitGost).*\n\n*The original author's identity has been anonymized to protect their privacy.*", commitMessage)

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
