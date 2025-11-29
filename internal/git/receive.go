package git

import (
	"bytes"
	"os/exec"

	"github.com/go-git/go-git/v5"
)

func ReceivePack(tempDir string, body []byte) error {
	// Initialize repo
	_, err := git.PlainInit(tempDir, false)
	if err != nil {
		return err
	}

	// If body is empty, skip unpacking
	if len(body) == 0 {
		return nil
	}

	// Use git unpack-objects to unpack the packfile
	cmd := exec.Command("git", "unpack-objects")
	cmd.Dir = tempDir
	cmd.Stdin = bytes.NewReader(body)
	return cmd.Run()
}
