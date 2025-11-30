package http

import (
	"gitGost/internal/git"
	"gitGost/internal/github"
	"gitGost/internal/utils"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

func ReceivePackDiscoveryHandler(c *gin.Context) {
	// Git receive-pack discovery: respond with capabilities
	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
	c.Writer.WriteHeader(http.StatusOK)
	data := []byte("001a# service=git-receive-pack\n0000")
	c.Writer.Write(data)
}

func ReceivePackHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	// Read body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		utils.Log("Error reading body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	utils.Log("Received push for %s/%s, size: %d bytes", owner, repo, len(body))

	// Create temp repo
	tempDir, err := utils.CreateTempDir()
	if err != nil {
		utils.Log("Error creating temp dir: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create temp dir"})
		return
	}
	defer utils.CleanupTempDir(tempDir)

	// Receive pack
	err = git.ReceivePack(tempDir, body)
	if err != nil {
		utils.Log("Error receiving pack: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to receive pack"})
		return
	}

	// Squash commits
	squashedCommit, err := git.SquashCommits(tempDir)
	if err != nil {
		utils.Log("Error squashing commits: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to squash commits"})
		return
	}

	utils.Log("Squashed to commit: %s", squashedCommit)

	// Push to GitHub
	branch, err := git.PushToGitHub(owner, repo, tempDir)
	if err != nil {
		utils.Log("Error pushing to GitHub: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to push to GitHub"})
		return
	}

	utils.Log("Pushed to branch: %s", branch)

	// Create PR
	prURL, err := github.CreatePR(owner, repo, branch)
	if err != nil {
		utils.Log("Error creating PR: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create PR"})
		return
	}

	utils.Log("Created PR: %s", prURL)

	// Respond
	c.JSON(http.StatusOK, gin.H{
		"pr_url": prURL,
		"branch": branch,
		"status": "ok",
	})
}

// HealthHandler provides basic health check
func HealthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// MetricsHandler provides basic system metrics
func MetricsHandler(c *gin.Context) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	c.JSON(http.StatusOK, gin.H{
		"memory": gin.H{
			"alloc":       m.Alloc,
			"total_alloc": m.TotalAlloc,
			"sys":         m.Sys,
			"num_gc":      m.NumGC,
		},
		"goroutines": runtime.NumGoroutine(),
		"uptime":     time.Since(startTime).String(),
	})
}

// startTime tracks when the server started
var startTime = time.Now()
