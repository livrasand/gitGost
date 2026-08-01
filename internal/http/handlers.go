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
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/livrasand/gitGost/internal/database"
	"github.com/livrasand/gitGost/internal/git"
	"github.com/livrasand/gitGost/internal/github"
	"github.com/livrasand/gitGost/internal/provider"
	cbprovider "github.com/livrasand/gitGost/internal/provider/codeberg"
	ghprovider "github.com/livrasand/gitGost/internal/provider/github"
	glprovider "github.com/livrasand/gitGost/internal/provider/gitlab"
	"github.com/livrasand/gitGost/internal/utils"

	"github.com/gin-gonic/gin"
)

var uploadPackClient = &http.Client{Timeout: 10 * time.Minute}

const (
	karmaStoreMax           = 10000
	reportStoreMax          = 10000
	blockedStoreMax         = 10000
	flaggedStoreMax         = 10000
	badgeCacheMax           = 10000
	ethicalStoreMax         = 100000
	rateLimitStoreMax       = 10000
	reportRateLimitStoreMax = 10000
	actionTokenMax          = 10000
	reportTokenMax          = 10000
)

type boundedEntry[V any] struct {
	value V
	at    time.Time
}

type boundedMap[V any] struct {
	mu      sync.Mutex
	data    map[string]boundedEntry[V]
	maxSize int
	ttl     time.Duration
}

func newBoundedMap[V any](maxSize int, ttl time.Duration) *boundedMap[V] {
	return &boundedMap[V]{
		data:    make(map[string]boundedEntry[V]),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

func (m *boundedMap[V]) Get(key string) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key]
	if !ok {
		var zero V
		return zero, false
	}
	if m.ttl > 0 && time.Since(e.at) > m.ttl {
		delete(m.data, key)
		var zero V
		return zero, false
	}
	e.at = time.Now()
	m.data[key] = e
	return e.value, true
}

func (m *boundedMap[V]) Peek(key string) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key]
	if !ok {
		var zero V
		return zero, false
	}
	if m.ttl > 0 && time.Since(e.at) > m.ttl {
		delete(m.data, key)
		var zero V
		return zero, false
	}
	return e.value, true
}

func (m *boundedMap[V]) Set(key string, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	_, exists := m.data[key]
	needed := 1
	if exists {
		needed = 0
	}
	m.evictOldestLocked(needed)
	m.data[key] = boundedEntry[V]{value: value, at: now}
}

func (m *boundedMap[V]) Update(key string, fn func(V, bool) V) V {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	old, ok := m.data[key]
	present := false
	var oldVal V
	if ok {
		if m.ttl > 0 && now.Sub(old.at) > m.ttl {
			delete(m.data, key)
		} else {
			present = true
			oldVal = old.value
		}
	}
	needed := 1
	if present {
		needed = 0
	}
	m.evictOldestLocked(needed)
	newVal := fn(oldVal, present)
	m.data[key] = boundedEntry[V]{value: newVal, at: now}
	return newVal
}

func (m *boundedMap[V]) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
}

func (m *boundedMap[V]) Range(fn func(key string, value V) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.data {
		if m.ttl > 0 && time.Since(e.at) > m.ttl {
			continue
		}
		if !fn(k, e.value) {
			break
		}
	}
}

func (m *boundedMap[V]) evictOldestLocked(needed int) {
	if m.maxSize <= 0 {
		return
	}
	for len(m.data)+needed > m.maxSize {
		var oldestKey string
		var oldestAt time.Time
		first := true
		for k, e := range m.data {
			if first || e.at.Before(oldestAt) {
				oldestKey = k
				oldestAt = e.at
				first = false
			}
		}
		if oldestKey == "" {
			break
		}
		delete(m.data, oldestKey)
	}
}

func windowAdd(store *boundedMap[[]time.Time], ip string, now time.Time, window time.Duration, max int) int {
	count := store.Update(ip, func(times []time.Time, ok bool) []time.Time {
		cutoff := now.Add(-window)
		valid := make([]time.Time, 0, max+1)
		for _, t := range times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		valid = append(valid, now)
		if len(valid) > max+1 {
			n := max + 1
			tmp := make([]time.Time, n)
			copy(tmp, valid[len(valid)-n:])
			valid = tmp
		}
		return valid
	})
	return len(count)
}

type reportState struct {
	Count int
	First time.Time
	IPs   map[string]time.Time
}

func providerFromPath(path string) provider.Provider {
	if strings.HasPrefix(path, "/v1/gl/") {
		return glprovider.New()
	}
	if strings.HasPrefix(path, "/v1/cb/") {
		return cbprovider.New()
	}
	return ghprovider.New()
}

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

func WritePktLine(w io.Writer, data string) error {
	if data == "" {
		_, err := w.Write([]byte("0000"))
		return err
	}

	length := len(data) + 4
	_, err := fmt.Fprintf(w, "%04x%s", length, data)
	return err
}

func WriteSidebandLine(w io.Writer, band byte, message string) error {
	if message == "" {
		return nil
	}
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}

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

	prov := providerFromPath(c.Request.URL.Path)
	refs, err := prov.GetRefs(owner, repo)
	if err != nil {
		utils.Log("Error getting refs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get refs"})
		return
	}

	var advertisement bytes.Buffer

	serviceLine := "# service=git-receive-pack\n"
	WritePktLine(&advertisement, serviceLine)
	WritePktLine(&advertisement, "")

	capabilities := "report-status delete-refs side-band-64k quiet ofs-delta push-options"

	first := true
	for _, ref := range refs {
		if strings.HasPrefix(ref.Ref, "refs/heads/") || strings.HasPrefix(ref.Ref, "refs/tags/") {
			line := fmt.Sprintf("%s %s", ref.SHA, ref.Ref)
			if first {
				line += "\x00" + capabilities
				first = false
			}
			line += "\n"
			WritePktLine(&advertisement, line)
		}
	}

	if first {
		line := fmt.Sprintf("0000000000000000000000000000000000000000 capabilities^{}\x00%s\n", capabilities)
		WritePktLine(&advertisement, line)
	}

	WritePktLine(&advertisement, "")

	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write(advertisement.Bytes())
}

func ReceivePackHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	fmt.Printf("DEBUG: ReceivePackHandler called for %s/%s\n", owner, repo)

	if c.GetHeader("Expect") == "100-continue" {
		c.Writer.WriteHeader(http.StatusContinue)
	}

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

	go recordGlobalBurst(ip)

	prov := providerFromPath(c.Request.URL.Path)

	policy, err := prov.GetRepoPolicy(owner, repo)
	if err == nil && policy != nil && policy.DenyAll {
		c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		c.Writer.WriteHeader(http.StatusOK)
		var errResp bytes.Buffer
		WriteSidebandLine(&errResp, 2, "remote: ")
		WriteSidebandLine(&errResp, 2, "remote: CONTRIBUTION BLOCKED")
		WriteSidebandLine(&errResp, 2, "remote: ")
		WriteSidebandLine(&errResp, 2, "remote: This repository does not accept anonymous contributions")
		WriteSidebandLine(&errResp, 2, "remote: via gitGost. Please contact the maintainer directly.")
		WriteSidebandLine(&errResp, 2, "remote: ")
		WriteSidebandLine(&errResp, 3, "push rejected: repository has opted out of gitGost")
		WritePktLine(&errResp, "")
		c.Writer.Write(errResp.Bytes())
		return
	}

	utils.Log("Content-Type: %s", c.GetHeader("Content-Type"))
	utils.Log("Content-Length: %s", c.GetHeader("Content-Length"))

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPushSize)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		utils.Log("Error reading body: %v", err)
		sendErrorResponse(c, "error reading body")
		return
	}

	utils.Log("Received push for %s/%s, size: %d bytes", owner, repo, len(body))

	tempDir, err := utils.CreateTempDir()
	if err != nil {
		utils.Log("Error creating temp dir: %v", err)
		sendErrorResponse(c, fmt.Sprintf("error creating temp dir: %v", err))
		return
	}
	defer utils.CleanupTempDir(tempDir)

	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	c.Writer.WriteHeader(http.StatusOK)

	var response bytes.Buffer

	WriteSidebandLine(&response, 2, "remote: gitGost: Processing your anonymous contribution...")

	newSHA, commitMessage, receivedPRHash, err := git.ReceivePack(tempDir, body, owner, repo, prov.CloneURL(owner, repo), prov.TokenEnvVar())
	if err != nil {
		utils.Log("Error receiving pack: %v", err)
		WriteSidebandLine(&response, 3, fmt.Sprintf("unpack error: %v", err))
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Commits received successfully, HEAD at: %s", newSHA)
	WriteSidebandLine(&response, 2, "remote: gitGost: Commits anonymized successfully")

	WriteSidebandLine(&response, 2, "remote: gitGost: Creating fork...")
	forkOwner, err := prov.ForkRepo(owner, repo)
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
		branchFromHash := fmt.Sprintf("gitgost-%s", receivedPRHash)
		WriteSidebandLine(&response, 2, fmt.Sprintf("remote: gitGost: Updating existing PR (hash: %s)...", receivedPRHash))

		existingPRURL, branchExists, err := prov.GetExistingMR(owner, repo, forkOwner, branchFromHash)
		if err != nil {
			utils.Log("Error checking existing PR: %v", err)
		}

		if branchExists {
			WriteSidebandLine(&response, 2, "remote: gitGost: Pushing update to existing branch...")
			branch, err = git.PushToGitHub(owner, repo, tempDir, forkOwner, branchFromHash, prov.PushURL(forkOwner, repo), prov.TokenEnvVar())
			if err != nil {
				utils.Log("Error pushing update to fork: %v", err)
				WriteSidebandLine(&response, 1, "unpack ok\n")
				WriteSidebandLine(&response, 3, fmt.Sprintf("error pushing update: %v", err))
				WritePktLine(&response, "")
				c.Writer.Write(response.Bytes())
				return
			}
			if existingPRURL != "" {
				prURL = existingPRURL
				isUpdate = true
				utils.Log("Updated existing branch: %s, PR: %s", branch, prURL)
			} else {
				WriteSidebandLine(&response, 2, "remote: gitGost: PR was closed, creating new PR on existing branch...")
				prURL, err = prov.CreateMR(owner, repo, branch, forkOwner, commitMessage)
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
			utils.Log("PR hash not found, creating new PR")
			WriteSidebandLine(&response, 2, "remote: gitGost: Hash not found, creating new PR...")
		}
	}

	if !isUpdate {
		WriteSidebandLine(&response, 2, "remote: gitGost: Pushing to fork...")
		branch, err = git.PushToGitHub(owner, repo, tempDir, forkOwner, "", prov.PushURL(forkOwner, repo), prov.TokenEnvVar())
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

		WriteSidebandLine(&response, 2, "remote: gitGost: Creating pull request...")
		prURL, err = prov.CreateMR(owner, repo, branch, forkOwner, commitMessage)
		if err != nil {
			utils.Log("Error creating PR: %v", err)
			WriteSidebandLine(&response, 1, "unpack ok\n")
			WriteSidebandLine(&response, 3, fmt.Sprintf("error creating PR: %v", err))
			WritePktLine(&response, "")
			c.Writer.Write(response.Bytes())
			return
		}

		utils.Log("Created PR: %s", prURL)

		if err := RecordPR(c.Request.Context(), owner, repo, prURL); err != nil {
			utils.Log("Error recording stats: %v", err)
		}
	}

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

	outPRHash := github.GeneratePRHash(owner, repo, branch)

	go func() {
		ntfyTopic := github.NtfyTopicForPR(outPRHash)
		var ntfyTitle, ntfyMsg string
		actionBtn := fmt.Sprintf("http, Check Status, %s/api/pr/%s/status, clear=true, method=GET", github.NtfyServiceURL(), outPRHash)
		if isUpdate {
			ntfyTitle = "PR Updated · gitGost"
			ntfyMsg = fmt.Sprintf("Your anonymous PR was updated.\nPR: %s\nTopic: %s/%s", prURL, github.NtfyBaseURL(), ntfyTopic)
		} else {
			ntfyTitle = "PR Created · gitGost"
			ntfyMsg = fmt.Sprintf("Your anonymous PR was created.\nPR: %s\nTopic: %s/%s", prURL, github.NtfyBaseURL(), ntfyTopic)
		}
		if err := github.PublishNtfyEvent(outPRHash, ntfyTitle, ntfyMsg, actionBtn); err != nil {
			utils.Log("ntfy publish error for hash %s: %v", outPRHash, err)
		}
	}()

	if prURL != "" {
		provShort := "gh"
		if strings.HasPrefix(c.Request.URL.Path, "/v1/gl/") {
			provShort = "gl"
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/cb/") {
			provShort = "cb"
		}
		switch provShort {
		case "gl":
			if num := glprovider.ExtractMRIID(prURL); num > 0 {
				trackPR(outPRHash, owner, repo, num, prURL, provShort)
			}
		case "cb":
			if num := cbprovider.ExtractPRNumber(prURL); num > 0 {
				trackPR(outPRHash, owner, repo, num, prURL, provShort)
			}
		default:
			if num := github.ExtractPRNumber(prURL); num > 0 {
				trackPR(outPRHash, owner, repo, num, prURL, provShort)
			}
		}
	}

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

	WriteSidebandLine(&response, 1, "unpack ok\n")
	WriteSidebandLine(&response, 1, "ok refs/heads/main\n")
	WritePktLine(&response, "")

	c.Writer.Write(response.Bytes())
	c.Writer.Flush()

	time.Sleep(100 * time.Millisecond)
}

func UploadPackDiscoveryHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	prov := providerFromPath(c.Request.URL.Path)
	token := os.Getenv(prov.TokenEnvVar())

	q := url.Values{}
	q.Set("service", "git-upload-pack")
	for k, vals := range c.Request.URL.Query() {
		if k == "service" {
			continue
		}
		for _, v := range vals {
			q.Add(k, v)
		}
	}
	remoteURL := prov.CloneURL(owner, repo) + "/info/refs?" + q.Encode()
	req, err := http.NewRequest("GET", remoteURL, nil)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}
	req.Header.Set("User-Agent", "git/2.0")
	if token != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth("x-access-token", token))
	}

	resp, err := uploadPackClient.Do(req)
	if err != nil {
		utils.Log("UploadPackDiscovery error: %v", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "failed to reach remote"})
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

	const maxUploadBytes = 50 * 1024 * 1024
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

	prov := providerFromPath(c.Request.URL.Path)
	token := os.Getenv(prov.TokenEnvVar())

	remoteURL := prov.CloneURL(owner, repo) + "/git-upload-pack"
	req, err := http.NewRequest("POST", remoteURL, bytes.NewReader(body))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	req.Header.Set("Accept", "application/x-git-upload-pack-result")
	req.Header.Set("User-Agent", "git/2.0")
	if gp := c.Request.Header.Get("Git-Protocol"); gp != "" {
		req.Header.Set("Git-Protocol", gp)
	}
	if ce := c.Request.Header.Get("Content-Encoding"); ce != "" {
		req.Header.Set("Content-Encoding", ce)
	}
	if token != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth("x-access-token", token))
	}

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

func SetBuildInfo(hash, built, repo string) {
	commitHash = hash
	buildTime = built
	sourceRepo = repo
}

