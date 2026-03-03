package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/livrasand/gitGost/internal/database"
	"github.com/livrasand/gitGost/internal/git"
	"github.com/livrasand/gitGost/internal/github"
	"github.com/livrasand/gitGost/internal/utils"

	"github.com/gin-gonic/gin"
)

var uploadPackClient = &http.Client{Timeout: 30 * time.Second}

const anonymousFriendlyBadgeSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="180" height="20" viewBox="0 0 180 20">
  <rect width="180" height="20" fill="#4CAF50" rx="3"/>
  <text x="90" y="14" fill="#ffffff" font-family="Arial, sans-serif" font-size="12" text-anchor="middle">Anonymous Contributor Friendly</text>
</svg>`

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// WritePktLine escribe una línea en formato pkt-line
func WritePktLine(w io.Writer, data string) error {
	if data == "" {
		_, err := w.Write([]byte("0000"))
		return err
	}

	length := len(data) + 4
	_, err := fmt.Fprintf(w, "%04x%s", length, data)
	return err
}

// WriteSidebandLine escribe una línea con prefijo de banda para side-band-64k
func WriteSidebandLine(w io.Writer, band byte, message string) error {
	if message == "" {
		return nil
	}
	// Agregar newline si no existe
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}

	// Formato: longitud(4 bytes hex) + banda(1 byte) + mensaje
	data := append([]byte{band}, []byte(message)...)
	length := len(data) + 4

	_, err := fmt.Fprintf(w, "%04x", length)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

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

	// Service line
	serviceLine := "# service=git-receive-pack\n"
	WritePktLine(&advertisement, serviceLine)
	WritePktLine(&advertisement, "") // flush

	// Capabilities
	capabilities := "report-status delete-refs side-band-64k quiet ofs-delta push-options"

	// Refs
	first := true
	for _, ref := range refs {
		if strings.HasPrefix(ref.Ref, "refs/heads/") || strings.HasPrefix(ref.Ref, "refs/tags/") {
			line := fmt.Sprintf("%s %s", ref.GetSha(), ref.Ref)
			if first {
				line += "\x00" + capabilities
				first = false
			}
			line += "\n"
			WritePktLine(&advertisement, line)
		}
	}

	// Si no hay refs, enviar capacidades de todos modos
	if first {
		line := fmt.Sprintf("0000000000000000000000000000000000000000 capabilities^{}\x00%s\n", capabilities)
		WritePktLine(&advertisement, line)
	}

	// Flush final
	WritePktLine(&advertisement, "")

	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write(advertisement.Bytes())
}

func ReceivePackHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	fmt.Printf("DEBUG: ReceivePackHandler called for %s/%s\n", owner, repo)

	// Handle 100 Continue
	if c.GetHeader("Expect") == "100-continue" {
		c.Writer.WriteHeader(http.StatusContinue)
	}

	// Check panic mode
	if isPanicMode() {
		c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		c.Writer.WriteHeader(http.StatusOK)
		var errResp bytes.Buffer
		WriteSidebandLine(&errResp, 2, "remote: ")
		WriteSidebandLine(&errResp, 2, "remote: SERVICE TEMPORARILY SUSPENDED")
		WriteSidebandLine(&errResp, 2, "remote: ")
		WriteSidebandLine(&errResp, 2, "remote: The panic button has been activated. The service has been")
		WriteSidebandLine(&errResp, 2, "remote: temporarily suspended due to detected bot activity")
		WriteSidebandLine(&errResp, 2, "remote: sending mass PRs. Please try again in 15 minutes.")
		WriteSidebandLine(&errResp, 2, "remote: ")
		WriteSidebandLine(&errResp, 3, "push rejected: service temporarily suspended")
		WritePktLine(&errResp, "")
		c.Writer.Write(errResp.Bytes())
		return
	}

	// Check rate limit per IP (5 PRs/IP/hour)
	ip := c.ClientIP()
	if checkRateLimit(ip) {
		c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		c.Writer.WriteHeader(http.StatusOK)
		var errResp bytes.Buffer
		WriteSidebandLine(&errResp, 2, "remote: ")
		WriteSidebandLine(&errResp, 2, fmt.Sprintf("remote: Rate limit exceeded: max %d PRs per hour per IP.", rateLimitMaxPRs))
		WriteSidebandLine(&errResp, 2, "remote: Please try again later.")
		WriteSidebandLine(&errResp, 2, "remote: ")
		WriteSidebandLine(&errResp, 3, "push rejected: rate limit exceeded")
		WritePktLine(&errResp, "")
		c.Writer.Write(errResp.Bytes())
		return
	}

	// Record push globally to detect botnet/script patterns across IPs
	go recordGlobalBurst(ip)

	// Track PR URL for potential rollback (registered after PR is created below)

	// Read full body
	utils.Log("Content-Type: %s", c.GetHeader("Content-Type"))
	utils.Log("Content-Length: %s", c.GetHeader("Content-Length"))

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		utils.Log("Error reading body: %v", err)
		sendErrorResponse(c, "error reading body")
		return
	}

	utils.Log("Received push for %s/%s, size: %d bytes", owner, repo, len(body))

	// Create temporary repository
	tempDir, err := utils.CreateTempDir()
	if err != nil {
		utils.Log("Error creating temp dir: %v", err)
		sendErrorResponse(c, fmt.Sprintf("error creating temp dir: %v", err))
		return
	}
	defer utils.CleanupTempDir(tempDir)

	// Set headers before writing anything
	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	c.Writer.WriteHeader(http.StatusOK)

	var response bytes.Buffer

	// Initial progress message
	WriteSidebandLine(&response, 2, "remote: gitGost: Processing your anonymous contribution...")

	// Process the packfile
	newSHA, commitMessage, receivedPRHash, err := git.ReceivePack(tempDir, body, owner, repo)
	if err != nil {
		utils.Log("Error receiving pack: %v", err)
		WriteSidebandLine(&response, 3, fmt.Sprintf("unpack error: %v", err))
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Commits received successfully, HEAD at: %s", newSHA)
	WriteSidebandLine(&response, 2, "remote: gitGost: Commits anonymized successfully")

	// Create a fork of the repository
	WriteSidebandLine(&response, 2, "remote: gitGost: Creating fork...")
	forkOwner, err := github.ForkRepo(owner, repo)
	if err != nil {
		utils.Log("Error creating fork: %v", err)
		WriteSidebandLine(&response, 1, "unpack ok\n")
		WriteSidebandLine(&response, 3, fmt.Sprintf("error creating fork: %v", err))
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Fork ready: %s/%s", forkOwner, repo)
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote: gitGost: Fork ready at %s/%s", forkOwner, repo))

	var branch, prURL string
	isUpdate := false

	if receivedPRHash != "" {
		// Update mode: the client sent an existing pr-hash
		branchFromHash := fmt.Sprintf("gitgost-%s", receivedPRHash)
		WriteSidebandLine(&response, 2, fmt.Sprintf("remote: gitGost: Updating existing PR (hash: %s)...", receivedPRHash))

		existingPRURL, branchExists, err := github.GetExistingPR(owner, repo, forkOwner, branchFromHash)
		if err != nil {
			utils.Log("Error checking existing PR: %v", err)
		}

		if branchExists {
			// Push to the fork in the existing branch (force)
			WriteSidebandLine(&response, 2, "remote: gitGost: Pushing update to existing branch...")
			branch, err = git.PushToGitHub(owner, repo, tempDir, forkOwner, branchFromHash)
			if err != nil {
				utils.Log("Error pushing update to fork: %v", err)
				WriteSidebandLine(&response, 1, "unpack ok\n")
				WriteSidebandLine(&response, 3, fmt.Sprintf("error pushing update: %v", err))
				WritePktLine(&response, "")
				c.Writer.Write(response.Bytes())
				return
			}
			if existingPRURL != "" {
				// PR found: update successful
				prURL = existingPRURL
				isUpdate = true
				utils.Log("Updated existing branch: %s, PR: %s", branch, prURL)
			} else {
				// Branch exists but PR was closed/merged: create new PR
				WriteSidebandLine(&response, 2, "remote: gitGost: PR was closed, creating new PR on existing branch...")
				prURL, err = github.CreatePR(owner, repo, branch, forkOwner, commitMessage)
				if err != nil {
					utils.Log("Error creating PR on existing branch: %v", err)
					WriteSidebandLine(&response, 1, "unpack ok\n")
					WriteSidebandLine(&response, 3, fmt.Sprintf("error creating PR: %v", err))
					WritePktLine(&response, "")
					c.Writer.Write(response.Bytes())
					return
				}
				isUpdate = true
				utils.Log("Created new PR on existing branch: %s, PR: %s", branch, prURL)
				if err := RecordPR(c.Request.Context(), owner, repo, prURL); err != nil {
					utils.Log("Error recording stats: %v", err)
				}
			}
		} else {
			// The hash does not correspond to an existing branch: create new PR
			utils.Log("PR hash not found, creating new PR")
			WriteSidebandLine(&response, 2, "remote: gitGost: Hash not found, creating new PR...")
		}
	}

	if !isUpdate {
		// Normal flow: push to new branch and create PR
		WriteSidebandLine(&response, 2, "remote: gitGost: Pushing to fork...")
		branch, err = git.PushToGitHub(owner, repo, tempDir, forkOwner, "")
		if err != nil {
			utils.Log("Error pushing to fork: %v", err)
			WriteSidebandLine(&response, 1, "unpack ok\n")
			WriteSidebandLine(&response, 3, fmt.Sprintf("error pushing to fork: %v", err))
			WritePktLine(&response, "")
			c.Writer.Write(response.Bytes())
			return
		}

		utils.Log("Pushed to fork branch: %s", branch)
		WriteSidebandLine(&response, 2, fmt.Sprintf("remote: gitGost: Branch '%s' created", branch))

		// Create PR from the fork to the original repository
		WriteSidebandLine(&response, 2, "remote: gitGost: Creating pull request...")
		prURL, err = github.CreatePR(owner, repo, branch, forkOwner, commitMessage)
		if err != nil {
			utils.Log("Error creating PR: %v", err)
			WriteSidebandLine(&response, 1, "unpack ok\n")
			WriteSidebandLine(&response, 3, fmt.Sprintf("error creating PR: %v", err))
			WritePktLine(&response, "")
			c.Writer.Write(response.Bytes())
			return
		}

		utils.Log("Created PR: %s", prURL)

		// Record statistics
		if err := RecordPR(c.Request.Context(), owner, repo, prURL); err != nil {
			utils.Log("Error recording stats: %v", err)
		}
	}

	// Register PR URL for potential burst rollback only while a burst alert is active;
	// prune entries older than TTL regardless.
	if isGlobalBurstAlertActive() {
		nowPR := time.Now()
		recentBurstPRsMu.Lock()
		cutoffPR := nowPR.Add(-recentBurstPRsTTL)
		newURLs := recentBurstPRs[:0]
		newAts := recentBurstPRsAt[:0]
		for i, at := range recentBurstPRsAt {
			if at.After(cutoffPR) {
				newURLs = append(newURLs, recentBurstPRs[i])
				newAts = append(newAts, at)
			}
		}
		newURLs = append(newURLs, prURL)
		newAts = append(newAts, nowPR)
		recentBurstPRs = newURLs
		recentBurstPRsAt = newAts
		recentBurstPRsMu.Unlock()
	}

	// Generate pr-hash for this branch (deterministic: owner/repo/branch)
	outPRHash := github.GeneratePRHash(owner, repo, branch)

	// Publish ntfy event in background (does not block the Git response)
	go func() {
		ntfyTopic := github.NtfyTopicForPR(outPRHash)
		var ntfyTitle, ntfyMsg string
		if isUpdate {
			ntfyTitle = "PR Updated · gitGost"
			ntfyMsg = fmt.Sprintf("Your anonymous PR was updated.\nPR: %s\nTopic: %s/%s", prURL, github.NtfyBaseURL(), ntfyTopic)
		} else {
			ntfyTitle = "PR Created · gitGost"
			ntfyMsg = fmt.Sprintf("Your anonymous PR was created.\nPR: %s\nTopic: %s/%s", prURL, github.NtfyBaseURL(), ntfyTopic)
		}
		if err := github.PublishNtfyEvent(outPRHash, ntfyTitle, ntfyMsg); err != nil {
			utils.Log("ntfy publish error for hash %s: %v", outPRHash, err)
		}
	}()

	// CLEAR SUCCESS MESSAGES
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, "remote: ========================================")
	if isUpdate {
		WriteSidebandLine(&response, 2, "remote: SUCCESS! Pull Request Updated")
	} else {
		WriteSidebandLine(&response, 2, "remote: SUCCESS! Pull Request Created")
	}
	WriteSidebandLine(&response, 2, "remote: ========================================")
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote: PR URL: %s", prURL))
	WriteSidebandLine(&response, 2, "remote: Author: @gitgost-anonymous")
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote: Branch: %s", branch))
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote: PR Hash: %s", outPRHash))
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, "remote: Subscribe to PR notifications (no account needed):")
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote:   %s/%s", github.NtfyBaseURL(), github.NtfyTopicForPR(outPRHash)))
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, "remote: To update this PR on future pushes, use:")
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote:   git push gost <branch>:main -o pr-hash=%s", outPRHash))
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, "remote: Your identity has been anonymized.")
	WriteSidebandLine(&response, 2, "remote: No trace to you remains in the commit history.")
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, "remote: ========================================")
	WriteSidebandLine(&response, 2, "remote: ")

	// Standard Git response (sideband 1 = protocol data)
	WriteSidebandLine(&response, 1, "unpack ok\n")
	WriteSidebandLine(&response, 1, "ok refs/heads/main\n")
	WritePktLine(&response, "") // final flush

	c.Writer.Write(response.Bytes())
	c.Writer.Flush()

	// Small delay to allow Git to process the response and close its side first
	time.Sleep(100 * time.Millisecond)
}

func UploadPackDiscoveryHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	githubURL := fmt.Sprintf("https://github.com/%s/%s.git/info/refs?service=git-upload-pack", owner, repo)
	req, err := http.NewRequest("GET", githubURL, nil)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}
	req.Header.Set("User-Agent", "git/2.0")

	resp, err := uploadPackClient.Do(req)
	if err != nil {
		utils.Log("UploadPackDiscovery error: %v", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "failed to reach GitHub"})
		return
	}
	defer resp.Body.Close()

	c.Writer.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	c.Writer.Header().Set("WWW-Authenticate", "None")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		utils.Log("UploadPackDiscovery copy error (status %d): %v", resp.StatusCode, err)
	}
}

func UploadPackHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	const maxUploadBytes = 50 * 1024 * 1024 // 50 MB
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	githubURL := fmt.Sprintf("https://github.com/%s/%s.git/git-upload-pack", owner, repo)
	req, err := http.NewRequest("POST", githubURL, bytes.NewReader(body))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	req.Header.Set("Accept", "application/x-git-upload-pack-result")
	req.Header.Set("User-Agent", "git/2.0")

	resp, err := uploadPackClient.Do(req)
	if err != nil {
		utils.Log("UploadPack error: %v", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "failed to reach GitHub"})
		return
	}
	defer resp.Body.Close()

	c.Writer.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		utils.Log("UploadPack copy error: %v", err)
	}
}

func basicAuth(username, password string) string {
	credentials := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(credentials))
}

// sendErrorResponse sends an error response in Git protocol format
func sendErrorResponse(c *gin.Context, errorMsg string) {
	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	c.Writer.WriteHeader(http.StatusOK)
	var response bytes.Buffer
	WriteSidebandLine(&response, 3, errorMsg)
	WritePktLine(&response, "")
	c.Writer.Write(response.Bytes())
}

func HealthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":         "healthy",
		"deployedCommit": commitHash,
		"deployedAt":     buildTime,
		"sourceRepo":     sourceRepo,
		"leapcell":       true,
		"goVersion":      runtime.Version(),
		"verify": gin.H{
			"github": fmt.Sprintf("https://github.com/livrasand/gitGost/commit/%s", commitHash),
			"source": "100% open source - auditable",
		},
	})
}

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

var (
	commitHash = "dev"
	buildTime  = "unknown"
	sourceRepo = "https://github.com/livrasand/gitGost"
)

// SetBuildInfo allows main to inject compiled values with -ldflags
func SetBuildInfo(hash, built, repo string) {
	commitHash = hash
	buildTime = built
	sourceRepo = repo
}

var (
	startTime  = time.Now()
	dbClient   *database.SupabaseClient
	dbOnce     sync.Once
	secretKey  []byte
	identityMu sync.Mutex
	// karmaStore stores karma per hash (in-memory fallback)
	karmaStore = make(map[string]int)
	// reportCounts stores report counts per hash (in-memory fallback)
	reportCounts      = make(map[string]int)
	reportFirstAt     = make(map[string]time.Time)
	reportIPs         = make(map[string]map[string]time.Time)
	flaggedLastAction = make(map[string]time.Time)
	blockedHashes     = make(map[string]bool)

	// panicMode: service temporarily suspended
	panicMode      bool
	panicMu        sync.Mutex
	panicPassword  string
	ntfyAdminTopic string

	// rateLimitStore: PR counter per IP within a 1-hour window
	rateLimitStore  = make(map[string][]time.Time)
	rateLimitMu     sync.Mutex
	rateLimitWindow = time.Hour
	rateLimitMaxPRs = 5

	// globalBurst: tracks all push attempts globally to detect botnet/script activity
	// across multiple IPs in a short time window
	globalBurstMu       sync.Mutex
	globalBurstTimes    []time.Time        // timestamps of all pushes
	globalBurstIPs      []string           // IPs corresponding to each push
	globalBurstWindow   = 60 * time.Second // sliding window
	globalBurstMaxTotal = 20               // max pushes globally in the window
	globalBurstMaxIPs   = 10               // max distinct IPs in the window
	globalBurstAlerted  bool               // avoid repeated alerts

	// recentBurstPRs: PR URLs created during the current burst window, for rollback.
	// Entries older than recentBurstPRsTTL are pruned on each registration.
	recentBurstPRsMu  sync.Mutex
	recentBurstPRs    []string
	recentBurstPRsAt  []time.Time     // creation time per PR URL
	recentBurstPRsTTL = 2 * time.Hour // keep burst PRs for 2 hours max

	// actionTokens: short-lived tokens used in ntfy action buttons instead of panicPassword.
	// Each token is single-use and expires after actionTokenTTL.
	actionTokensMu sync.Mutex
	actionTokens   = make(map[string]time.Time) // token → expiry
	actionTokenTTL = 10 * time.Minute

	// adminRollbackLimit: simple rate limit for /admin/rollback (max 5 calls/min)
	rollbackLimitMu    sync.Mutex
	rollbackLimitTimes []time.Time
	rollbackLimitMax   = 5
	rollbackLimitWin   = time.Minute

	reportFormTmpl   = template.Must(template.New("reportForm").Parse(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8" /><title>Report content · gitGost</title><style>body{font-family:Inter,system-ui,-apple-system,Segoe UI,sans-serif;background:#0d1117;color:#c9d1d9;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:32px;} .shell{background:linear-gradient(145deg, rgba(255,166,87,0.16), rgba(255,107,107,0.14));border:1px solid rgba(255,166,87,0.45);border-radius:16px;padding:1.5px;box-shadow:0 16px 38px rgba(0,0,0,.42);max-width:620px;width:100%;} .card{background:#0d1117;border-radius:14px;padding:26px;border:1px solid rgba(255,255,255,0.05);} h1{margin:0 0 6px;font-size:24px;color:#ffa657;} .eyebrow{display:inline-flex;align-items:center;gap:.35rem;padding:.35rem .75rem;background:rgba(255,166,87,0.12);color:#ffa657;border:1px solid rgba(255,166,87,0.4);border-radius:999px;font-family:'IBM Plex Mono', monospace;font-size:.85rem;margin-bottom:5px;} .sub{margin:6px 0 14px;color:#9fb3ff;font-size:14px;} .policy{background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.05);border-radius:12px;padding:14px;margin:14px 0;font-size:13px;line-height:1.55;} .policy strong{color:#ffa657;} label{display:block;font-weight:700;margin:12px 0 6px;letter-spacing:.01em;} .readonly{background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.08);border-radius:10px;padding:12px;color:#c9d1d9;font-family:'IBM Plex Mono', monospace;} button{margin-top:14px;width:100%;padding:12px;border-radius:10px;border:none;background:linear-gradient(135deg,#ffa657,#ff6b6b);color:#0d1117;font-weight:700;font-size:15px;cursor:pointer;box-shadow:0 10px 30px rgba(0,0,0,0.25);} .note{margin-top:10px;font-size:12px;color:#9fb3ff;} .error{color:#ffb4c4;font-size:13px;margin-top:10px;} .count{display:flex;gap:8px;align-items:center;margin:10px 0;font-family:'IBM Plex Mono', monospace;} .pill{padding:6px 10px;border-radius:999px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.04);} .pill strong{color:#ffa657;} .state{margin-left:auto;font-size:12px;color:#9fb3ff;} .legend{font-size:12px;color:#9fb3ff;margin-top:10px;} input[type=text]{width:100%;padding:12px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.04);color:#c9d1d9;} form{margin-top:12px;} a{color:#9fb3ff;} .locked{opacity:.55;pointer-events:none;} </style></head><body><div class="shell"><div class="card"><div class="eyebrow">Anonymous moderation</div><h1>Report content</h1><div class="sub">Flag abuse from anonymous contributions.</div><div class="policy"><ul style="margin:0 0 6px 18px; padding:0 0 0 4px; line-height:1.6;">` + string(reportPolicyHTML) + `</ul><div class="note">Reports reset after 30 days.</div></div><form method="POST" action="/v1/moderation/report"><label for="hash">Hash</label><input type="text" id="hash" name="hash" value="{{.Hash}}" placeholder="goster-xxxxx" {{if eq .State "blocked"}}class="locked" readonly{{end}} /><div class="count"><div class="pill">Reports: <strong>{{.Reports}}</strong></div><div class="state">State: {{.State}}</div></div><button type="submit" {{if eq .State "blocked"}}disabled class="locked"{{end}}>Submit report</button></form><div class="legend">Hash identifies the anonymous submitter. No personal data is collected.</div>{{if .Error}}<div class="error">{{.Error}}</div>{{end}}</div></div></body></html>`))
	reportThanksTmpl = template.Must(template.New("reportThanks").Parse(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8" /><title>Report received · gitGost</title><style>body{font-family:Inter,system-ui,-apple-system,Segoe UI,sans-serif;background:#0d1117;color:#c9d1d9;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:32px;} .shell{background:linear-gradient(145deg, rgba(255,166,87,0.16), rgba(255,107,107,0.14));border:1px solid rgba(255,166,87,0.45);border-radius:16px;padding:1.5px;box-shadow:0 16px 38px rgba(0,0,0,.42);max-width:620px;width:100%;} .card{background:#0d1117;border-radius:14px;padding:26px;border:1px solid rgba(255,255,255,0.05);} h1{margin:0 0 10px;font-size:24px;color:#ffa657;} p{margin:6px 0 0;color:#9fb3ff;} .pill{display:inline-block;margin-top:12px;padding:8px 12px;border-radius:999px;background:rgba(255,255,255,0.04);color:#ffa657;font-weight:700;border:1px solid rgba(255,255,255,0.08);} .cta{margin-top:16px;display:inline-block;padding:12px 16px;border-radius:10px;background:linear-gradient(135deg,#ffa657,#ff6b6b);color:#0d1117;font-weight:700;text-decoration:none;box-shadow:0 10px 30px rgba(0,0,0,0.25);} .small{margin-top:12px;font-size:12px;color:#9fb3ff;} .state{margin-top:10px;font-size:14px;} </style></head><body><div class="shell"><div class="card"><h1>Report received</h1><p>Hash: <strong>{{.Hash}}</strong></p><span class="pill">Total reports: {{.Reports}}</span><div class="state">State: {{.State}}</div><p class="small">Thanks for helping moderate. Your identity stays anonymous.</p><a class="cta" href="https://gitgost.leapcell.app/" target="_blank" rel="noreferrer">Explore gitGost</a></div></div></body></html>`))
)

type anonymousIssueRequest struct {
	// ...
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
}

type anonymousCommentRequest struct {
	UserToken string `json:"user_token"`
	Body      string `json:"body"`
}

const (
	reportWindow    = 30 * 24 * time.Hour
	flaggedCooldown = 6 * time.Hour
)

var reportPolicyHTML = template.HTML(`<li><strong>0–2 reports:</strong> internal log only.</li><li><strong>3–5 reports:</strong> hash flagged, 6h cooldown, karma reset.</li><li><strong>6+ reports:</strong> hash blocked; we attempt to remove its comments.</li>`)

// newActionToken generates a single-use token valid for actionTokenTTL and stores it.
func newActionToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	token := hex.EncodeToString(b)
	expiry := time.Now().Add(actionTokenTTL)
	actionTokensMu.Lock()
	actionTokens[token] = expiry
	actionTokensMu.Unlock()
	return token
}

