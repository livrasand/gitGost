package git

import (
	"context"
	"os"
	"strings"
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

func TestSafeCloneURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{
			name: "github lowercases host",
			raw:  "https://GITHUB.com/owner/repo",
			want: "https://github.com/owner/repo",
		},
		{
			name:    "missing scheme",
			raw:     "github.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "host not allowed",
			raw:     "https://evil.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "too many path segments",
			raw:     "https://github.com/owner/repo/extra",
			wantErr: true,
		},
		{
			name:    "path traversal in owner",
			raw:     "https://github.com/../repo",
			wantErr: true,
		},
		{
			name:    "path traversal in repo",
			raw:     "https://github.com/owner/..",
			wantErr: true,
		},
		{
			name:    "injected option as url",
			raw:     "https://github.com/owner/repo?--upload-pack=evil",
			wantErr: true,
		},
		{
			name:    "userinfo rejected",
			raw:     "https://user:pass@github.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "fragment rejected",
			raw:     "https://github.com/owner/repo#fragment",
			wantErr: true,
		},
		{
			name: "port allowed",
			raw:  "https://github.com:443/owner/repo",
			want: "https://github.com/owner/repo",
		},
		{
			name: "trailing slash allowed",
			raw:  "https://github.com/owner/repo/",
			want: "https://github.com/owner/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeCloneURL(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("safeCloneURL(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("safeCloneURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestGitClone_InvalidURLRejected(t *testing.T) {
	ctx := context.Background()
	err := gitClone(ctx, "--upload-pack=evil", t.TempDir())
	if err == nil {
		t.Fatal("gitClone should reject an option-injection URL")
	}
	if !strings.Contains(err.Error(), "URL de repositorio inválida") {
		t.Fatalf("expected URL validation error, got: %v", err)
	}
}
