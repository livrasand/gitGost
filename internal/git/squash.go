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

	// Get all references
	refs, err := r.References()
	if err != nil {
		return "", err
	}

	var latestCommit *object.Commit
	var treeHash plumbing.Hash

	// Find the latest commit from any branch
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsBranch() {
			commit, err := r.CommitObject(ref.Hash())
			if err != nil {
				return nil // Skip invalid refs
			}
			if latestCommit == nil || commit.Committer.When.After(latestCommit.Committer.When) {
				latestCommit = commit
				treeHash = commit.TreeHash
			}
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	// If no commits found, create initial commit with empty tree
	if latestCommit == nil {
		// Create empty tree
		tree := &object.Tree{}
		obj := r.Storer.NewEncodedObject()
		err = tree.Encode(obj)
		if err != nil {
			return "", err
		}
		treeHash, err = r.Storer.SetEncodedObject(obj)
		if err != nil {
			return "", err
		}
	}

	// Create new anonymous commit
	newCommit := &object.Commit{
		Author: object.Signature{
			Name:  "@gitgost-anonymous",
			Email: "anon@gitgost",
			When:  time.Now(),
		},
		Committer: object.Signature{
			Name:  "@gitgost-anonymous",
			Email: "anon@gitgost",
			When:  time.Now(),
		},
		Message:  "Anonymous contribution via gitGost",
		TreeHash: treeHash,
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