// consumeActionToken validates and removes a single-use action token.
// Returns true if the token was valid and not expired.
func consumeActionToken(token string) bool {
	actionTokensMu.Lock()
	defer actionTokensMu.Unlock()
	expiry, ok := actionTokens[token]
	if !ok {
		return false
	}
	delete(actionTokens, token)
	return time.Now().Before(expiry)
}

// InitPanicConfig initializes the panic button password and ntfy admin topic
func InitPanicConfig(password, adminTopic string) {
	panicPassword = password
	ntfyAdminTopic = adminTopic
}

// isPanicMode returns whether the service is suspended
func isPanicMode() bool {
	panicMu.Lock()
	defer panicMu.Unlock()
	return panicMode
}

// isGlobalBurstAlertActive returns true when a global burst alert is currently active.
func isGlobalBurstAlertActive() bool {
	globalBurstMu.Lock()
	defer globalBurstMu.Unlock()
	return globalBurstAlerted
}

// recordGlobalBurst records a push attempt globally and notifies the admin if suspicious
// activity is detected (too many pushes in a short window, possibly from multiple IPs).
func recordGlobalBurst(ip string) {
	now := time.Now()
	globalBurstMu.Lock()
	defer globalBurstMu.Unlock()

	// Slide the window: discard entries older than globalBurstWindow
	cutoff := now.Add(-globalBurstWindow)
	newTimes := globalBurstTimes[:0]
	newIPs := globalBurstIPs[:0]
	for i, t := range globalBurstTimes {
		if t.After(cutoff) {
			newTimes = append(newTimes, t)
			newIPs = append(newIPs, globalBurstIPs[i])
		}
	}
	newTimes = append(newTimes, now)
	newIPs = append(newIPs, ip)
	globalBurstTimes = newTimes
	globalBurstIPs = newIPs

	total := len(globalBurstTimes)

	// Count distinct IPs in window
	seen := make(map[string]struct{}, total)
	for _, bip := range globalBurstIPs {
		seen[bip] = struct{}{}
	}
	distinctIPs := len(seen)

	// Trigger alert if thresholds exceeded and not already alerted in this window
	if !globalBurstAlerted && (total >= globalBurstMaxTotal || distinctIPs >= globalBurstMaxIPs) {
		globalBurstAlerted = true
		go notifyAdminGlobalBurst(total, distinctIPs)
	}

	// Reset alert flag once activity drops below half the threshold
	if globalBurstAlerted && total < globalBurstMaxTotal/2 && distinctIPs < globalBurstMaxIPs/2 {
		globalBurstAlerted = false
	}
}

