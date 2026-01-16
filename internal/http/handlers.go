package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/livrasand/gitGost/internal/database"
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
		sendErrorResponse(c, "error reading body")
		return
	}

	utils.Log("Received push for %s/%s, size: %d bytes", owner, repo, len(body))

	// Crear repo temporal
	tempDir, err := utils.CreateTempDir()
	if err != nil {
		utils.Log("Error creating temp dir: %v", err)
		sendErrorResponse(c, fmt.Sprintf("error creating temp dir: %v", err))
		return
	}
	defer utils.CleanupTempDir(tempDir)

	// Configurar cabeceras antes de escribir cualquier cosa
	c.Writer.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	c.Writer.WriteHeader(http.StatusOK)

	var response bytes.Buffer

	// Mensaje de progreso inicial
	WriteSidebandLine(&response, 2, "remote: gitGost: Processing your anonymous contribution...")

	// Procesar el packfile
	newSHA, commitMessage, err := git.ReceivePack(tempDir, body, owner, repo)
	if err != nil {
		utils.Log("Error receiving pack: %v", err)
		WriteSidebandLine(&response, 3, fmt.Sprintf("unpack error: %v", err))
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Commits received successfully, HEAD at: %s", newSHA)
	WriteSidebandLine(&response, 2, "remote: gitGost: Commits anonymized successfully")

	// Crear fork del repositorio
	WriteSidebandLine(&response, 2, "remote: gitGost: Creating fork...")
	forkOwner, err := github.ForkRepo(owner, repo)
	if err != nil {
		utils.Log("Error creating fork: %v", err)
		WriteSidebandLine(&response, 2, "unpack ok")
		WriteSidebandLine(&response, 3, fmt.Sprintf("error creating fork: %v", err))
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Fork ready: %s/%s", forkOwner, repo)
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote: gitGost: Fork ready at %s/%s", forkOwner, repo))

	// Push al fork
	WriteSidebandLine(&response, 2, "remote: gitGost: Pushing to fork...")
	branch, err := git.PushToGitHub(owner, repo, tempDir, forkOwner)
	if err != nil {
		utils.Log("Error pushing to fork: %v", err)
		WriteSidebandLine(&response, 2, "unpack ok")
		WriteSidebandLine(&response, 3, fmt.Sprintf("error pushing to fork: %v", err))
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Pushed to fork branch: %s", branch)
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote: gitGost: Branch '%s' created", branch))

	// Crear PR desde el fork al repo original
	WriteSidebandLine(&response, 2, "remote: gitGost: Creating pull request...")
	prURL, err := github.CreatePR(owner, repo, branch, forkOwner, commitMessage)
	if err != nil {
		utils.Log("Error creating PR: %v", err)
		WriteSidebandLine(&response, 2, "unpack ok")
		WriteSidebandLine(&response, 3, fmt.Sprintf("error creating PR: %v", err))
		WritePktLine(&response, "")
		c.Writer.Write(response.Bytes())
		return
	}

	utils.Log("Created PR: %s", prURL)

	// Registrar estadísticas
	if err := RecordPR(c.Request.Context(), owner, repo, prURL); err != nil {
		utils.Log("Error recording stats: %v", err)
		// No fallamos si solo falla las estadísticas
	}

	// MENSAJES DE ÉXITO CLAROS
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, "remote: ========================================")
	WriteSidebandLine(&response, 2, "remote: SUCCESS! Pull Request Created")
	WriteSidebandLine(&response, 2, "remote: ========================================")
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote: PR URL: %s", prURL))
	WriteSidebandLine(&response, 2, "remote: Author: @gitgost-anonymous")
	WriteSidebandLine(&response, 2, fmt.Sprintf("remote: Branch: %s", branch))
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, "remote: Your identity has been anonymized.")
	WriteSidebandLine(&response, 2, "remote: No trace to you remains in the commit history.")
	WriteSidebandLine(&response, 2, "remote: ")
	WriteSidebandLine(&response, 2, "remote: ========================================")
	WriteSidebandLine(&response, 2, "remote: ")

	// Respuesta exitosa estándar de Git
	WriteSidebandLine(&response, 2, "unpack ok")
	WriteSidebandLine(&response, 2, "ok refs/heads/main")
	WritePktLine(&response, "") // flush final

	c.Writer.Write(response.Bytes())
}

// sendErrorResponse envía una respuesta de error en formato Git protocol
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

var (
	startTime = time.Now()
	dbClient  *database.SupabaseClient
	dbOnce    sync.Once
)

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not configured"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not configured"})
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
