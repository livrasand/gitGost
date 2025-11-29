package git

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

func PushToGitHub(owner, repo, tempDir string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	// Generate unique branch name
	timestamp := time.Now().Unix()
	random := rand.Intn(1000)
	branch := fmt.Sprintf("gitgost/pr-%d-%d", timestamp, random)

	r, err := git.PlainOpen(tempDir)
	if err != nil {
		return "", err
	}

	// Create remote
	remoteURL := fmt.Sprintf("https://%s@github.com/%s/%s.git", token, owner, repo)
	_, err = r.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	})
	if err != nil {
		return "", err
	}

	// Push to new branch
	err = r.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{config.RefSpec("HEAD:" + branch)},
	})
	if err != nil {
		return "", err
	}

	return branch, nil
}