// notifyAdminGlobalBurst sends an ntfy alert about suspected botnet/script activity
func notifyAdminGlobalBurst(total, distinctIPs int) {
	if ntfyAdminTopic == "" {
		return
	}
	serviceURL := github.NtfyServiceURL()
	title := "🚨 Suspicious activity detected · gitGost"
	msg := fmt.Sprintf(
		"%d push attempts from %d distinct IPs in the last %s. This may indicate bot, script, or coordinated abuse.",
		total, distinctIPs, globalBurstWindow,
	)
	// Generate single-use tokens per action (expire in 10 min, never expose panicPassword)
	tokActivate := newActionToken()
	tokRollback := newActionToken()
	tokDeactivate := newActionToken()
	// ntfy action buttons: panic control + close burst PRs
	actions := fmt.Sprintf(
		`http, Activate Panic, %s/admin/panic, method=POST, body={"token":"%s","active":true}, clear=true; http, Close Burst PRs, %s/admin/rollback, method=POST, body={"token":"%s"}, clear=true; http, Deactivate Panic, %s/admin/panic, method=POST, body={"token":"%s","active":false}`,
		serviceURL, tokActivate,
		serviceURL, tokRollback,
		serviceURL, tokDeactivate,
	)
	if err := github.PublishNtfyAdmin(ntfyAdminTopic, title, msg, actions); err != nil {
		utils.Log("ntfy global burst alert error: %v", err)
	}
}

