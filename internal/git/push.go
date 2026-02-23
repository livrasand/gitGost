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

// PushToGitHub empuja los commits al fork. Si targetBranch está vacío, genera
// un nombre único con timestamp. Si targetBranch está dado, hace force-push a esa rama.
func PushToGitHub(owner, repo, tempDir, forkOwner, targetBranch string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set")
	}

	// Debug: log token (first 10 chars)
	fmt.Printf("DEBUG: Using GitHub token: %s...\n", token[:min(10, len(token))])

	// Usar el branch dado o generar uno único con timestamp
	branch := targetBranch
	if branch == "" {
		timestamp := time.Now().Unix()
		branch = fmt.Sprintf("gitgost-%d", timestamp)
	}

	r, err := git.PlainOpen(tempDir)
	if err != nil {
		return "", err
	}

	// Push to fork, not original repo
	forkURL := fmt.Sprintf("https://github.com/%s/%s.git", forkOwner, repo)
	fmt.Printf("DEBUG: Pushing to fork: %s\n", forkURL)
	fmt.Printf("DEBUG: Branch name: %s\n", branch)

	// Eliminar remote origin si existe y crear uno nuevo apuntando al fork
	_ = r.DeleteRemote("origin")

	_, err = r.CreateRemote(&config.RemoteConfig{
		Name: "fork",
		URLs: []string{forkURL},
	})
	if err != nil {
		fmt.Printf("DEBUG: CreateRemote error: %v\n", err)
		return "", err
	}
	fmt.Printf("DEBUG: Remote 'fork' is ready\n")

	// Usar force-push (+) cuando se actualiza una rama existente
	refSpecStr := fmt.Sprintf("HEAD:refs/heads/%s", branch)
	if targetBranch != "" {
		refSpecStr = "+" + refSpecStr
	}
	refSpec := config.RefSpec(refSpecStr)
	fmt.Printf("DEBUG: RefSpec: %s\n", refSpec)
	err = r.Push(&git.PushOptions{
		RemoteName: "fork",
		RefSpecs:   []config.RefSpec{refSpec},
		Auth: &http.BasicAuth{
			Username: "x-access-token", // GitHub requires this for token auth
			Password: token,            // Token goes in password field
		},
		Force: targetBranch != "",
	})
	if err != nil {
		fmt.Printf("DEBUG: Push error: %v\n", err)
		return "", err
	}

	return branch, nil
}
