package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

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