// checkRateLimit checks if the IP has exceeded the PR rate limit per hour.
// Returns true if the request should be blocked. Notifies admin via ntfy on first excess.
func checkRateLimit(ip string) bool {
	now := time.Now()
	rateLimitMu.Lock()
	times := rateLimitStore[ip]
	// Keep only timestamps within the window
	valid := times[:0]
	for _, t := range times {
		if now.Sub(t) < rateLimitWindow {
			valid = append(valid, t)
		}
	}
	valid = append(valid, now)
	rateLimitStore[ip] = valid
	count := len(valid)
	rateLimitMu.Unlock()

	if count > rateLimitMaxPRs {
		// Notify admin only once when the limit is first exceeded (at rateLimitMaxPRs+1)
		if count == rateLimitMaxPRs+1 {
			go notifyAdminRateLimit(ip, count)
		}
		return true
	}
	return false
}

// notifyAdminRateLimit sends an ntfy alert to the admin when an IP exceeds the rate limit
func notifyAdminRateLimit(ip string, count int) {
	if ntfyAdminTopic == "" {
		return
	}
	serviceURL := github.NtfyServiceURL()
	title := "⚠️ Rate limit exceeded · gitGost"
	msg := fmt.Sprintf("IP %s exceeded the limit of %d PRs/hour (attempts: %d).", ip, rateLimitMaxPRs, count)
	// Generate single-use tokens per action (expire in 10 min, never expose panicPassword)
	tokActivate := newActionToken()
	tokRollback := newActionToken()
	tokDeactivate := newActionToken()
	// ntfy action buttons: panic control + close burst PRs
	actions := fmt.Sprintf(
		`http, Activate Panic, %s/admin/panic, method=POST, body={"token":"%s","active":true}, clear=true; http, Close Burst PRs, %s/admin/rollback, method=POST, body={"token":"%s"}, clear=true; http, Deactivate Panic, %s/admin/panic, method=POST, body={"token":"%s","active":false}`,
		serviceURL, tokActivate,
		serviceURL, tokRollback,
		serviceURL, tokDeactivate,
	)
	if err := github.PublishNtfyAdmin(ntfyAdminTopic, title, msg, actions); err != nil {
		utils.Log("ntfy admin alert error: %v", err)
	}
}

