package utils

import (
	"os"
	"path/filepath"
	"time"
)

func CreateTempDir() (string, error) {
	dir, err := os.MkdirTemp("", "gitgost-*")
	return dir, err
}

func CleanupTempDir(dir string) {
	os.RemoveAll(dir)
}

// CleanupOldTempDirs removes temp dirs older than 1 hour
func CleanupOldTempDirs() {
	tempDir := os.TempDir()
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		Log("Error reading temp dir: %v", err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) > 8 && entry.Name()[:8] == "gitgost-" {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) > time.Hour {
				os.RemoveAll(filepath.Join(tempDir, entry.Name()))
				Log("Cleaned up old temp dir: %s", entry.Name())
			}
		}
	}
}
