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

func CreatePR(owner, repo, branch string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo)
	data := map[string]interface{}{
		"title": "Contribution",
		"head":  branch,
		"base":  "main",
		"body":  "Anonymous contribution.",
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