// PanicHandler activates or deactivates panic mode
// POST /admin/panic  body: {"password": "...", "active": true|false}
//
//	or body: {"token": "<action-token>", "active": true|false}
func PanicHandler(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
		Token    string `json:"token"`
		Active   bool   `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}
	authorized := (panicPassword != "" && req.Password == panicPassword) ||
		(req.Token != "" && consumeActionToken(req.Token))
	if !authorized {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	panicMu.Lock()
	panicMode = req.Active
	panicMu.Unlock()

	state := "deactivated"
	if req.Active {
		state = "activated"
	}
	utils.Log("panic mode %s", state)
	c.JSON(http.StatusOK, gin.H{"panic_mode": req.Active, "state": state})
}

// ServiceStatusHandler returns the current service status (used by the frontend)
func ServiceStatusHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"panic_mode": isPanicMode(),
	})
}

// RollbackBurstHandler closes all PRs registered during the current burst window.
// POST /admin/rollback  body: {"password": "..."} or {"token": "<action-token>"}
func RollbackBurstHandler(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// Accept either the static password or a valid single-use action token
	authorized := (panicPassword != "" && req.Password == panicPassword) ||
		(req.Token != "" && consumeActionToken(req.Token))
	if !authorized {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	// Rate limit: max rollbackLimitMax calls per rollbackLimitWin
	now := time.Now()
	rollbackLimitMu.Lock()
	valid := rollbackLimitTimes[:0]
	for _, t := range rollbackLimitTimes {
		if now.Sub(t) < rollbackLimitWin {
			valid = append(valid, t)
		}
	}
	valid = append(valid, now)
	rollbackLimitTimes = valid
	exceeded := len(valid) > rollbackLimitMax
	rollbackLimitMu.Unlock()
	if exceeded {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "rollback rate limit exceeded"})
		return
	}

	recentBurstPRsMu.Lock()
	toClose := make([]string, len(recentBurstPRs))
	copy(toClose, recentBurstPRs)
	recentBurstPRs = recentBurstPRs[:0]
	recentBurstPRsAt = recentBurstPRsAt[:0]
	recentBurstPRsMu.Unlock()

	if len(toClose) == 0 {
		c.JSON(http.StatusOK, gin.H{"closed": 0, "message": "no burst PRs to close"})
		return
	}

	const maxCloseWorkers = 5
	closeConcurrency := make(chan struct{}, maxCloseWorkers)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		closed []string
		failed []string
	)
	for _, prURL := range toClose {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			closeConcurrency <- struct{}{}
			defer func() { <-closeConcurrency }()
			if err := github.ClosePRByURL(u); err != nil {
				utils.Log("rollback: failed to close %s: %v", u, err)
				mu.Lock()
				failed = append(failed, u)
				mu.Unlock()
			} else {
				mu.Lock()
				closed = append(closed, u)
				mu.Unlock()
			}
		}(prURL)
	}
	wg.Wait()

	utils.Log("rollback: closed %d PRs, failed %d", len(closed), len(failed))
	c.JSON(http.StatusOK, gin.H{
		"closed":      len(closed),
		"failed":      len(failed),
		"closed_urls": closed,
		"failed_urls": failed,
	})
}

// InitDatabase inicializa el cliente de Supabase de forma thread-safe
func InitDatabase(url, key string) {
	dbOnce.Do(func() {
		dbClient = database.NewSupabaseClient(url, key)
	})
}

// RecordPR registra un nuevo PR anonimizado en Supabase
func RecordPR(ctx context.Context, owner, repo, prURL string) error {
	if dbClient == nil {
		return fmt.Errorf("database client not initialized")
	}
	return dbClient.InsertPR(ctx, owner, repo, prURL)
}

// StatsHandler maneja el endpoint de estadísticas
func StatsHandler(c *gin.Context) {
	if dbClient == nil {
		c.JSON(http.StatusOK, gin.H{"total_prs": 0})
		return
	}

	totalPRs, err := dbClient.GetTotalPRs(c.Request.Context())
	if err != nil {
		utils.Log("Error getting total PRs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load stats"})
		return
	}

	lastUpdated, err := dbClient.GetLatestPRCreatedAt(c.Request.Context())
	if err != nil {
		utils.Log("Error getting latest PR timestamp: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load stats"})
		return
	}

	response := gin.H{
		"total_prs": totalPRs,
	}

	// Solo incluir last_updated si hay PRs
	if lastUpdated != nil {
		response["last_updated"] = lastUpdated
	}

	c.JSON(http.StatusOK, response)
}

// RecentPRsHandler devuelve los PRs recientes
func RecentPRsHandler(c *gin.Context) {
	if dbClient == nil {
		c.JSON(http.StatusOK, gin.H{"prs": []database.PRRecord{}, "total": 0})
		return
	}

	prs, err := dbClient.GetRecentPRs(c.Request.Context(), 10)
	if err != nil {
		utils.Log("Error getting recent PRs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load PRs"})
		return
	}

	totalPRs, err := dbClient.GetTotalPRs(c.Request.Context())
	if err != nil {
		utils.Log("Error getting total PRs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load total count"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"prs":   prs,
		"total": totalPRs,
	})
}

// CreateAnonymousIssueHandler crea una issue anónima con hash/karma/token
func CreateAnonymousIssueHandler(c *gin.Context) {
	var req anonymousIssueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	owner := c.Param("owner")
	repo := c.Param("repo")

	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Body) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title and body are required"})
		return
	}

	issueURL, issueNumber, err := github.CreateAnonymousIssue(owner, repo, req.Title, req.Body, req.Labels)
	if err != nil {
		utils.Log("Error creating issue: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userToken := generateUserToken()
	hash := deriveHash(owner, repo, issueNumber, userToken)
	karma := getKarma(c.Request.Context(), hash)
	updateKarma(c.Request.Context(), hash, karma)

	resp := gin.H{
		"issue_url":         issueURL,
		"number":            issueNumber,
		"hash":              hash,
		"karma":             karma,
		"user_token":        userToken,
		"issue_reply_token": userToken,
	}

	c.JSON(http.StatusOK, resp)
}

// CreateAnonymousCommentHandler publica comentario con hash/karma
func CreateAnonymousCommentHandler(c *gin.Context) {
	var req anonymousCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	owner := c.Param("owner")
	repo := c.Param("repo")
	numberStr := c.Param("number")
	number, err := strconv.Atoi(numberStr)
	if err != nil || number <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid issue number"})
		return
	}

	if strings.TrimSpace(req.Body) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body is required"})
		return
	}

	userToken := req.UserToken
	if strings.TrimSpace(userToken) == "" {
		userToken = generateUserToken()
	}
	hash := deriveHash(owner, repo, number, userToken)
	reports := getReportCountWithWindow(c.Request.Context(), hash)
	if reports > 5 {
		c.JSON(http.StatusForbidden, gin.H{"error": "hash bloqueado por reportes"})
		return
	}
	if reports > 2 {
		if blocked := isFlaggedCooldown(hash); blocked {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "cooldown activo por reportes"})
			return
		}
	}
	currentKarma := getKarma(c.Request.Context(), hash)
	karma := currentKarma + 1
	if reports > 2 {
		karma = 0
	}
	updateKarma(c.Request.Context(), hash, karma)
	if reports > 2 {
		markFlaggedAction(hash)
		if err := github.UpdateCommentsKarmaByHash(hash, 0); err != nil {
			utils.Log("Error updating comment karma for hash %s: %v", hash, err)
		}
	}
	reportURL := fmt.Sprintf("%s://%s/v1/moderation/report?hash=%s", getScheme(c.Request), c.Request.Host, hash)

	legend := fmt.Sprintf("\n\n---\ngoster-%s · karma (%d) · [report](%s)", hash, karma, reportURL)
	bodyWithLegend := req.Body + legend

	commentURL, err := github.CreateAnonymousComment(owner, repo, number, bodyWithLegend)
	if err != nil {
		utils.Log("Error creating comment: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{
		"comment_url": commentURL,
		"hash":        hash,
		"karma":       karma,
		"user_token":  userToken,
	}

	c.JSON(http.StatusOK, resp)
}

// CreateAnonymousPRCommentHandler publica un comentario anónimo en un Pull Request
func CreateAnonymousPRCommentHandler(c *gin.Context) {
	var req anonymousCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	owner := c.Param("owner")
	repo := c.Param("repo")
	numberStr := c.Param("number")
	number, err := strconv.Atoi(numberStr)
	if err != nil || number <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid PR number"})
		return
	}

	if strings.TrimSpace(req.Body) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body is required"})
		return
	}

	userToken := req.UserToken
	if strings.TrimSpace(userToken) == "" {
		userToken = generateUserToken()
	}
	hash := deriveHash(owner, repo, number, userToken)
	reports := getReportCountWithWindow(c.Request.Context(), hash)
	if reports > 5 {
		c.JSON(http.StatusForbidden, gin.H{"error": "hash bloqueado por reportes"})
		return
	}
	if reports > 2 {
		if blocked := isFlaggedCooldown(hash); blocked {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "cooldown activo por reportes"})
			return
		}
	}
	currentKarma := getKarma(c.Request.Context(), hash)
	karma := currentKarma + 1
	if reports > 2 {
		karma = 0
	}
	updateKarma(c.Request.Context(), hash, karma)
	if reports > 2 {
		markFlaggedAction(hash)
		if err := github.UpdateCommentsKarmaByHash(hash, 0); err != nil {
			utils.Log("Error updating PR comment karma for hash %s: %v", hash, err)
		}
	}
	reportURL := fmt.Sprintf("%s://%s/v1/moderation/report?hash=%s", getScheme(c.Request), c.Request.Host, hash)

	legend := fmt.Sprintf("\n\n---\ngoster-%s · karma (%d) · [report](%s)", hash, karma, reportURL)
	bodyWithLegend := req.Body + legend

	commentURL, err := github.CreateAnonymousPRComment(owner, repo, number, bodyWithLegend)
	if err != nil {
		utils.Log("Error creating PR comment: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"comment_url": commentURL,
		"hash":        hash,
		"karma":       karma,
		"user_token":  userToken,
	})
}

// ReportHashHandler permite reportar un hash
func ReportHashHandler(c *gin.Context) {
	if c.Request.Method == http.MethodGet {
		hash := strings.TrimSpace(c.Query("hash"))
		if hash == "" {
			c.Header("Content-Type", "text/html; charset=utf-8")
			_ = reportFormTmpl.Execute(c.Writer, gin.H{"Hash": "", "Reports": 0, "State": "sin datos", "Error": "El hash es obligatorio", "PolicyHTML": reportPolicyHTML})
			return
		}
		if isBlockedHash(hash) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			_ = reportFormTmpl.Execute(c.Writer, gin.H{"Hash": hash, "Reports": 6, "State": "bloqueado", "Error": "Este hash ya fue baneado/eliminado.", "PolicyHTML": reportPolicyHTML})
			return
		}
		reports := getReportCountWithWindow(c.Request.Context(), hash)
		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = reportFormTmpl.Execute(c.Writer, gin.H{"Hash": hash, "Reports": reports, "State": reportStateLabel(reports), "PolicyHTML": reportPolicyHTML})
		return
	}

	hash := strings.TrimSpace(c.PostForm("hash"))
	if hash == "" {
		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = reportFormTmpl.Execute(c.Writer, gin.H{"Hash": "", "Reports": 0, "State": "sin datos", "Error": "El hash es obligatorio.", "PolicyHTML": reportPolicyHTML})
		return
	}

	ip := strings.TrimSpace(c.ClientIP())
	reports := recordReport(c.Request.Context(), hash, ip)
	if reports >= 6 {
		setBlockedHash(hash)
		go func(h string) {
			if err := github.DeleteCommentsByHash(h); err != nil {
				utils.Log("Error deleting comments for hash %s: %v", h, err)
			}
		}(hash)
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = reportThanksTmpl.Execute(c.Writer, gin.H{"Hash": hash, "Reports": reports, "State": reportStateLabel(reports)})
}

func recordReport(ctx context.Context, hash, ip string) int {
	reports := 0
	if dbClient != nil {
		_ = dbClient.DeleteOldReports(ctx, hash, time.Now().Add(-reportWindow))
		if exists, err := dbClient.HasReportFromIP(ctx, hash, ip); err == nil && exists {
			if count, err := dbClient.GetReportCount(ctx, hash); err == nil {
				return count
			}
			return 0
		}
		if err := dbClient.InsertReport(ctx, hash, ip); err == nil {
			if count, err := dbClient.GetReportCount(ctx, hash); err == nil {
				reports = count
			}
		}
	}

	if reports == 0 {
		identityMu.Lock()
		first, ok := reportFirstAt[hash]
		if !ok || time.Since(first) > reportWindow {
			reportCounts[hash] = 0
			reportFirstAt[hash] = time.Now()
			reportIPs[hash] = make(map[string]time.Time)
		}
		if ip != "" {
			if ipTimes, ok := reportIPs[hash]; ok {
				if t, ok := ipTimes[ip]; ok && time.Since(t) <= reportWindow {
					reports = reportCounts[hash]
					identityMu.Unlock()
					return reports
				}
			} else {
				reportIPs[hash] = make(map[string]time.Time)
			}
		}
		reportCounts[hash]++
		reports = reportCounts[hash]
		if ip != "" {
			reportIPs[hash][ip] = time.Now()
		}
		identityMu.Unlock()
	}

	if reports >= 3 && reports <= 5 {
		updateKarma(ctx, hash, 0)
		markFlaggedAction(hash)
		if err := github.UpdateCommentsKarmaByHash(hash, 0); err != nil {
			utils.Log("Error updating comment karma for hash %s: %v", hash, err)
		}
	}

	return reports
}

func getReportCountWithWindow(ctx context.Context, hash string) int {
	if hash == "" {
		return 0
	}
	if dbClient != nil {
		_ = dbClient.DeleteOldReports(ctx, hash, time.Now().Add(-reportWindow))
		if count, err := dbClient.GetReportCount(ctx, hash); err == nil {
			identityMu.Lock()
			first, ok := reportFirstAt[hash]
			if !ok || time.Since(first) > reportWindow {
				reportCounts[hash] = 0
				reportFirstAt[hash] = time.Now()
			}
			memCount := reportCounts[hash]
			identityMu.Unlock()
			if memCount > count {
				return memCount
			}
			return count
		}
	}

	identityMu.Lock()
	defer identityMu.Unlock()
	first, ok := reportFirstAt[hash]
	if !ok || time.Since(first) > reportWindow {
		reportCounts[hash] = 0
		reportFirstAt[hash] = time.Now()
	}
	return reportCounts[hash]
}

func reportStateLabel(count int) string {
	switch {
	case count >= 6:
		return "bloqueado"
	case count >= 3:
		return "flagged"
	default:
		return "registrado"
	}
}

func setBlockedHash(hash string) {
	if hash == "" {
		return
	}
	identityMu.Lock()
	blockedHashes[hash] = true
	identityMu.Unlock()
}

func isBlockedHash(hash string) bool {
	if hash == "" {
		return false
	}
	identityMu.Lock()
	blocked := blockedHashes[hash]
	identityMu.Unlock()
	return blocked
}

func isFlaggedCooldown(hash string) bool {
	identityMu.Lock()
	defer identityMu.Unlock()
	last, ok := flaggedLastAction[hash]
	if !ok {
		return false
	}
	return time.Since(last) < flaggedCooldown
}

func markFlaggedAction(hash string) {
	identityMu.Lock()
	flaggedLastAction[hash] = time.Now()
	identityMu.Unlock()
}

func getSecretKey() []byte {
	identityMu.Lock()
	defer identityMu.Unlock()
	if secretKey != nil {
		return secretKey
	}
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		// fallback
		b = []byte(time.Now().String())
	}
	secretKey = b
	return secretKey
}

func deriveHash(owner, repo string, number int, userToken string) string {
	input := fmt.Sprintf("%s/%s#%d|%s", owner, repo, number, userToken)
	h := hmac.New(sha256.New, getSecretKey())
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))[:8]
}

func generateUserToken() string {
	buf := make([]byte, 10)
	_, err := rand.Read(buf)
	if err != nil {
		return fmt.Sprintf("tok-%d", time.Now().UnixNano())
	}
	return strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
}

func getKarma(ctx context.Context, hash string) int {
	identityMu.Lock()
	if karma, ok := karmaStore[hash]; ok {
		identityMu.Unlock()
		return karma
	}
	identityMu.Unlock()

	if dbClient != nil {
		if karma, err := dbClient.GetKarma(ctx, hash); err == nil {
			identityMu.Lock()
			karmaStore[hash] = karma
			identityMu.Unlock()
			return karma
		}
	}

	identityMu.Lock()
	karmaStore[hash] = 0
	identityMu.Unlock()
	return 0
}

func updateKarma(ctx context.Context, hash string, karma int) {
	identityMu.Lock()
	karmaStore[hash] = karma
	identityMu.Unlock()
	if dbClient != nil {
		_ = dbClient.UpsertKarma(ctx, hash, karma)
	}
}

func getScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if scheme := r.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	return "http"
}

// BadgeHandler serves various badges
func BadgeHandler(c *gin.Context) {
	badge := c.Param("badge")
	switch badge {
	case "anonymous-friendly.svg":
		serveAnonymousFriendlyBadge(c)
	case "deployed.svg":
		// Si el servicio está suspendido, mostrar badge rojo
		if isPanicMode() {
			serveSuspendedBadge(c)
			return
		}
		serveDeployedBadge(c)
	default:
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "Badge not found"})
	}
}

func serveSuspendedBadge(c *gin.Context) {
	label := "gitGost"
	value := "suspended"
	labelW := 56
	valueW := 76
	totalW := labelW + valueW
	labelMid := labelW / 2
	valueMid := labelW + valueW/2

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s" viewBox="0 0 %d 20">
  <title>%s: %s</title>
  <linearGradient id="s" x2="0" y2="100%%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <clipPath id="r">
    <rect width="%d" height="20" rx="3" fill="#fff"/>
  </clipPath>
  <g clip-path="url(#r)">
    <rect width="%d" height="20" fill="#555"/>
    <rect x="%d" width="%d" height="20" fill="#e05d44"/>
    <rect width="%d" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="110">
    <text x="%d" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
    <text x="%d" y="140" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
    <text x="%d" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
    <text x="%d" y="140" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
  </g>
</svg>`,
		totalW, label, value, totalW,
		label, value,
		totalW,
		labelW,
		labelW, valueW,
		totalW,
		labelMid*10, (labelW-10)*10, label,
		labelMid*10, (labelW-10)*10, label,
		valueMid*10, (valueW-6)*10, value,
		valueMid*10, (valueW-6)*10, value,
	)

	c.Header("Content-Type", "image/svg+xml")
	c.Header("Cache-Control", "no-cache, no-store")
	c.String(http.StatusOK, svg)
}

