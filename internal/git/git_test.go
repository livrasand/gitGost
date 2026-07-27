package git

import (
	"os"
	"testing"

	goGit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

func TestPushToGitHub_NoToken(t *testing.T) {
	originalToken := os.Getenv("GITHUB_TOKEN")
	defer os.Setenv("GITHUB_TOKEN", originalToken)

	os.Unsetenv("GITHUB_TOKEN")

	_, err := PushToGitHub("owner", "repo", "/tmp/nonexistent", "forkowner", "", "", "")
	if err == nil {
		t.Error("Expected error when GITHUB_TOKEN is not set")
	}
	if err.Error() != "GITHUB_TOKEN not set" {
		t.Errorf("Expected 'GITHUB_TOKEN not set', got '%s'", err.Error())
	}
}

func TestReceivePack(t *testing.T) {
	tempDir := t.TempDir()

	originalToken := os.Getenv("GITHUB_TOKEN")
	defer func() {
		if originalToken != "" {
			os.Setenv("GITHUB_TOKEN", originalToken)
		}
	}()

	os.Unsetenv("GITHUB_TOKEN")
	_, _, _, err := ReceivePack(tempDir, []byte{}, "owner", "repo", "", "")
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
	err := RewriteCommits("/tmp/nonexistent")
	if err != nil {
		t.Errorf("RewriteCommits should not error (it's a stub): %v", err)
	}
}

func TestAnonymizeCommits_UsesLocalHEADBranch(t *testing.T) {
	storer := memory.NewStorage()
	repo, err := goGit.Init(storer, nil)
	if err != nil {
		t.Fatal(err)
	}

	signature := object.Signature{Name: "author", Email: "author@example.com"}
	base := &object.Commit{Author: signature, Committer: signature, Message: "base"}
	baseObject := storer.NewEncodedObject()
	if err := base.Encode(baseObject); err != nil {
		t.Fatal(err)
	}
	baseHash, err := storer.SetEncodedObject(baseObject)
	if err != nil {
		t.Fatal(err)
	}

	child := &object.Commit{Author: signature, Committer: signature, Message: "child", ParentHashes: []plumbing.Hash{baseHash}}
	childObject := storer.NewEncodedObject()
	if err := child.Encode(childObject); err != nil {
		t.Fatal(err)
	}
	childHash, err := storer.SetEncodedObject(childObject)
	if err != nil {
		t.Fatal(err)
	}

	if err := storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/heads/develop"), baseHash)); err != nil {
		t.Fatal(err)
	}
	if err := storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.ReferenceName("refs/heads/develop"))); err != nil {
		t.Fatal(err)
	}

	newHash, err := AnonymizeCommits(repo, childHash.String())
	if err != nil {
		t.Fatal(err)
	}
	if newHash == childHash.String() {
		t.Fatal("expected child commit to be anonymized")
	}

	anonymized, err := repo.CommitObject(plumbing.NewHash(newHash))
	if err != nil {
		t.Fatal(err)
	}
	if len(anonymized.ParentHashes) != 1 || anonymized.ParentHashes[0] != baseHash {
		t.Fatalf("expected base commit to remain unchanged, got parents %v", anonymized.ParentHashes)
	}
}