var (
	startTime           = time.Now()
	dbClient            *database.SupabaseClient
	dbOnce              sync.Once
	secretKey           []byte
	identityMu          sync.Mutex
	karmaStore          = newBoundedMap[int](karmaStoreMax, 24*time.Hour)
	reportStore         = newBoundedMap[reportState](reportStoreMax, reportWindow)
	flaggedStore        = newBoundedMap[time.Time](flaggedStoreMax, flaggedCooldown)
	blockedStore        = newBoundedMap[bool](blockedStoreMax, 0)
	panicMode           bool
	panicMu             sync.Mutex
	panicPassword       string
	ntfyAdminTopic      string
	mentaAPIEndpoint    string
	mentaAPIKey         string
	rateLimitStore      = newBoundedMap[[]time.Time](rateLimitStoreMax, rateLimitWindow)
	rateLimitWindow     = time.Hour
	rateLimitMaxPRs     = 5
	globalBurstMu       sync.Mutex
	globalBurstTimes    []time.Time
	globalBurstIPs      []string
	globalBurstWindow   = 60 * time.Second
	globalBurstMaxTotal = 20
	globalBurstMaxIPs   = 10
	globalBurstAlerted  bool
	recentBurstPRsMu    sync.Mutex
	recentBurstPRs      []string
	recentBurstPRsAt    []time.Time
	recentBurstPRsTTL   = 2 * time.Hour

	actionTokens   = newBoundedMap[time.Time](actionTokenMax, actionTokenTTL)
	actionTokenTTL = 10 * time.Minute

	rollbackLimitMu    sync.Mutex
	rollbackLimitTimes []time.Time
	rollbackLimitMax   = 5
	rollbackLimitWin   = time.Minute

	reportRateLimitStore  = newBoundedMap[[]time.Time](reportRateLimitStoreMax, reportRateLimitWindow)
	reportRateLimitWindow = time.Hour
	reportRateLimitMax    = 5

	reportTokens   = newBoundedMap[time.Time](reportTokenMax, reportTokenTTL)
	reportTokenTTL = 10 * time.Minute

	reportFormTmpl   = template.Must(template.New("reportForm").Parse(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8" /><script src="https://mentacaptchaeu.eu.pythonanywhere.com/menta-captcha.js"></script><title>Report content · gitGost</title><style>body{font-family:Inter,system-ui,-apple-system,Segoe UI,sans-serif;background:#0d1117;color:#c9d1d9;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:32px;} .shell{background:linear-gradient(145deg, rgba(255,166,87,0.16), rgba(255,107,107,0.14));border:1px solid rgba(255,166,87,0.45);border-radius:16px;padding:1.5px;box-shadow:0 16px 38px rgba(0,0,0,.42);max-width:620px;width:100%;} .card{background:#0d1117;border-radius:14px;padding:26px;border:1px solid rgba(255,255,255,0.05);} h1{margin:0 0 6px;font-size:24px;color:#ffa657;} .eyebrow{display:inline-flex;align-items:center;gap:.35rem;padding:.35rem .75rem;background:rgba(255,166,87,0.12);color:#ffa657;border:1px solid rgba(255,166,87,0.4);border-radius:999px;font-family:'IBM Plex Mono', monospace;font-size:.85rem;margin-bottom:5px;} .sub{margin:6px 0 14px;color:#9fb3ff;font-size:14px;} .policy{background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.05);border-radius:12px;padding:14px;margin:14px 0;font-size:13px;line-height:1.55;} .policy strong{color:#ffa657;} label{display:block;font-weight:700;margin:12px 0 6px;letter-spacing:.01em;} .readonly{background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.08);border-radius:10px;padding:12px;color:#c9d1d9;font-family:'IBM Plex Mono', monospace;} button{margin-top:14px;width:100%;padding:12px;border-radius:10px;border:none;background:linear-gradient(135deg,#ffa657,#ff6b6b);color:#0d1117;font-weight:700;font-size:15px;cursor:pointer;box-shadow:0 10px 30px rgba(0,0,0,0.25);} .note{margin-top:10px;font-size:12px;color:#9fb3ff;} .error{color:#ffb4c4;font-size:13px;margin-top:10px;} .count{display:flex;gap:8px;align-items:center;margin:10px 0;font-family:'IBM Plex Mono', monospace;} .pill{padding:6px 10px;border-radius:999px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.04);} .pill strong{color:#ffa657;} .state{margin-left:auto;font-size:12px;color:#9fb3ff;} .legend{font-size:12px;color:#9fb3ff;margin-top:10px;} input[type=text]{width:100%;padding:12px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.04);color:#c9d1d9;} form{margin-top:12px;} a{color:#9fb3ff;} .locked{opacity:.55;pointer-events:none;} </style></head><body><div class="shell"><div class="card"><div class="eyebrow">Anonymous moderation</div><h1>Report content</h1><div class="sub">Flag abuse from anonymous contributions.</div><div class="policy"><ul style="margin:0 0 6px 18px; padding:0 0 0 4px; line-height:1.6;">` + string(reportPolicyHTML) + `</ul><div class="note">Reports reset after 30 days.</div></div><form method="POST" action="/v1/moderation/report" onsubmit="const t=document.getElementById('menta-report')?.token; document.getElementById('report-captcha-token').value=t||''"><label for="hash">Hash</label><input type="text" id="hash" name="hash" value="{{.Hash}}" placeholder="goster-xxxxx" {{if eq .State "blocked"}}class="locked" readonly{{end}} /><div class="count"><div class="pill">Reports: <strong>{{.Reports}}</strong></div><div class="state">State: {{.State}}</div></div><input type="hidden" name="report_token" value="{{.ReportToken}}" /><input type="hidden" name="captcha_token" id="report-captcha-token" /><div class="note">Please complete the CAPTCHA below.</div><menta-widget id="menta-report" data-cap-api-endpoint="https://mentacaptchaeu.eu.pythonanywhere.com" data-cap-i18n-initial-state="I'm not a robot"></menta-widget><button type="submit" {{if eq .State "blocked"}}disabled class="locked"{{end}}>Submit report</button></form><div class="legend">Hash identifies the anonymous submitter. No personal data is collected.</div>{{if .Error}}<div class="error">{{.Error}}</div>{{end}}</div></div></body></html>`))
	reportThanksTmpl = template.Must(template.New("reportThanks").Parse(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8" /><title>Report received · gitGost</title><style>body{font-family:Inter,system-ui,-apple-system,Segoe UI,sans-serif;background:#0d1117;color:#c9d1d9;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:32px;} .shell{background:linear-gradient(145deg, rgba(255,166,87,0.16), rgba(255,107,107,0.14));border:1px solid rgba(255,166,87,0.45);border-radius:16px;padding:1.5px;box-shadow:0 16px 38px rgba(0,0,0,.42);max-width:620px;width:100%;} .card{background:#0d1117;border-radius:14px;padding:26px;border:1px solid rgba(255,255,255,0.05);} h1{margin:0 0 10px;font-size:24px;color:#ffa657;} p{margin:6px 0 0;color:#9fb3ff;} .pill{display:inline-block;margin-top:12px;padding:8px 12px;border-radius:999px;background:rgba(255,255,255,0.04);color:#ffa657;font-weight:700;border:1px solid rgba(255,255,255,0.08);} .cta{margin-top:16px;display:inline-block;padding:12px 16px;border-radius:10px;background:linear-gradient(135deg,#ffa657,#ff6b6b);color:#0d1117;font-weight:700;text-decoration:none;box-shadow:0 10px 30px rgba(0,0,0,0.25);} .small{margin-top:12px;font-size:12px;color:#9fb3ff;} .state{margin-top:10px;font-size:14px;} </style></head><body><div class="shell"><div class="card"><h1>Report received</h1><p>Hash: <strong>{{.Hash}}</strong></p><span class="pill">Total reports: {{.Reports}}</span><div class="state">State: {{.State}}</div><p class="small">Thanks for helping moderate. Your identity stays anonymous.</p><a class="cta" href="https://gitgost.fly.dev/" target="_blank" rel="noreferrer">Explore gitGost</a></div></div></body></html>`))
)

type anonymousIssueRequest struct {
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	Labels       []string `json:"labels"`
	CaptchaToken string   `json:"captcha_token"`
}

type anonymousCommentRequest struct {
	UserToken    string `json:"user_token"`
	Body         string `json:"body"`
	CaptchaToken string `json:"captcha_token"`
}

const (
	reportWindow    = 30 * 24 * time.Hour
	flaggedCooldown = 6 * time.Hour
)

var reportPolicyHTML = template.HTML(`<li><strong>0–2 reports:</strong> internal log only.</li><li><strong>3–5 reports:</strong> hash flagged, 6h cooldown, karma reset.</li><li><strong>6+ reports:</strong> hash blocked; we attempt to remove its comments.</li>`)

type prTrack struct {
	Owner    string
	Repo     string
	Number   int
	PRURL    string
	Provider string
	LastETag string
	AddedAt  time.Time
}

var (
	prTrackMu           sync.RWMutex
	prTrackStore        = make(map[string]*prTrack)
	prTrackTTL          = 24 * time.Hour
	prTrackEvictionOnce sync.Once
)

func startPRTrackEviction() {
	prTrackEvictionOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				prTrackMu.Lock()
				for hash, t := range prTrackStore {
					if time.Since(t.AddedAt) > prTrackTTL {
						delete(prTrackStore, hash)
					}
				}
				prTrackMu.Unlock()
			}
		}()
	})
}

func trackPR(prHash, owner, repo string, number int, prURL, provider string) {
	startPRTrackEviction()
	prTrackMu.Lock()
	defer prTrackMu.Unlock()
	prTrackStore[prHash] = &prTrack{
		Owner:    owner,
		Repo:     repo,
		Number:   number,
		PRURL:    prURL,
		Provider: provider,
		AddedAt:  time.Now(),
	}
}

func getPRTrack(prHash string) (prTrack, bool) {
	prTrackMu.Lock()
	defer prTrackMu.Unlock()
	t, ok := prTrackStore[prHash]
	if !ok {
		return prTrack{}, false
	}
	if time.Since(t.AddedAt) > prTrackTTL {
		delete(prTrackStore, prHash)
		return prTrack{}, false
	}
	return *t, true
}

func providerFromName(name string) provider.Provider {
	switch name {
	case "gl":
		return glprovider.New()
	case "cb":
		return cbprovider.New()
	default:
		return ghprovider.New()
	}
}

func newActionToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	token := hex.EncodeToString(b)
	actionTokens.Set(token, time.Now().Add(actionTokenTTL))
	return token
}

func consumeActionToken(token string) bool {
	expiry, ok := actionTokens.Get(token)
	if !ok {
		return false
	}
	actionTokens.Delete(token)
	return time.Now().Before(expiry)
}

func InitPanicConfig(password, adminTopic string) {
	panicPassword = password
	ntfyAdminTopic = adminTopic
}

func InitMentaConfig(apiEndpoint, apiKey string) {
	mentaAPIEndpoint = strings.TrimRight(apiEndpoint, "/")
	mentaAPIKey = apiKey
}

