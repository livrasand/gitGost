package http

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/livrasand/gitGost/internal/git"
	"github.com/livrasand/gitGost/internal/github"
	"github.com/livrasand/gitGost/internal/utils"

	"github.com/gin-gonic/gin"
)

func ReceivePackDiscoveryHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	// Get refs from GitHub
	refs, err := github.GetRefs(owner, repo)
	if err != nil {
		utils.Log("Error getting refs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get refs"})
		return
	}

	// Build advertisement
	var advertisement bytes.Buffer

	// Service line with proper pkt-line format
	serviceLine := "# service=git-receive-pack\n"
	advertisement.WriteString(fmt.Sprintf("%04x", len(serviceLine)+4))
	advertisement.WriteString(serviceLine)
	advertisement.WriteString("0000") // flush packet

	// Refs
	capabilities := "report-status delete-refs ofs-delta side-band-64k"
	first := true
	for _, ref := range refs {
		if strings.HasPrefix(ref.Ref, "refs/heads/") || strings.HasPrefix(ref.Ref, "refs/tags/") {
			line := ref.GetSha() + " " + ref.Ref
			if first {
				line += "\x00" + capabilities
				first = false
			}
			line += "\n"
			length := len(line) + 4 // +4 for length prefix
			advertisement.WriteString(fmt.Sprintf("%04x", length))
			advertisement.WriteString(line)
		}
	}

	// Final flush packet
	advertisement.WriteString("0000")

	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write(advertisement.Bytes())
}

func ReceivePackHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	// Handle 100 Continue for large requests
	if c.GetHeader("Expect") == "100-continue" {
		c.Writer.WriteHeader(http.StatusContinue)
	}

	// Read body
	utils.Log("Content-Type: %s", c.GetHeader("Content-Type"))
	utils.Log("Content-Length: %s", c.GetHeader("Content-Length"))
	utils.Log("Transfer-Encoding: %s", c.GetHeader("Transfer-Encoding"))
	utils.Log("Expect: %s", c.GetHeader("Expect"))

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		utils.Log("Error reading body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	utils.Log("Received push for %s/%s, size: %d bytes", owner, repo, len(body))
	if len(body) > 0 {
		utils.Log("Body starts with: %x", body[:min(20, len(body))])
	}

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

	// Respond with Git protocol success
	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	c.Writer.WriteHeader(http.StatusOK)

	// Write pkt-lined response
	// unpack ok\n
	unpackLine := "unpack ok\n"
	c.Writer.WriteString(fmt.Sprintf("%04x%s", len(unpackLine), unpackLine))

	// ok refs/heads/main\n (assuming push to main)
	okLine := "ok refs/heads/main\n"
	c.Writer.WriteString(fmt.Sprintf("%04x%s", len(okLine), okLine))

	// flush
	c.Writer.WriteString("0000")
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
