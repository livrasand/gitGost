package git

import (
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func SquashCommits(tempDir string) (string, error) {
	r, err := git.PlainOpen(tempDir)
	if err != nil {
		return "", err
	}

	ref, err := r.Head()
	if err != nil {
		return "", err
	}

	commit, err := r.CommitObject(ref.Hash())
	if err != nil {
		return "", err
	}

	// Create new anonymous commit
	newCommit := &object.Commit{
		Author: object.Signature{
			Name:  "gitGost Anonymous",
			Email: "anon@gitgost",
			When:  time.Now(),
		},
		Committer: object.Signature{
			Name:  "gitGost Anonymous",
			Email: "anon@gitgost",
			When:  time.Now(),
		},
		Message:  "Anonymous contribution via gitGost",
		TreeHash: commit.TreeHash,
	}

	obj := r.Storer.NewEncodedObject()
	err = newCommit.Encode(obj)
	if err != nil {
		return "", err
	}

	hash, err := r.Storer.SetEncodedObject(obj)
	if err != nil {
		return "", err
	}

	// Update HEAD
	err = r.Storer.SetReference(plumbing.NewHashReference(plumbing.HEAD, hash))
	if err != nil {
		return "", err
	}

	return hash.String(), nil
}
