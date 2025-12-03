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
	capabilities := "report-status delete-refs side-band-64k quiet ofs-delta"

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

	// Manejar 100 Continue
	if c.GetHeader("Expect") == "100-continue" {
		c.Writer.WriteHeader(http.StatusContinue)
	}

	// Leer body completo
	utils.Log("Content-Type: %s", c.GetHeader("Content-Type"))
	utils.Log("Content-Length: %s", c.GetHeader("Content-Length"))

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		utils.Log("Error reading body: %v", err)
		// Responder en formato Git protocol, no JSON
		c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		c.Writer.WriteHeader(http.StatusOK)
		var response bytes.Buffer
		WritePktLine(&response, "unpack error reading body\n")
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Received push for %s/%s, size: %d bytes", owner, repo, len(body))

	// Crear repo temporal
	tempDir, err := utils.CreateTempDir()
	if err != nil {
		utils.Log("Error creating temp dir: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create temp dir"})
		return
	}
	defer utils.CleanupTempDir(tempDir)

	// Procesar el packfile
	newSHA, err := git.ReceivePack(tempDir, body, owner, repo)
	if err != nil {
		utils.Log("Error receiving pack: %v", err)

		// Responder con error en formato Git protocol
		c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		c.Writer.WriteHeader(http.StatusOK)

		var response bytes.Buffer
		WritePktLine(&response, fmt.Sprintf("unpack %s\n", err.Error()))
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	// TODO: Reescribir commits para anonimizar (mantener historia)
	// Por ahora, usamos los commits tal cual para mantener la historia compartida
	utils.Log("Commits received successfully, HEAD at: %s", newSHA)

	// Crear fork del repositorio
	forkOwner, err := github.ForkRepo(owner, repo)
	if err != nil {
		utils.Log("Error creating fork: %v", err)

		c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		c.Writer.WriteHeader(http.StatusOK)

		var response bytes.Buffer
		WritePktLine(&response, "unpack ok\n")
		WritePktLine(&response, fmt.Sprintf("ng refs/heads/main %s\n", err.Error()))
		WritePktLine(&response, "")

		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Fork ready: %s/%s", forkOwner, repo)

	// Push al fork
	branch, err := git.PushToGitHub(owner, repo, tempDir, forkOwner)
	if err != nil {
		utils.Log("Error pushing to fork: %v", err)

		c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		c.Writer.WriteHeader(http.StatusOK)

		var response bytes.Buffer
		WritePktLine(&response, "unpack ok\n")
		WritePktLine(&response, fmt.Sprintf("ng refs/heads/main %s\n", err.Error()))
		WritePktLine(&response, "")

		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Pushed to fork branch: %s", branch)

	// Crear PR desde el fork al repo original
	prURL, err := github.CreatePR(owner, repo, branch, forkOwner)
	if err != nil {
		utils.Log("Error creating PR: %v", err)
		// Continuar aunque falle el PR
	} else {
		utils.Log("Created PR: %s", prURL)
	}

	// Respuesta exitosa
	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	c.Writer.WriteHeader(http.StatusOK)

	var response bytes.Buffer
	if prURL != "" {
		WritePktLine(&response, fmt.Sprintf("PR created: %s\n", prURL))
	}
	WritePktLine(&response, "unpack ok\n")
	WritePktLine(&response, "ok refs/heads/main\n")
	WritePktLine(&response, "")

	c.Writer.Write(response.Bytes())
}

func HealthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"time":   time.Now().UTC().Format(time.RFC3339),
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

var startTime = time.Now()
