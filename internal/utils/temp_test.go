package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateTempDir(t *testing.T) {
	dir, err := CreateTempDir()
	if err != nil {
		t.Fatalf("CreateTempDir() failed: %v", err)
	}

	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("Created directory does not exist: %s", dir)
	}

	// Check if directory name contains "gitgost-"
	if !strings.Contains(dir, "gitgost-") {
		t.Errorf("Directory name should contain 'gitgost-', got: %s", dir)
	}

	// Clean up
	CleanupTempDir(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Directory should be cleaned up: %s", dir)
	}
}

func TestCleanupTempDir(t *testing.T) {
	dir, err := CreateTempDir()
	if err != nil {
		t.Fatalf("CreateTempDir() failed: %v", err)
	}

	// Create a file in the directory
	testFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Errorf("Test file should exist: %s", testFile)
	}

	// Clean up directory
	CleanupTempDir(dir)

	// Verify directory and file are gone
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Directory should be cleaned up: %s", dir)
	}
}

func TestCleanupOldTempDirs(t *testing.T) {
	// Create a temp directory with old timestamp
	tempDir := os.TempDir()
	oldDir := filepath.Join(tempDir, "gitgost-old-test")

	if err := os.MkdirAll(oldDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Set old modification time (2 hours ago)
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatalf("Failed to set old timestamp: %v", err)
	}

	// Create a recent directory (should not be cleaned)
	recentDir := filepath.Join(tempDir, "gitgost-recent-test")
	if err := os.MkdirAll(recentDir, 0755); err != nil {
		t.Fatalf("Failed to create recent test directory: %v", err)
	}

	// Run cleanup
	CleanupOldTempDirs()

	// Check that old directory was cleaned
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("Old directory should be cleaned up: %s", oldDir)
		os.RemoveAll(oldDir) // cleanup in case test fails
	}

	// Check that recent directory still exists
	if _, err := os.Stat(recentDir); os.IsNotExist(err) {
		t.Errorf("Recent directory should still exist: %s", recentDir)
	} else {
		os.RemoveAll(recentDir) // cleanup
	}
}
