package github

import (
	"os"
	"testing"
)

func TestCreatePR_NoToken(t *testing.T) {
	// Save original env
	originalToken := os.Getenv("GITHUB_TOKEN")
	defer os.Setenv("GITHUB_TOKEN", originalToken)

	// Remove token
	os.Unsetenv("GITHUB_TOKEN")

	_, err := CreatePR("owner", "repo", "branch", "forkowner")
	if err == nil {
		t.Error("Expected error when GITHUB_TOKEN is not set")
	}
	if err.Error() != "GITHUB_TOKEN not set" {
		t.Errorf("Expected 'GITHUB_TOKEN not set', got '%s'", err.Error())
	}
}

// Note: Testing CreatePR with actual GitHub API would require:
// 1. A valid GitHub token
// 2. A real repository
// 3. A real branch
// 4. Network access
// This would be an integration test, not a unit test.
// For now, we test the error case when token is missing.
