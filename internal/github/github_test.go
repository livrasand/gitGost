package github

import (
	"os"
	"testing"
)

func TestCreatePR_NoToken(t *testing.T) {
	originalToken := os.Getenv("GITHUB_TOKEN")
	defer os.Setenv("GITHUB_TOKEN", originalToken)

	os.Unsetenv("GITHUB_TOKEN")

	_, err := CreatePR("owner", "repo", "branch", "forkowner", "test commit message")
	if err == nil {
		t.Error("Expected error when GITHUB_TOKEN is not set")
	}
	if err.Error() != "GITHUB_TOKEN not set" {
		t.Errorf("Expected 'GITHUB_TOKEN not set', got '%s'", err.Error())
	}
}
