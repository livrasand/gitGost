package git

import (
	"fmt"
	"os"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func PushToGitHub(owner, repo, tempDir string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	// Debug: log token (first 10 chars)
	fmt.Printf("DEBUG: Using GitHub token: %s...\n", token[:min(10, len(token))])

	// Generate unique branch name (valid Git ref name)
	timestamp := time.Now().Unix()
	branch := fmt.Sprintf("test-%d", timestamp)

	r, err := git.PlainOpen(tempDir)
	if err != nil {
		return "", err
	}

	// Create remote
	remoteURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	fmt.Printf("DEBUG: Remote URL: %s\n", remoteURL)
	fmt.Printf("DEBUG: Branch name: %s\n", branch)
	_, err = r.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	})
	if err != nil {
		fmt.Printf("DEBUG: CreateRemote error: %v\n", err)
		return "", err
	}

	// Push to new branch with authentication
	refSpec := config.RefSpec("HEAD:" + branch)
	fmt.Printf("DEBUG: RefSpec: %s\n", refSpec)
	err = r.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{refSpec},
		Auth: &http.BasicAuth{
			Username: token, // GitHub tokens can be used as username
			Password: "",    // Password is empty for token auth
		},
	})
	if err != nil {
		fmt.Printf("DEBUG: Push error: %v\n", err)
		return "", err
	}

	return branch, nil
}
