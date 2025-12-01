package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"

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

	// Parse Git Smart HTTP receive-pack protocol
	// Body contains pkt-lines with commands, then flush (0000), then packfile
	packfile, err := parseReceivePackBody(body)
	if err != nil {
		return fmt.Errorf("failed to parse receive-pack body: %w", err)
	}

	if len(packfile) == 0 {
		// No packfile, just commands (e.g., delete refs)
		return nil
	}

	// Use git unpack-objects to unpack the packfile
	cmd := exec.Command("git", "unpack-objects")
	cmd.Dir = tempDir
	cmd.Stdin = bytes.NewReader(packfile)
	return cmd.Run()
}

// parseReceivePackBody parses the Git Smart HTTP receive-pack request body
// Returns the packfile data after the flush packet
func parseReceivePackBody(body []byte) ([]byte, error) {
	i := 0
	for i < len(body) {
		if i+4 > len(body) {
			return nil, fmt.Errorf("incomplete pkt-line at position %d", i)
		}

		// Read 4 hex digits for length
		lengthStr := string(body[i : i+4])
		i += 4

		length, err := strconv.ParseInt(lengthStr, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid pkt-line length '%s' at position %d", lengthStr, i-4)
		}

		if length == 0 {
			// Flush packet, packfile follows
			if i >= len(body) {
				return []byte{}, nil // No packfile
			}
			remaining := body[i:]
			// Verify packfile starts with PACK
			if len(remaining) >= 4 && string(remaining[:4]) == "PACK" {
				return remaining, nil
			}
			return nil, fmt.Errorf("expected PACK after flush, got %q", remaining[:min(4, len(remaining))])
		}

		// Skip the pkt-line data
		if int(length) > len(body)-i {
			return nil, fmt.Errorf("pkt-line length %d exceeds remaining data", length)
		}
		i += int(length)
	}

	return nil, fmt.Errorf("no flush packet found in receive-pack body")
}
