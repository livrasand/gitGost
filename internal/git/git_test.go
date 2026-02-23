package git

import (
	"os"
	"testing"
)

func TestPushToGitHub_NoToken(t *testing.T) {
	// Save original env
	originalToken := os.Getenv("GITHUB_TOKEN")
	defer os.Setenv("GITHUB_TOKEN", originalToken)

	// Remove token
	os.Unsetenv("GITHUB_TOKEN")

	_, err := PushToGitHub("owner", "repo", "/tmp/nonexistent", "forkowner", "")
	if err == nil {
		t.Error("Expected error when GITHUB_TOKEN is not set")
	}
	if err.Error() != "GITHUB_TOKEN not set" {
		t.Errorf("Expected 'GITHUB_TOKEN not set', got '%s'", err.Error())
	}
}

func TestReceivePack(t *testing.T) {
	tempDir := t.TempDir()

	// Save original env
	originalToken := os.Getenv("GITHUB_TOKEN")
	defer func() {
		if originalToken != "" {
			os.Setenv("GITHUB_TOKEN", originalToken)
		}
	}()

	// Test without token - should fail
	os.Unsetenv("GITHUB_TOKEN")
	_, _, _, err := ReceivePack(tempDir, []byte{}, "owner", "repo")
	if err == nil {
		t.Error("Expected error when GITHUB_TOKEN is not set")
	}
	if err.Error() != "GITHUB_TOKEN not set" {
		t.Errorf("Expected 'GITHUB_TOKEN not set', got '%s'", err.Error())
	}
}

func TestSquashCommits_NoRepo(t *testing.T) {
	_, err := SquashCommits("/tmp/nonexistent")
	if err == nil {
		t.Error("Expected error when directory doesn't exist")
	}
}

func TestRewriteCommits(t *testing.T) {
	// This is currently a stub, so it should not error
	err := RewriteCommits("/tmp/nonexistent")
	if err != nil {
		t.Errorf("RewriteCommits should not error (it's a stub): %v", err)
	}
}