func verifyMentaCaptcha(token string) bool {
	if mentaAPIEndpoint == "" {
		return true
	}
	if strings.TrimSpace(token) == "" {
		return false
	}
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, mentaAPIEndpoint+"/verify", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if mentaAPIKey != "" {
		req.Header.Set("X-API-Key", mentaAPIKey)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("Menta verify request failed: %v", err)
		return false
	}
	defer resp.Body.Close()

	var result struct {
		Valid bool `json:"valid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	return result.Valid
}

func isPanicMode() bool {
	panicMu.Lock()
	defer panicMu.Unlock()
	return panicMode
}

func isGlobalBurstAlertActive() bool {
	globalBurstMu.Lock()
	defer globalBurstMu.Unlock()
	return globalBurstAlerted
}

func recordGlobalBurst(ip string) {
	now := time.Now()
	globalBurstMu.Lock()
	defer globalBurstMu.Unlock()

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

	seen := make(map[string]struct{}, total)
	for _, bip := range globalBurstIPs {
		seen[bip] = struct{}{}
	}
	distinctIPs := len(seen)

	if !globalBurstAlerted && (total >= globalBurstMaxTotal || distinctIPs >= globalBurstMaxIPs) {
		globalBurstAlerted = true
		go notifyAdminGlobalBurst(total, distinctIPs)
	}

	if globalBurstAlerted && total < globalBurstMaxTotal/2 && distinctIPs < globalBurstMaxIPs/2 {
		globalBurstAlerted = false
	}
}

func notifyAdminGlobalBurst(total, distinctIPs int) {
	if ntfyAdminTopic == "" {
		return
	}
	serviceURL := github.NtfyServiceURL()
	title := "Suspicious activity detected · gitGost"
	msg := fmt.Sprintf(
		"%d push attempts from %d distinct IPs in the last %s. This may indicate bot, script, or coordinated abuse.",
		total, distinctIPs, globalBurstWindow,
	)
	tokActivate := newActionToken()
	tokRollback := newActionToken()
	tokDeactivate := newActionToken()
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

func checkRateLimit(ip string) bool {
	count := windowAdd(rateLimitStore, ip, time.Now(), rateLimitWindow, rateLimitMaxPRs)
	if count > rateLimitMaxPRs {
		if count == rateLimitMaxPRs+1 {
			go notifyAdminRateLimit(ip, count)
		}
		return true
	}
	return false
}

func notifyAdminRateLimit(ip string, count int) {
	if ntfyAdminTopic == "" {
		return
	}
	serviceURL := github.NtfyServiceURL()
	title := "Rate limit exceeded · gitGost"
	msg := fmt.Sprintf("IP %s exceeded the limit of %d PRs/hour (attempts: %d).", ip, rateLimitMaxPRs, count)
	tokActivate := newActionToken()
	tokRollback := newActionToken()
	tokDeactivate := newActionToken()
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

func ServiceStatusHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"panic_mode": isPanicMode(),
	})
}

func RollbackBurstHandler(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
		Token    string `json:"token"`
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
			var closeErr error
			switch {
			case strings.Contains(u, "gitlab.com"):
				closeErr = glprovider.New().CloseMRByURL(u)
			case strings.Contains(u, "codeberg.org"):
				closeErr = cbprovider.New().CloseMRByURL(u)
			default:
				closeErr = github.ClosePRByURL(u)
			}
			if err := closeErr; err != nil {
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

func InitDatabase(url, key string) {
	dbOnce.Do(func() {
		dbClient = database.NewSupabaseClient(url, key)
	})
}

func RecordPR(ctx context.Context, owner, repo, prURL string) error {
	if dbClient == nil {
		return fmt.Errorf("database client not initialized")
	}
	return dbClient.InsertPR(ctx, owner, repo, prURL)
}

func StatsHandler(c *gin.Context) {
	if dbClient == nil {
		c.JSON(http.StatusOK, gin.H{"total_prs": 0})
		return
	}

	totalPRs, err := dbClient.GetTotalPRs(c.Request.Context())
	if err != nil {
		utils.Log("Error getting total PRs: %v", err)
		c.JSON(http.StatusOK, gin.H{"total_prs": 0})
		return
	}

	lastUpdated, err := dbClient.GetLatestPRCreatedAt(c.Request.Context())
	if err != nil {
		utils.Log("Error getting latest PR timestamp: %v", err)
		c.JSON(http.StatusOK, gin.H{"total_prs": totalPRs})
		return
	}

	totalComments, err := dbClient.GetTotalComments(c.Request.Context())
	if err != nil {
		utils.Log("Error getting total comments: %v", err)
		totalComments = 0
	}

	response := gin.H{
		"total_prs":      totalPRs,
		"total_comments": totalComments,
	}

	if lastUpdated != nil {
		response["last_updated"] = lastUpdated
	}

	c.JSON(http.StatusOK, response)
}

func RecentPRsHandler(c *gin.Context) {
	if dbClient == nil {
		c.JSON(http.StatusOK, gin.H{"prs": []database.PRRecord{}, "total": 0})
		return
	}

	prs, err := dbClient.GetRecentPRs(c.Request.Context(), 10)
	if err != nil {
		utils.Log("Error getting recent PRs: %v", err)
		c.JSON(http.StatusOK, gin.H{"prs": []database.PRRecord{}, "total": 0})
		return
	}

	totalPRs, err := dbClient.GetTotalPRs(c.Request.Context())
	if err != nil {
		utils.Log("Error getting total PRs: %v", err)
		c.JSON(http.StatusOK, gin.H{"prs": prs, "total": 0})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"prs":   prs,
		"total": totalPRs,
	})
}

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

	if !verifyMentaCaptcha(req.CaptchaToken) {
		c.JSON(http.StatusForbidden, gin.H{"error": "captcha verification failed"})
		return
	}

	prov := providerFromPath(c.Request.URL.Path)
	issueURL, issueNumber, err := prov.CreateAnonymousIssue(owner, repo, req.Title, req.Body, req.Labels)
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
		"appeal_token":      generateAppealToken(hash),
	}

	c.JSON(http.StatusOK, resp)
}

func GitLabIssueNotesProxyHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	number := c.Param("number")

	for _, r := range number {
		if r < '0' || r > '9' {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid issue number"})
			return
		}
	}

	glToken := os.Getenv("GITLAB_TOKEN")

	projectID := url.PathEscape(owner + "/" + repo)
	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/issues/%s/notes?per_page=100&sort=asc", projectID, number)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "proxy error"})
		return
	}
	if glToken != "" {
		req.Header.Set("PRIVATE-TOKEN", glToken)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gitlab unreachable"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", body)
}

func GitLabCommitCountHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	projectID := url.PathEscape(owner + "/" + repo)
	client := &http.Client{Timeout: 30 * time.Second}
	glToken := os.Getenv("GITLAB_TOKEN")

	glFetch := func(page int) (int, error) {
		pageURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/commits?per_page=100&page=%d",
			projectID, page)
		req, err := http.NewRequest("GET", pageURL, nil)
		if err != nil {
			return 0, err
		}
		if glToken != "" {
			req.Header.Set("PRIVATE-TOKEN", glToken)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()

		if page == 1 {
			if total := resp.Header.Get("X-Total"); total != "" {
				count, err := strconv.Atoi(total)
				if err == nil {
					return -count, nil
				}
			}
		}

		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("status %d", resp.StatusCode)
		}

		var items []struct{}
		if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
			return 0, err
		}
		return len(items), nil
	}

	n, err := glFetch(1)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"total": 0})
		return
	}
	if n < 0 {
		c.JSON(http.StatusOK, gin.H{"total": -n})
		return
	}

	if n < 100 {
		c.JSON(http.StatusOK, gin.H{"total": n})
		return
	}

	lo, hi := 2, 2
	for {
		n, err := glFetch(hi)
		if err != nil || n == 0 {
			break
		}
		if n < 100 {
			total := (hi-1)*100 + n
			c.JSON(http.StatusOK, gin.H{"total": total})
			return
		}
		lo = hi + 1
		hi *= 2
		if hi > 10000 {
			break
		}
	}

	lastNonEmpty := lo - 1
	firstEmpty := hi

	for lo < firstEmpty {
		mid := (lo + firstEmpty) / 2
		n, err := glFetch(mid)
		if err != nil {
			break
		}
		if n > 0 {
			lastNonEmpty = mid
			if n < 100 {
				total := (mid-1)*100 + n
				c.JSON(http.StatusOK, gin.H{"total": total})
				return
			}
			lo = mid + 1
		} else {
			firstEmpty = mid
		}
	}

	n, err = glFetch(lastNonEmpty)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"total": (lastNonEmpty-1)*100 + 100})
		return
	}
	total := (lastNonEmpty-1)*100 + n
	c.JSON(http.StatusOK, gin.H{"total": total})
}

func GitLabAvatarHandler(c *gin.Context) {
	email := c.Query("email")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email required"})
		return
	}

	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/users?email=%s", url.QueryEscape(email))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "proxy error"})
		return
	}

	glToken := os.Getenv("GITLAB_TOKEN")
	if glToken != "" {
		req.Header.Set("PRIVATE-TOKEN", glToken)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gitlab unreachable"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var users []struct {
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(body, &users); err != nil || len(users) == 0 {
		c.JSON(http.StatusOK, gin.H{"avatar_url": ""})
		return
	}

	c.JSON(http.StatusOK, gin.H{"avatar_url": users[0].AvatarURL})
}

func GitLabCommitsHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	projectID := url.PathEscape(owner + "/" + repo)
	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/commits?per_page=30", projectID)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "proxy error"})
		return
	}

	glToken := os.Getenv("GITLAB_TOKEN")
	if glToken != "" {
		req.Header.Set("PRIVATE-TOKEN", glToken)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "gitlab unreachable"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", body)
}

func GitLabCommitDetailHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	sha := c.Param("sha")

	if len(sha) < 6 || len(sha) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha"})
		return
	}

	projectID := url.PathEscape(owner + "/" + repo)

	glToken := os.Getenv("GITLAB_TOKEN")
	authHeader := func(req *http.Request) {
		if glToken != "" {
			req.Header.Set("PRIVATE-TOKEN", glToken)
		}
		req.Header.Set("Accept", "application/json")
	}

	client := &http.Client{Timeout: 10 * time.Second}

	type commitResult struct {
		data []byte
		ok   bool
	}

	chCommit := make(chan commitResult, 1)
	chDiff := make(chan commitResult, 1)

	go func() {
		req, _ := http.NewRequest("GET", fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/commits/%s", projectID, sha), nil)
		authHeader(req)
		resp, err := client.Do(req)
		if err != nil {
			chCommit <- commitResult{nil, false}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		chCommit <- commitResult{body, resp.StatusCode == http.StatusOK}
	}()

	go func() {
		req, _ := http.NewRequest("GET", fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/commits/%s/diff", projectID, sha), nil)
		authHeader(req)
		resp, err := client.Do(req)
		if err != nil {
			chDiff <- commitResult{nil, false}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		chDiff <- commitResult{body, resp.StatusCode == http.StatusOK}
	}()

	commitRes := <-chCommit
	if !commitRes.ok || commitRes.data == nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch commit"})
		return
	}

	diffRes := <-chDiff

	var commitData map[string]interface{}
	if err := json.Unmarshal(commitRes.data, &commitData); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid commit data"})
		return
	}

	if diffRes.ok && diffRes.data != nil {
		var diffData []interface{}
		if err := json.Unmarshal(diffRes.data, &diffData); err == nil {
			commitData["diff_files"] = diffData
		}
	}

	c.JSON(http.StatusOK, commitData)
}

func GitHubDiscussionsProxyHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	query := fmt.Sprintf(`{
			repository(owner: %q, name: %q) {
				discussions(first: 30, orderBy: {field: CREATED_AT, direction: DESC}) {
					totalCount
					nodes {
						number
						title
						createdAt
						author { login }
						category { name emoji }
						comments { totalCount }
					}
				}
			}
		}`, owner, repo)

	payload := map[string]string{"query": query}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal error"})
		return
	}

	req, err := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewBuffer(jsonBody))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "proxy error"})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	ghToken := os.Getenv("GITHUB_TOKEN")
	if ghToken != "" {
		req.Header.Set("Authorization", "bearer "+ghToken)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "github unreachable"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 403 {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":   "rate_limited",
			"message": "GitHub API rate limit reached for this server.",
		})
		return
	}

	if resp.StatusCode != 200 {
		c.Data(resp.StatusCode, "application/json", body)
		return
	}

	var ghResp struct {
		Data struct {
			Repository struct {
				Discussions struct {
					TotalCount int `json:"totalCount"`
					Nodes      []struct {
						Number    int    `json:"number"`
						Title     string `json:"title"`
						CreatedAt string `json:"createdAt"`
						Author    struct {
							Login string `json:"login"`
						} `json:"author"`
						Category struct {
							Name  string `json:"name"`
							Emoji string `json:"emoji"`
						} `json:"category"`
						Comments struct {
							TotalCount int `json:"totalCount"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"discussions"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &ghResp); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to parse GitHub response"})
		return
	}

	if len(ghResp.Errors) > 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": ghResp.Errors[0].Message})
		return
	}

	disc := ghResp.Data.Repository.Discussions
	c.JSON(http.StatusOK, gin.H{
		"totalCount": disc.TotalCount,
		"nodes":      disc.Nodes,
	})
}

func GitHubDiscussionDetailProxyHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	numberStr := c.Param("number")
	number, err := strconv.Atoi(numberStr)
	if err != nil || number <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid discussion number"})
		return
	}

	query := fmt.Sprintf(`{
		repository(owner: %q, name: %q) {
			discussion(number: %d) {
				number
				title
				body
				createdAt
				updatedAt
				author { login }
				category { name emoji }
				isAnswered
				comments(first: 100) {
					nodes {
						author { login }
						body
						createdAt
						isAnswer
					}
				}
			}
		}
	}`, owner, repo, number)

	payload := map[string]string{"query": query}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal error"})
		return
	}

	req, err := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewBuffer(jsonBody))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "proxy error"})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	ghToken := os.Getenv("GITHUB_TOKEN")
	if ghToken != "" {
		req.Header.Set("Authorization", "bearer "+ghToken)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "github unreachable"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 403 {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":   "rate_limited",
			"message": "GitHub API rate limit reached for this server.",
		})
		return
	}

	if resp.StatusCode != 200 {
		c.Data(resp.StatusCode, "application/json", body)
		return
	}

	var ghResp struct {
		Data struct {
			Repository struct {
				Discussion *struct {
					Number    int    `json:"number"`
					Title     string `json:"title"`
					Body      string `json:"body"`
					CreatedAt string `json:"createdAt"`
					UpdatedAt string `json:"updatedAt"`
					Author    struct {
						Login string `json:"login"`
					} `json:"author"`
					Category struct {
						Name  string `json:"name"`
						Emoji string `json:"emoji"`
					} `json:"category"`
					IsAnswered bool `json:"isAnswered"`
					Comments   struct {
						Nodes []struct {
							Author struct {
								Login string `json:"login"`
							} `json:"author"`
							Body      string `json:"body"`
							CreatedAt string `json:"createdAt"`
							IsAnswer  bool   `json:"isAnswer"`
						} `json:"nodes"`
					} `json:"comments"`
				} `json:"discussion"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &ghResp); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to parse GitHub response"})
		return
	}

	if len(ghResp.Errors) > 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": ghResp.Errors[0].Message})
		return
	}

	disc := ghResp.Data.Repository.Discussion
	if disc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "discussion not found"})
		return
	}

	c.JSON(http.StatusOK, disc)
}

func GitHubWikiProxyHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	page := c.Param("page")
	if page == "" {
		page = "Home"
	}

	pageURLs := []string{
		fmt.Sprintf("https://raw.githubusercontent.com/wiki/%s/%s/%s.md", owner, repo, page),
		fmt.Sprintf("https://raw.githubusercontent.com/wiki/%s/%s/%s", owner, repo, page),
	}

	client := &http.Client{Timeout: 15 * time.Second}
	for _, pageURL := range pageURLs {
		req, err := http.NewRequest("GET", pageURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "text/plain")
		req.Header.Set("User-Agent", "gitGost/1.0")

		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != 200 {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		c.Data(200, "text/plain; charset=utf-8", body)
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "wiki page not found"})
}

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

	if !verifyMentaCaptcha(req.CaptchaToken) {
		c.JSON(http.StatusForbidden, gin.H{"error": "captcha verification failed"})
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

	prov := providerFromPath(c.Request.URL.Path)
	commentURL, err := prov.CreateAnonymousComment(owner, repo, number, bodyWithLegend)
	if err != nil {
		utils.Log("Error creating comment: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if dbClient != nil {
		if err := dbClient.InsertComment(c.Request.Context(), owner, repo, commentURL); err != nil {
			utils.Log("Error recording comment in DB: %v", err)
		}
	}

	resp := gin.H{
		"comment_url":  commentURL,
		"hash":         hash,
		"karma":        karma,
		"user_token":   userToken,
		"appeal_token": generateAppealToken(hash),
	}

	c.JSON(http.StatusOK, resp)
}

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

	if !verifyMentaCaptcha(req.CaptchaToken) {
		c.JSON(http.StatusForbidden, gin.H{"error": "captcha verification failed"})
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

	prov := providerFromPath(c.Request.URL.Path)
	commentURL, err := prov.CreateAnonymousPRComment(owner, repo, number, bodyWithLegend)
	if err != nil {
		utils.Log("Error creating PR comment: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if dbClient != nil {
		if err := dbClient.InsertComment(c.Request.Context(), owner, repo, commentURL); err != nil {
			utils.Log("Error recording PR comment in DB: %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"comment_url":  commentURL,
		"hash":         hash,
		"karma":        karma,
		"user_token":   userToken,
		"appeal_token": generateAppealToken(hash),
	})
}

func CreateAnonymousDiscussionCommentHandler(c *gin.Context) {
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid discussion number"})
		return
	}

	if strings.TrimSpace(req.Body) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body is required"})
		return
	}

	if !verifyMentaCaptcha(req.CaptchaToken) {
		c.JSON(http.StatusForbidden, gin.H{"error": "captcha verification failed"})
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
	}
	reportURL := fmt.Sprintf("%s://%s/v1/moderation/report?hash=%s", getScheme(c.Request), c.Request.Host, hash)

	legend := fmt.Sprintf("\n\n---\ngoster-%s · karma (%d) · [report](%s)", hash, karma, reportURL)
	bodyWithLegend := req.Body + legend

	prov := providerFromPath(c.Request.URL.Path)
	commentURL, err := prov.CreateAnonymousDiscussionComment(owner, repo, number, bodyWithLegend)
	if err != nil {
		utils.Log("Error creating discussion comment: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if dbClient != nil {
		if err := dbClient.InsertComment(c.Request.Context(), owner, repo, commentURL); err != nil {
			utils.Log("Error recording discussion comment in DB: %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"comment_url":  commentURL,
		"hash":         hash,
		"karma":        karma,
		"user_token":   userToken,
		"appeal_token": generateAppealToken(hash),
	})
}

func renderReportForm(c *gin.Context, hash string, reports int, state, err, reportToken string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = reportFormTmpl.Execute(c.Writer, gin.H{
		"Hash":        hash,
		"Reports":     reports,
		"State":       state,
		"Error":       err,
		"PolicyHTML":  reportPolicyHTML,
		"ReportToken": reportToken,
	})
}

func checkReportRateLimit(ip string) bool {
	count := windowAdd(reportRateLimitStore, ip, time.Now(), reportRateLimitWindow, reportRateLimitMax)
	return count > reportRateLimitMax
}

func newReportToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	token := hex.EncodeToString(b)
	reportTokens.Set(token, time.Now().Add(reportTokenTTL))
	return token
}

func consumeReportToken(token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	expiry, ok := reportTokens.Get(token)
	if !ok {
		return false
	}
	reportTokens.Delete(token)
	return time.Now().Before(expiry)
}

func ReportHashHandler(c *gin.Context) {
	if c.Request.Method == http.MethodGet {
		hash := strings.TrimSpace(c.Query("hash"))
		if hash == "" {
			renderReportForm(c, "", 0, "sin datos", "El hash es obligatorio", newReportToken())
			return
		}
		if isBlockedHash(hash) {
			renderReportForm(c, hash, 6, "bloqueado", "Este hash ya fue baneado/eliminado.", newReportToken())
			return
		}
		reports := getReportCountWithWindow(c.Request.Context(), hash)
		renderReportForm(c, hash, reports, reportStateLabel(reports), "", newReportToken())
		return
	}

	hash := strings.TrimSpace(c.PostForm("hash"))
	if hash == "" {
		renderReportForm(c, "", 0, "sin datos", "El hash es obligatorio.", newReportToken())
		return
	}

	if isBlockedHash(hash) {
		renderReportForm(c, hash, 6, "bloqueado", "Este hash ya fue baneado/eliminado.", newReportToken())
		return
	}

	currentReports := getReportCountWithWindow(c.Request.Context(), hash)
	currentState := reportStateLabel(currentReports)
	ip := strings.TrimSpace(c.ClientIP())
	if checkReportRateLimit(ip) {
		renderReportForm(c, hash, currentReports, currentState, fmt.Sprintf("Rate limit exceeded: max %d reports per hour per IP.", reportRateLimitMax), newReportToken())
		return
	}

	captchaToken := strings.TrimSpace(c.PostForm("captcha_token"))
	if !verifyMentaCaptcha(captchaToken) {
		renderReportForm(c, hash, currentReports, currentState, "CAPTCHA verification failed.", newReportToken())
		return
	}

	reportToken := strings.TrimSpace(c.PostForm("report_token"))
	if !consumeReportToken(reportToken) {
		renderReportForm(c, hash, currentReports, currentState, "Invalid or expired report token. Please reload the page.", newReportToken())
		return
	}

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
		now := time.Now()
		state := reportStore.Update(hash, func(s reportState, ok bool) reportState {
			if !ok || time.Since(s.First) > reportWindow {
				if ip == "" {
					return reportState{Count: 1, First: now}
				}
				return reportState{Count: 1, First: now, IPs: map[string]time.Time{ip: now}}
			}
			if ip != "" {
				if s.IPs == nil {
					s.IPs = make(map[string]time.Time)
				}
				if t, ok := s.IPs[ip]; ok && time.Since(t) <= reportWindow {
					return s
				}
				s.IPs[ip] = now
			}
			s.Count++
			return s
		})
		reports = state.Count
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
			if s, ok := reportStore.Get(hash); ok && s.Count > count {
				return s.Count
			}
			return count
		}
	}

	if s, ok := reportStore.Get(hash); ok {
		return s.Count
	}
	return 0
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
	blockedStore.Set(hash, true)
}

func isBlockedHash(hash string) bool {
	if hash == "" {
		return false
	}
	blocked, _ := blockedStore.Peek(hash)
	return blocked
}

func isFlaggedCooldown(hash string) bool {
	if hash == "" {
		return false
	}
	last, ok := flaggedStore.Peek(hash)
	if !ok {
		return false
	}
	return time.Since(last) < flaggedCooldown
}

func markFlaggedAction(hash string) {
	if hash == "" {
		return
	}
	flaggedStore.Set(hash, time.Now())
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
	if hash == "" {
		return 0
	}
	if karma, ok := karmaStore.Get(hash); ok {
		return karma
	}

	if dbClient != nil {
		if karma, err := dbClient.GetKarma(ctx, hash); err == nil {
			karmaStore.Set(hash, karma)
			return karma
		}
	}

	karmaStore.Set(hash, 0)
	return 0
}

func updateKarma(ctx context.Context, hash string, karma int) {
	karmaStore.Set(hash, karma)
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

func BadgeHandler(c *gin.Context) {
	badge := c.Param("badge")
	switch badge {
	case "anonymous-friendly.svg":
		serveAnonymousFriendlyBadge(c)
	case "deployed.svg":
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
			if c.Query("provider") == "gl" {
				verified = glprovider.New().IsRepoVerified(owner, repoName)
			} else {
				verified = github.IsRepoVerified(owner, repoName)
			}
		}
	}

	fillColor := "#4CAF50"
	if repo != "" && !verified {
		fillColor = "#9E9E9E"
	}

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="230" height="20.909" role="img" aria-label="Anonymous Contributor Friendly" viewBox="0 0 230 20.909"><title>Anonymous Contributor Friendly</title><path id="s" x2="0" y2="100%%" d=""><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></path><clipPath id="r"><path width="220" height="20" rx="3" fill="#fff" d="M3.136 0H226.864A3.136 3.136 0 0 1 230 3.136V17.773A3.136 3.136 0 0 1 226.864 20.909H3.136A3.136 3.136 0 0 1 0 17.773V3.136A3.136 3.136 0 0 1 3.136 0z"/></clipPath><a href="https://gitgost.fly.dev/" target="_blank" rel="noreferrer"><g clip-path="url(#r)"><path width="28" height="20" fill="black" d="M0 0H29.273V20.909H0V0z"/><path x="28" width="192" height="20" fill="%s" d="M29.273 0H230V20.909H29.273V0z"/><path width="220" height="20" fill="url(#s)" d="M0 0H230V20.909H0V0z"/></g><g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="110"><g transform="matrix(.13 0 0 .13 8 3)"><path fill="#fff" d="M52.273 8.711c-19.219 0 -34.847 15.628 -34.847 34.851v43.558c0 4.786 3.925 8.715 8.711 8.715 3.582 0 6.534 -2.952 6.534 -6.534V84.943c0 -1.229 0.947 -2.177 2.177 -2.177s2.181 0.947 2.181 2.177v4.357c0 3.582 2.948 6.534 6.534 6.534 3.582 0 6.534 -2.952 6.534 -6.534V84.943c0 -1.229 0.947 -2.177 2.177 -2.177s2.177 0.947 2.177 2.177v4.357c0 3.582 2.952 6.534 6.534 6.534 3.586 0 6.534 -2.952 6.534 -6.534V84.943c0 -1.229 0.951 -2.177 2.181 -2.177s2.177 0.947 2.177 2.177v4.357c0 3.582 2.952 6.534 6.534 6.534 4.786 0 8.711 -3.929 8.711 -8.715V43.562c0 -19.223 -15.63 -34.851 -34.847 -34.851zM30.322 37.036c0.27 -0.024 0.539 0.008 0.801 0.086L52.273 43.468l21.142 -6.346a2.175 2.175 0 0 1 2.222 0.592c0.568 0.605 0.742 1.479 0.45 2.255l-6.534 17.426a2.175 2.175 0 0 1 -2.63 1.328L52.273 54.534l-14.649 4.186a2.175 2.175 0 0 1 -2.639 -1.328l-6.534 -17.425a2.17 2.17 0 0 1 1.871 -2.933z"/></g><text aria-hidden="true" x="1290" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="1900">Anonymous Contributor Friendly</text><text x="1290" y="140" transform="scale(.1)" fill="#fff" textLength="1900">Anonymous Contributor Friendly</text></g></a></svg>`, fillColor)

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

var (
	badgeCache = newBoundedMap[int](badgeCacheMax, 5*time.Minute)
)

func BadgePRCountHandler(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	if !isValidRepoName(owner) || !isValidRepoName(repo) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid owner or repo"})
		return
	}

	cacheKey := owner + "/" + repo
	count, ok := badgeCache.Get(cacheKey)
	if !ok {
		if dbClient != nil {
			if n, err := dbClient.GetPRCountByRepo(c.Request.Context(), owner, repo); err == nil {
				count = n
				badgeCache.Set(cacheKey, count)
			}
		}
	}

	label := "Anonymous PRs"
	value := fmt.Sprintf("%d", count)
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

func PRCheckHandler(c *gin.Context) {
	hash := strings.TrimSpace(c.Param("hash"))
	if hash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hash is required"})
		return
	}

	track, ok := getPRTrack(hash)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"hash":    hash,
			"tracked": false,
			"error":   "PR not found. This endpoint only tracks PRs created through gitGost.",
		})
		return
	}

	prov := providerFromName(track.Provider)
	status, err := prov.GetMRStatus(track.Owner, track.Repo, track.Number)
	if err != nil {
		utils.Log("Error fetching MR status for %s/%s#%d: %v", track.Owner, track.Repo, track.Number, err)
		c.JSON(http.StatusOK, gin.H{
			"hash":      hash,
			"tracked":   true,
			"pr_number": track.Number,
			"owner":     track.Owner,
			"repo":      track.Repo,
			"pr_url":    track.PRURL,
			"error":     "could not fetch status from provider",
		})
		return
	}

	events := status.Events
	if len(events) > 10 {
		events = events[len(events)-10:]
	}

	response := gin.H{
		"hash":       hash,
		"tracked":    true,
		"pr_number":  status.Number,
		"owner":      track.Owner,
		"repo":       track.Repo,
		"pr_url":     track.PRURL,
		"provider":   track.Provider,
		"state":      status.State,
		"title":      status.Title,
		"comments":   status.Comments,
		"updated_at": status.UpdatedAt,
	}

	if events != nil {
		response["events"] = events
	}

	if status.ETag != "" && status.ETag != track.LastETag {
		prTrackMu.Lock()
		if t, ok := prTrackStore[hash]; ok {
			t.LastETag = status.ETag
		}
		prTrackMu.Unlock()
	}

	c.JSON(http.StatusOK, response)
}