func serveAnonymousFriendlyBadge(c *gin.Context) {
	repo := c.Query("repo")
	verified := false
	if repo != "" {
		parts := strings.Split(repo, "/")
		if len(parts) == 2 {
			owner, repoName := parts[0], parts[1]
			verified = github.IsRepoVerified(owner, repoName)
		}
	}

	fillColor := "#4CAF50" // green if static or verified
	if repo != "" && !verified {
		fillColor = "#9E9E9E" // gray if dynamic and not verified
	}

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="230" height="20.909" role="img" aria-label="Anonymous Contributor Friendly" viewBox="0 0 230 20.909"><title>Anonymous Contributor Friendly</title><path id="s" x2="0" y2="100%%" d=""><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></path><clipPath id="r"><path width="220" height="20" rx="3" fill="#fff" d="M3.136 0H226.864A3.136 3.136 0 0 1 230 3.136V17.773A3.136 3.136 0 0 1 226.864 20.909H3.136A3.136 3.136 0 0 1 0 17.773V3.136A3.136 3.136 0 0 1 3.136 0z"/></clipPath><a href="https://gitgost.leapcell.app/" target="_blank" rel="noreferrer"><g clip-path="url(#r)"><path width="28" height="20" fill="black" d="M0 0H29.273V20.909H0V0z"/><path x="28" width="192" height="20" fill="%s" d="M29.273 0H230V20.909H29.273V0z"/><path width="220" height="20" fill="url(#s)" d="M0 0H230V20.909H0V0z"/></g><g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="110"><g transform="matrix(.13 0 0 .13 8 3)"><path fill="#fff" d="M52.273 8.711c-19.219 0 -34.847 15.628 -34.847 34.851v43.558c0 4.786 3.925 8.715 8.711 8.715 3.582 0 6.534 -2.952 6.534 -6.534V84.943c0 -1.229 0.947 -2.177 2.177 -2.177s2.181 0.947 2.181 2.177v4.357c0 3.582 2.948 6.534 6.534 6.534 3.582 0 6.534 -2.952 6.534 -6.534V84.943c0 -1.229 0.947 -2.177 2.177 -2.177s2.177 0.947 2.177 2.177v4.357c0 3.582 2.952 6.534 6.534 6.534 3.586 0 6.534 -2.952 6.534 -6.534V84.943c0 -1.229 0.951 -2.177 2.181 -2.177s2.177 0.947 2.177 2.177v4.357c0 3.582 2.952 6.534 6.534 6.534 4.786 0 8.711 -3.929 8.711 -8.715V43.562c0 -19.223 -15.63 -34.851 -34.847 -34.851zM30.322 37.036c0.27 -0.024 0.539 0.008 0.801 0.086L52.273 43.468l21.142 -6.346a2.175 2.175 0 0 1 2.222 0.592c0.568 0.605 0.742 1.479 0.45 2.255l-6.534 17.426a2.175 2.175 0 0 1 -2.63 1.328L52.273 54.534l-14.649 4.186a2.175 2.175 0 0 1 -2.639 -1.328l-6.534 -17.425a2.17 2.17 0 0 1 1.871 -2.933z"/></g><text aria-hidden="true" x="1290" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="1900">Anonymous Contributor Friendly</text><text x="1290" y="140" transform="scale(.1)" fill="#fff" textLength="1900">Anonymous Contributor Friendly</text></g></a></svg>`, fillColor)

	c.Header("Content-Type", "image/svg+xml")
	c.String(http.StatusOK, svg)
}

func serveDeployedBadge(c *gin.Context) {
	commit := c.Query("commit")
	if commit == "" {
		commit = commitHash
	}
	if len(commit) > 7 {
		commit = commit[:7]
	}

	c.Header("Content-Type", "image/svg+xml")
	c.Header("Cache-Control", "public, max-age=3600")

	// Shields.io-compatible badge: label width ~72px, value width ~(len*7+10)px
	label := "deployed"
	labelW := 64
	valueW := len(commit)*7 + 10
	if valueW < 30 {
		valueW = 30
	}
	totalW := labelW + valueW
	labelMidX := labelW / 2
	valueMidX := labelW + valueW/2

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s" viewBox="0 0 %d 20">
  <title>%s: %s</title>
  <linearGradient id="s" x2="0" y2="100%%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <clipPath id="r">
    <rect width="%d" height="20" rx="3" fill="#fff"/>
  </clipPath>
  <g clip-path="url(#r)">
    <rect width="%d" height="20" fill="#555"/>
    <rect x="%d" width="%d" height="20" fill="#0ea5e9"/>
    <rect width="%d" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="110">
    <text x="%d" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
    <text x="%d" y="140" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
    <text x="%d" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
    <text x="%d" y="140" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
  </g>
</svg>`,
		totalW, label, commit, totalW,
		label, commit,
		totalW,
		labelW,
		labelW, valueW,
		totalW,
		labelMidX*10, (labelW-10)*10, label,
		labelMidX*10, (labelW-10)*10, label,
		valueMidX*10, (valueW-6)*10, commit,
		valueMidX*10, (valueW-6)*10, commit,
	)

	c.String(http.StatusOK, svg)
}

