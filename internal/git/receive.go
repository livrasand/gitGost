package git

import (
	"bytes"
	"fmt"
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

	// Para debug: mostrar los primeros bytes
	fmt.Printf("DEBUG: Body length: %d bytes\n", len(body))
	if len(body) > 0 {
		fmt.Printf("DEBUG: First 100 bytes: %x\n", body[:minInt(100, len(body))])
	}

	// Buscar directamente el packfile (empieza con "PACK")
	packfileStart := findPackfileStart(body)
	if packfileStart == -1 {
		return fmt.Errorf("no packfile found in body")
	}

	packfile := body[packfileStart:]
	fmt.Printf("DEBUG: Found packfile at position %d, size %d bytes\n", packfileStart, len(packfile))

	// Use git unpack-objects to unpack the packfile
	cmd := exec.Command("git", "unpack-objects", "-v")
	cmd.Dir = tempDir
	cmd.Stdin = bytes.NewReader(packfile)
	
	output, err := cmd.CombinedOutput()
	fmt.Printf("DEBUG: git unpack-objects output: %s\n", string(output))
	
	return err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// findPackfileStart busca el inicio del packfile (magic number "PACK")
func findPackfileStart(body []byte) int {
	packMagic := []byte("PACK")
	for i := 0; i < len(body)-3; i++ {
		if bytes.Equal(body[i:i+4], packMagic) {
			return i
		}
	}
	return -1
}