func VerifyHandler(c *gin.Context) {
	shortHash := commitHash
	if len(shortHash) > 7 {
		shortHash = shortHash[:7]
	}

	baseURL := fmt.Sprintf("%s://%s", getScheme(c.Request), c.Request.Host)

	repoSlug := strings.TrimPrefix(sourceRepo, "https://github.com/")
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

func BinaryHandler(c *gin.Context) {
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

func CodebergProxyHandler(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.AbortWithStatusJSON(http.StatusMethodNotAllowed, gin.H{"error": "only GET/HEAD allowed"})
		return
	}

	path := strings.TrimPrefix(c.Request.URL.Path, "/api/cb-proxy/")
	if path == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing path"})
		return
	}

	allowedAPIPath := strings.HasPrefix(path, "api/v1/")
	allowedRawPath := strings.Contains(path, "/raw/branch/")
	if !allowedAPIPath && !allowedRawPath {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "path not allowed"})
		return
	}

	target := "https://codeberg.org/" + path
	if c.Request.URL.RawQuery != "" {
		target += "?" + c.Request.URL.RawQuery
	}

	req, err := http.NewRequest(c.Request.Method, target, nil)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}

	if allowedAPIPath {
		if token := os.Getenv("CODEBERG_TOKEN"); token != "" {
			req.Header.Set("Authorization", "token "+token)
		} else if auth := c.GetHeader("Authorization"); auth != "" {
			req.Header.Set("Authorization", auth)
		}
	}
	req.Header.Set("User-Agent", "gitGost/1.0")
	if accept := c.GetHeader("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("Codeberg proxy error: %v", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "failed to reach Codeberg"})
		return
	}
	defer resp.Body.Close()

	for _, h := range []string{"Content-Type", "Content-Length", "Cache-Control", "ETag", "Link", "X-Total-Count", "X-Total", "X-Page", "X-PerPage", "X-PageCount", "X-HasMore"} {
		if v := resp.Header.Get(h); v != "" {
			c.Writer.Header().Set(h, v)
		}
	}
	if expose := resp.Header.Get("Access-Control-Expose-Headers"); expose != "" {
		c.Writer.Header().Set("Access-Control-Expose-Headers", expose)
	}
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		utils.Log("Codeberg proxy copy error: %v", err)
	}
}