// badgeCache almacena el conteo de PRs por "owner/repo" con TTL de 5 minutos
var (
	badgeCache    = make(map[string]int)
	badgeCacheAt  = make(map[string]time.Time)
	badgeCacheMu  sync.Mutex
	badgeCacheTTL = 5 * time.Minute
)

// BadgePRCountHandler sirve un badge SVG dinámico con el conteo de PRs anónimos para owner/repo.
// GET /badge/:owner/:repo
func BadgePRCountHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	if !isValidRepoName(owner) || !isValidRepoName(repo) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid owner or repo"})
		return
	}

	cacheKey := owner + "/" + repo
	badgeCacheMu.Lock()
	count, ok := badgeCache[cacheKey]
	cachedAt := badgeCacheAt[cacheKey]
	badgeCacheMu.Unlock()

	if !ok || time.Since(cachedAt) > badgeCacheTTL {
		dbOk := false
		if dbClient != nil {
			if n, err := dbClient.GetPRCountByRepo(c.Request.Context(), owner, repo); err == nil {
				count = n
				dbOk = true
			}
		}
		// Solo actualizar el cache si la DB respondió correctamente,
		// o si ya había un valor previo (refresco de TTL con valor conocido).
		if dbOk || ok {
			badgeCacheMu.Lock()
			badgeCache[cacheKey] = count
			badgeCacheAt[cacheKey] = time.Now()
			badgeCacheMu.Unlock()
		}
	}

	label := "Anonymous PRs"
	value := fmt.Sprintf("%d", count)

	// Ancho dinámico: label ~100px + value ~(len*7+16)px
	valueWidth := len(value)*7 + 16
	if valueWidth < 30 {
		valueWidth = 30
	}
	totalWidth := 100 + valueWidth
	labelMid := 50
	valueMid := 100 + valueWidth/2

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s" viewBox="0 0 %d 20">
  <title>%s: %s</title>
  <linearGradient id="s" x2="0" y2="100%%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <clipPath id="r">
    <rect width="%d" height="20" rx="3" fill="#fff"/>
  </clipPath>
  <g clip-path="url(#r)">
    <rect width="100" height="20" fill="#555"/>
    <rect x="100" width="%d" height="20" fill="#4CAF50"/>
    <rect width="%d" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="110">
    <text x="%d" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="860" lengthAdjust="spacing">%s</text>
    <text x="%d" y="140" transform="scale(.1)" textLength="860" lengthAdjust="spacing">%s</text>
    <text x="%d" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
    <text x="%d" y="140" transform="scale(.1)" textLength="%d" lengthAdjust="spacing">%s</text>
  </g>
