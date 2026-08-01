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

	refs, err := r.References()
	if err != nil {
		return "", err
	}

	var latestCommit *object.Commit
	var treeHash plumbing.Hash

	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsBranch() {
			commit, err := r.CommitObject(ref.Hash())
			if err != nil {
				return nil
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

	if latestCommit == nil {
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

	err = r.Storer.SetReference(plumbing.NewHashReference(plumbing.HEAD, hash))
	if err != nil {
		return "", err
	}

	return hash.String(), nil
}