func SearchHandler(c *gin.Context) {
	query := c.Query("q")
	provider := c.Query("provider")
	topicParam := c.Query("topic")

	if query == "" && topicParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter required"})
		return
	}

	results := []gin.H{}

	ghQuery := query
	glQuery := query
	cbQuery := query
	if topicParam != "" {
		ghQuery = "topic:" + topicParam
		glQuery = topicParam
		cbQuery = topicParam
	}

	if provider == "gh" || provider == "all" || provider == "" {
		ghResults := searchGitHub(ghQuery)
		results = append(results, ghResults...)
	}

	if provider == "gl" || provider == "all" || provider == "" {
		glResults := searchGitLab(glQuery)
		results = append(results, glResults...)
	}

	if provider == "cb" || provider == "all" || provider == "" {
		cbResults := searchCodeberg(cbQuery, topicParam)
		results = append(results, cbResults...)
	}

	c.JSON(http.StatusOK, gin.H{
		"query":   query,
		"results": results,
	})
}

func searchGitHub(query string) []gin.H {
	results := []gin.H{}

	apiURL := fmt.Sprintf("https://api.github.com/search/repositories?q=%s&sort=stars&order=desc&per_page=100", url.QueryEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return results
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("GitHub search error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		utils.Log("GitHub search returned status: %d", resp.StatusCode)
		return results
	}

	var data struct {
		Items []struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
			Description string   `json:"description"`
			Stargazers  int      `json:"stargazers_count"`
			Forks       int      `json:"forks_count"`
			Language    string   `json:"language"`
			Topics      []string `json:"topics"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		utils.Log("GitHub search decode error: %v", err)
		return results
	}

	for _, item := range data.Items {
		results = append(results, gin.H{
			"provider":    "github",
			"name":        item.Name,
			"full_name":   item.FullName,
			"owner":       item.Owner.Login,
			"description": item.Description,
			"stars":       item.Stargazers,
			"forks":       item.Forks,
			"language":    item.Language,
			"topics":      item.Topics,
			"url":         fmt.Sprintf("/gh/%s/%s", item.Owner.Login, item.Name),
		})
	}

	return results
}

func searchCodeberg(query, topic string) []gin.H {
	results := []gin.H{}

	q := query
	if q == "" && topic != "" {
		q = topic
	}

	params := url.Values{}
	params.Set("q", q)
	params.Set("sort", "stars")
	params.Set("order", "desc")
	params.Set("limit", "100")
	if topic != "" {
		params.Set("topic", "true")
	}

	apiURL := "https://codeberg.org/api/v1/repos/search?" + params.Encode()

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return results
	}
	if token := os.Getenv("CODEBERG_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("Codeberg search error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		utils.Log("Codeberg search returned status: %d", resp.StatusCode)
		return results
	}

	var data struct {
		Data []struct {
			Name        string `json:"name"`
			FullName    string `json:"full_name"`
			Description string `json:"description"`
			Stars       int    `json:"stars_count"`
			Forks       int    `json:"forks_count"`
			Language    string `json:"language"`
			Owner       struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		utils.Log("Codeberg search decode error: %v", err)
		return results
	}

	for _, item := range data.Data {
		owner := item.Owner.Login
		if owner == "" {
			parts := strings.Split(item.FullName, "/")
			if len(parts) >= 2 {
				owner = parts[0]
			}
		}
		results = append(results, gin.H{
			"provider":    "codeberg",
			"name":        item.Name,
			"full_name":   item.FullName,
			"owner":       owner,
			"description": item.Description,
			"stars":       item.Stars,
			"forks":       item.Forks,
			"language":    item.Language,
			"url":         fmt.Sprintf("/cb/%s/%s", owner, item.Name),
		})
	}

	return results
}

func getGitLabPrimaryLanguage(projectID int, token string) string {
	if projectID == 0 {
		return ""
	}
	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("https://gitlab.com/api/v4/projects/%d/languages", projectID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ""
	}
	if token != "" {
		req.Header.Set("PRIVATE-TOKEN", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var langs map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&langs); err != nil {
		return ""
	}
	var primary string
	var maxPct float64
	for lang, pct := range langs {
		if pct > maxPct {
			maxPct = pct
			primary = lang
		}
	}
	return primary
}

func searchGitLab(query string) []gin.H {
	results := []gin.H{}

	url := fmt.Sprintf("https://gitlab.com/api/v4/projects?search=%s&order_by=star_count&sort=desc&per_page=100", url.QueryEscape(query))

	glToken := os.Getenv("GITLAB_TOKEN")
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return results
	}
	if glToken != "" {
		req.Header.Set("PRIVATE-TOKEN", glToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("GitLab search error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		utils.Log("GitLab search returned status: %d", resp.StatusCode)
		return results
	}

	var data []struct {
		ID                int    `json:"id"`
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
		Description       string `json:"description"`
		StarCount         int    `json:"star_count"`
		ForksCount        int    `json:"forks_count"`
		Language          string `json:"language"`
		Namespace         struct {
			Path string `json:"path"`
		} `json:"namespace"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		utils.Log("GitLab search decode error: %v", err)
		return results
	}

	const languageBackfillLimit = 10
	langs := make([]string, len(data))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	backfills := 0
	for i, item := range data {
		if item.Language != "" || backfills >= languageBackfillLimit {
			continue
		}
		backfills++
		wg.Add(1)
		go func(idx, id int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			langs[idx] = getGitLabPrimaryLanguage(id, glToken)
		}(i, item.ID)
	}
	wg.Wait()

	for i, item := range data {
		parts := strings.Split(item.PathWithNamespace, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[0]
		language := item.Language
		if language == "" {
			language = langs[i]
		}

		results = append(results, gin.H{
			"provider":    "gitlab",
			"name":        item.Name,
			"full_name":   item.PathWithNamespace,
			"owner":       owner,
			"description": item.Description,
			"stars":       item.StarCount,
			"forks":       item.ForksCount,
			"language":    language,
			"url":         fmt.Sprintf("/gl/%s/%s", owner, item.Name),
		})
	}

	return results
}

func TrendingHandler(c *gin.Context) {
	provider := c.Param("provider")
	sort := c.Query("sort")

	if provider == "" {
		provider = "gh"
	}
	if sort == "" {
		sort = "trending"
	}

	perPage, err := strconv.Atoi(c.Query("per_page"))
	if err != nil || perPage < 1 || perPage > 100 {
		perPage = 10
	}
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page < 1 {
		page = 1
	}

	var results []gin.H

	switch provider {
	case "gh":
		results = getTrendingGitHub(sort, perPage, page)
	case "gl":
		results = getTrendingGitLab(sort, perPage, page)
	case "cb":
		results = getTrendingCodeberg(sort, perPage, page)
	case "all":
		results = append(getTrendingGitHub(sort, perPage, page), getTrendingGitLab(sort, perPage, page)...)
		results = append(results, getTrendingCodeberg(sort, perPage, page)...)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider, use 'gh', 'gl', 'cb' or 'all'"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"provider": provider,
		"results":  results,
	})
}

func getTrendingGitHub(sort string, perPage, page int) []gin.H {
	results := []gin.H{}

	now := time.Now()
	var dateCutoff string
	if sort == "new" {
		dateCutoff = now.AddDate(0, 0, -7).Format("2006-01-02")
	} else {
		dateCutoff = now.AddDate(0, 0, -30).Format("2006-01-02")
	}

	var url string
	switch sort {
	case "new":
		url = fmt.Sprintf("https://api.github.com/search/repositories?q=created:>%s&sort=updated&order=desc&per_page=%d&page=%d", dateCutoff, perPage, page)
	case "updated":
		url = fmt.Sprintf("https://api.github.com/search/repositories?q=pushed:>%s&sort=updated&order=desc&per_page=%d&page=%d", dateCutoff, perPage, page)
	default:
		url = fmt.Sprintf("https://api.github.com/search/repositories?q=created:>%s&sort=stars&order=desc&per_page=%d&page=%d", dateCutoff, perPage, page)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return results
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("GitHub trending error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		utils.Log("GitHub trending returned status: %d", resp.StatusCode)
		return results
	}

	var data struct {
		Items []struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
			Description string `json:"description"`
			Stargazers  int    `json:"stargazers_count"`
			Forks       int    `json:"forks_count"`
			Language    string `json:"language"`
			CreatedAt   string `json:"created_at"`
			UpdatedAt   string `json:"updated_at"`
			PushedAt    string `json:"pushed_at"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		utils.Log("GitHub trending decode error: %v", err)
		return results
	}

	for _, item := range data.Items {
		results = append(results, gin.H{
			"provider":    "github",
			"name":        item.Name,
			"full_name":   item.FullName,
			"owner":       item.Owner.Login,
			"description": item.Description,
			"stars":       item.Stargazers,
			"forks":       item.Forks,
			"language":    item.Language,
			"created_at":  item.CreatedAt,
			"updated_at":  item.UpdatedAt,
			"pushed_at":   item.PushedAt,
			"url":         fmt.Sprintf("/gh/%s/%s", item.Owner.Login, item.Name),
		})
	}

	return results
}

func getTrendingGitLab(sort string, perPage, page int) []gin.H {
	results := []gin.H{}

	var apiURL string
	switch sort {
	case "new":
		apiURL = fmt.Sprintf("https://gitlab.com/api/v4/projects?order_by=created_at&sort=desc&per_page=%d&page=%d", perPage, page)
	case "updated":
		since := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
		apiURL = fmt.Sprintf("https://gitlab.com/api/v4/projects?order_by=last_activity_at&sort=desc&last_activity_after=%s&per_page=%d&page=%d", url.QueryEscape(since), perPage, page)
	default:
		apiURL = fmt.Sprintf("https://gitlab.com/api/v4/projects?order_by=star_count&sort=desc&per_page=%d&page=%d", perPage, page)
	}

	glToken := os.Getenv("GITLAB_TOKEN")
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return results
	}
	if glToken != "" {
		req.Header.Set("PRIVATE-TOKEN", glToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("GitLab trending error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		utils.Log("GitLab trending returned status: %d", resp.StatusCode)
		return results
	}

	var data []struct {
		ID                int    `json:"id"`
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
		Description       string `json:"description"`
		StarCount         int    `json:"star_count"`
		ForksCount        int    `json:"forks_count"`
		Language          string `json:"language"`
		Namespace         struct {
			Path string `json:"path"`
		} `json:"namespace"`
		CreatedAt      string `json:"created_at"`
		UpdatedAt      string `json:"updated_at"`
		LastActivityAt string `json:"last_activity_at"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		utils.Log("GitLab trending decode error: %v", err)
		return results
	}

	langs := make([]string, len(data))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	for i, item := range data {
		wg.Add(1)
		go func(idx, id int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			langs[idx] = getGitLabPrimaryLanguage(id, glToken)
		}(i, item.ID)
	}
	wg.Wait()

	for i, item := range data {
		parts := strings.Split(item.PathWithNamespace, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[0]
		language := item.Language
		if language == "" {
			language = langs[i]
		}

		updatedAt := item.UpdatedAt
		if item.LastActivityAt != "" {
			updatedAt = item.LastActivityAt
		}
		results = append(results, gin.H{
			"provider":    "gitlab",
			"name":        item.Name,
			"full_name":   item.PathWithNamespace,
			"owner":       owner,
			"description": item.Description,
			"stars":       item.StarCount,
			"forks":       item.ForksCount,
			"language":    language,
			"created_at":  item.CreatedAt,
			"updated_at":  updatedAt,
			"url":         fmt.Sprintf("/gl/%s/%s", owner, item.Name),
		})
	}

	return results
}

func getTrendingCodeberg(sort string, perPage, page int) []gin.H {
	results := []gin.H{}

	params := url.Values{}
	params.Set("limit", strconv.Itoa(perPage))
	if page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	switch sort {
	case "new":
		params.Set("sort", "created")
		params.Set("order", "desc")
	case "updated":
		params.Set("sort", "updated")
		params.Set("order", "desc")
	default:
		params.Set("sort", "stars")
		params.Set("order", "desc")
	}

	apiURL := "https://codeberg.org/api/v1/repos/search?" + params.Encode()

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return results
	}
	if token := os.Getenv("CODEBERG_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		utils.Log("Codeberg trending error: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		utils.Log("Codeberg trending returned status: %d", resp.StatusCode)
		return results
	}

	var data struct {
		Data []struct {
			Name        string `json:"name"`
			FullName    string `json:"full_name"`
			Description string `json:"description"`
			Stars       int    `json:"stars_count"`
			Forks       int    `json:"forks_count"`
			Language    string `json:"language"`
			Owner       struct {
				Login string `json:"login"`
			} `json:"owner"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		utils.Log("Codeberg trending decode error: %v", err)
		return results
	}

	for _, item := range data.Data {
		owner := item.Owner.Login
		if owner == "" {
			parts := strings.Split(item.FullName, "/")
			if len(parts) >= 2 {
				owner = parts[0]
			}
		}
		results = append(results, gin.H{
			"provider":    "codeberg",
			"name":        item.Name,
			"full_name":   item.FullName,
			"owner":       owner,
			"description": item.Description,
			"stars":       item.Stars,
			"forks":       item.Forks,
			"language":    item.Language,
			"created_at":  item.CreatedAt,
			"updated_at":  item.UpdatedAt,
			"url":         fmt.Sprintf("/cb/%s/%s", owner, item.Name),
		})
	}

	return results
}