</svg>`,
		totalWidth, label, value, totalWidth,
		label, value,
		totalWidth,
		valueWidth,
		totalWidth,
		labelMid, label,
		labelMid, label,
		valueMid, (valueWidth - 16), value,
		valueMid, (valueWidth - 16), value,
	)

	c.Header("Content-Type", "image/svg+xml")
	c.Header("Cache-Control", "public, max-age=300")
	c.String(http.StatusOK, svg)
}

// PRStatusHandler devuelve el topic ntfy y la URL de suscripción para un PR hash dado.
// No almacena ni expone datos personales.
func PRStatusHandler(c *gin.Context) {
	hash := strings.TrimSpace(c.Param("hash"))
	if hash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hash is required"})
		return
	}

	topic := github.NtfyTopicForPR(hash)
	subscribeURL := fmt.Sprintf("%s/%s", github.NtfyBaseURL(), topic)

	c.JSON(http.StatusOK, gin.H{
		"hash":          hash,
		"ntfy_topic":    topic,
		"subscribe_url": subscribeURL,
	})
}

// VerifyHandler sirve instrucciones de verificación matemática del binario desplegado.
// Solo expone commitHash y sourceRepo (datos públicos, sin variables de entorno ni secretos).
func VerifyHandler(c *gin.Context) {
	shortHash := commitHash
	if len(shortHash) > 7 {
		shortHash = shortHash[:7]
	}

	baseURL := fmt.Sprintf("%s://%s", getScheme(c.Request), c.Request.Host)

	// URLs de verificación externas — independientes del operador
	repoSlug := "livrasand/gitGost"
	githubCommitURL := fmt.Sprintf("https://github.com/%s/commit/%s", repoSlug, commitHash)
	githubAttestURL := fmt.Sprintf("https://github.com/%s/attestations", repoSlug)
	sigstoreSearchURL := fmt.Sprintf("https://search.sigstore.dev/?hash=%s", commitHash)
	rekorSearchURL := fmt.Sprintf("https://rekor.sigstore.dev/api/v1/log/entries?logIndex=0&limit=1&search=%s", commitHash)

	body := fmt.Sprintf(`# gitGost Verification

## Currently Deployed Commit

%s

Full source: %s/health

## Independent Third-Party Verification (no trust in operator required)

The CI pipeline signs every build via Sigstore (Rekor transparency log).
These records are IMMUTABLE and controlled by neither gitGost nor Leapcell.

### 1. GitHub Attestations (easiest)

Every build on main generates a cryptographic attestation via actions/attest-build-provenance.
The attestation is anchored in Sigstore's public transparency log.

`+"```bash"+`
# Requires GitHub CLI (gh)
curl -o gitgost-server %s/gitgost-bin
gh attestation verify gitgost-server --repo %s
# Expected: ✓ Verification succeeded
`+"```"+`

Browse all attestations: %s

### 2. Sigstore / Rekor Transparency Log (independent)

The build provenance is recorded in Rekor, a public append-only log auditable by anyone.
No operator action can remove or alter it.

Search for this commit's entry:
  %s

Rekor API (raw):
  %s

### 3. Source Code Verification (always available)

Confirm that the deployed commit exists and is public on GitHub:

`+"```bash"+`
# 1. Get the deployed commit
curl %s/health
# → {"deployedCommit": "%s", ...}

# 2. Verify the commit exists in the public repo
# Visit: %s
`+"```"+`

If the commit exists on GitHub → the running code is 100%% auditable.

### 4. Local Binary Rebuild (deepest verification)

Reproduce the exact binary with the same environment used in CI (Linux amd64, CGO disabled):

`+"```bash"+`
# Requires Docker
git clone %s
cd gitGost
git checkout %s

docker run --rm \
  -v "$(pwd)":/src \
  -w /src \
  -e CGO_ENABLED=0 \
  -e GOOS=linux \
  -e GOARCH=amd64 \
  golang:alpine \
  go build -trimpath \
    -ldflags="-s -w -X 'github.com/livrasand/gitGost/internal/http.commitHash=%s'" \
    -o gitgost-local ./cmd/server

curl -o gitgost-server %s/gitgost-bin
sha256sum gitgost-local gitgost-server
# Hashes must be identical
`+"```"+`

Note: -trimpath and identical ldflags are required for reproducibility.
Compiling on macOS produces a different binary due to OS/arch differences.

## Known Limitation

Binary verification confirms the binary on disk matches the source.
It cannot cryptographically prove the running process in Leapcell's environment
has not been patched in memory. This is an inherent limit of any hosted service.
If this threat model is unacceptable, self-host gitGost: it is fully open source.

## Complete Source Code

%s

## Security

This endpoint exposes only public data: commit hash and repository URL.
It does not expose environment variables, tokens, keys, or internal configuration.
`,
		commitHash, baseURL,
		baseURL, repoSlug, githubAttestURL,
		sigstoreSearchURL, rekorSearchURL,
		baseURL, commitHash, githubCommitURL,
		sourceRepo, shortHash, commitHash, baseURL,
		sourceRepo)

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, body)
}

// BinaryHandler sirve el binario compilado del servidor para verificación.
// Usa /proc/self/exe para leer el ejecutable actual del proceso, sin depender
// de rutas hardcodeadas ni almacenamiento externo (compatible con Leapcell).
func BinaryHandler(c *gin.Context) {
	// /proc/self/exe apunta al ejecutable en curso en cualquier Linux
	exePath, err := os.Readlink("/proc/self/exe")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "binary not accessible on this platform"})
		return
	}

	f, err := os.Open(exePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not open binary"})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not stat binary"})
		return
	}

	c.Header("Content-Disposition", "attachment; filename=\"gitgost\"")
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", fmt.Sprintf("%d", info.Size()))
	c.Header("X-Deployed-Commit", commitHash)
	c.Header("X-Source-Repo", sourceRepo)
	c.Status(http.StatusOK)
	io.Copy(c.Writer, f)
}
