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

	// Parse Git protocol to extract packfile
	// The body contains commands followed by packfile
	// Look for the packfile signature (PK\x02\x03)
	packStart := bytes.Index(body, []byte("PK\x02"))
	if packStart == -1 {
		// No packfile found, this might be just commands
		return nil
	}

	packfile := body[packStart:]

	// Use git unpack-objects to unpack the packfile
	cmd := exec.Command("git", "unpack-objects")
	cmd.Dir = tempDir
	cmd.Stdin = bytes.NewReader(packfile)
	return cmd.Run()
}
