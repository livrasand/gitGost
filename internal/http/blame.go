package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/tokenpool"
	"github.com/livrasand/gitGost/internal/utils"
)

// Blame universal para las tres forjas. Ninguna API pública lo ofrece de forma
// homogénea (Gitea ni siquiera tiene endpoint de blame), así que se calcula
// aquí: se lista el historial de commits que tocaron el archivo y se recorren
// los parches de atrás hacia adelante atribuyendo cada línea del contenido
// final al commit que la introdujo.

const (
	blameMaxCommits  = 60
	blameHTTPTimeout = 20 * time.Second
)

var blameHunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

type blameCommitInfo struct {
	Sha     string
	Author  string
	Date    string
	Message string
}

type blameHunk struct {
	oldStart, oldCount int
	newStart, newCount int
	lines              []string
}

type blameRange struct {
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Sha     string `json:"sha"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

type blameEntry struct {
	attr     string
	finalIdx int
}

func BlameHandler(c *gin.Context) {
	provider := c.Param("provider")
	switch provider {
	case "github":
		provider = "gh"
	case "gitlab":
		provider = "gl"
	case "codeberg":
		provider = "cb"
	}
	if provider != "gh" && provider != "gl" && provider != "cb" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unsupported provider"})
		return
	}
	owner := c.Param("owner")
	repo := c.Param("repo")
	path := c.Query("path")
	ref := strings.TrimSpace(c.Query("ref"))
	if owner == "" || repo == "" || path == "" || strings.Contains(path, "..") {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "owner, repo and path are required"})
		return
	}

	client := &http.Client{Timeout: blameHTTPTimeout}

	commits, err := listPathCommits(client, provider, owner, repo, path, ref, blameMaxCommits, 1)
	if err != nil {
		utils.Log("blame: listing commits failed for %s/%s/%s: %v", provider, owner, repo, err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "failed to list commits"})
		return
	}
	if len(commits) == 0 {
		c.JSON(http.StatusOK, gin.H{"ranges": []blameRange{}, "truncated": false})
		return
	}

	content, _ := blameFetchContent(client, provider, owner, repo, path, ref)
	lines := []string{}
	if content != "" {
		trimmed := strings.TrimSuffix(content, "\n")
		lines = strings.Split(trimmed, "\n")
	}

	results := make([]string, len(lines))
	entries := make([]blameEntry, len(lines))
	for i := range entries {
		entries[i] = blameEntry{finalIdx: i}
	}

	// Recorrido inverso: del commit más reciente al más antiguo. Cada parche
	// transforma el estado "después del commit" en el estado "antes"; las líneas
	// '+' que desaparecen al retroceder fueron introducidas por ese commit.
	for i := len(commits) - 1; i >= 0; i-- {
		hunks, hunkErr := blameFileHunks(client, provider, owner, repo, path, commits[i])
		if hunkErr != nil || len(hunks) == 0 {
			continue
		}
		entries = blameApplyReverse(entries, hunks, commits[i].Sha, results)
	}

	// Líneas sin atribuir (historial truncado o parche fallido): se asignan al
	// commit más antiguo conocido para que el blame no salga lleno de huecos.
	oldest := commits[len(commits)-1]
	for i, sha := range results {
		if sha == "" {
			results[i] = oldest.Sha
		}
	}

	ranges := blameBuildRanges(results, commits)
	truncated := len(commits) >= blameMaxCommits
	c.JSON(http.StatusOK, gin.H{"ranges": ranges, "truncated": truncated})
}

func blameBuildRanges(results []string, commits []blameCommitInfo) []blameRange {
	bySha := make(map[string]blameCommitInfo, len(commits))
	for _, cm := range commits {
		bySha[cm.Sha] = cm
	}
	out := []blameRange{}
	for i := 0; i < len(results); {
		sha := results[i]
		j := i
		for j+1 < len(results) && results[j+1] == sha {
			j++
		}
		cm := bySha[sha]
		out = append(out, blameRange{
			Start:   i + 1,
			End:     j + 1,
			Sha:     sha,
			Author:  cm.Author,
			Date:    cm.Date,
			Message: cm.Message,
		})
		i = j + 1
	}
	return out
}

func blameApplyReverse(entries []blameEntry, hunks []blameHunk, sha string, results []string) []blameEntry {
	pos := 0
	nextNew := 1
	out := make([]blameEntry, 0, len(entries))
	for _, h := range hunks {
		start := h.newStart
		if h.newCount == 0 {
			start++
		}
		gap := start - nextNew
		for i := 0; i < gap && pos < len(entries); i++ {
			out = append(out, entries[pos])
			pos++
		}
		for _, line := range h.lines {
			switch {
			case line == "" || strings.HasPrefix(line, "\\"):
				// "\ No newline at end of file": ignorar.
			case strings.HasPrefix(line, "+"):
				if pos < len(entries) {
					e := entries[pos]
					pos++
					if e.finalIdx >= 0 && results[e.finalIdx] == "" {
						results[e.finalIdx] = sha
					}
				}
			case strings.HasPrefix(line, "-"):
				out = append(out, blameEntry{})
			default:
				if pos < len(entries) {
					out = append(out, entries[pos])
					pos++
				} else {
					out = append(out, blameEntry{})
				}
			}
		}
		nextNew = start + h.newCount
	}
	out = append(out, entries[pos:]...)
	return out
}

func blameParseHunks(body string) []blameHunk {
	hunks := []blameHunk{}
	var cur *blameHunk
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := blameHunkRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			cur = &blameHunk{
				oldStart: atoiDefault(m[1], 1),
				oldCount: atoiDefault(m[2], 1),
				newStart: atoiDefault(m[3], 1),
				newCount: atoiDefault(m[4], 1),
			}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		cur.lines = append(cur.lines, line)
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func blameGetJSON(client *http.Client, target string, headers map[string]string, out any) error {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gitGost")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		// Codeberg sufre timeouts puntuales: reintentar una vez antes de fallar.
		time.Sleep(500 * time.Millisecond)
		resp, err = client.Do(req)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream status %d for %s", resp.StatusCode, target)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

func blameGetText(client *http.Client, target string, headers map[string]string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "gitGost")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		// Codeberg sufre timeouts puntuales: reintentar una vez antes de fallar.
		time.Sleep(500 * time.Millisecond)
		resp, err = client.Do(req)
	}
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upstream status %d for %s", resp.StatusCode, target)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return string(b), err
}

// Devuelve los hunks del diff unificado que afectan al archivo concreto.
func blameFileHunks(client *http.Client, provider, owner, repo, path string, cm blameCommitInfo) ([]blameHunk, error) {
	switch provider {
	case "gh":
		headers := map[string]string{"Accept": "application/vnd.github+json"}
		if token := tokenpool.NextGitHubToken(); token != "" {
			headers["Authorization"] = "token " + token
		}
		var detail struct {
			Files []struct {
				Filename         string `json:"filename"`
				PreviousFilename string `json:"previous_filename"`
				Patch            string `json:"patch"`
			} `json:"files"`
		}
		target := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s",
			url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(cm.Sha))
		if err := blameGetJSON(client, target, headers, &detail); err != nil {
			return nil, err
		}
		body := ""
		for _, f := range detail.Files {
			if f.Patch == "" {
				continue
			}
			if f.Filename == path || f.PreviousFilename == path {
				body += f.Patch + "\n"
			}
		}
		return blameParseHunks(body), nil
	case "gl":
		headers := map[string]string{}
		if token := os.Getenv("GITLAB_TOKEN"); token != "" {
			headers["PRIVATE-TOKEN"] = token
		}
		project := url.PathEscape(owner + "/" + repo)
		target := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/commits/%s/diff",
			project, url.PathEscape(cm.Sha))
		var raw []struct {
			Diff    string `json:"diff"`
			NewPath string `json:"new_path"`
			OldPath string `json:"old_path"`
		}
		if err := blameGetJSON(client, target, headers, &raw); err != nil {
			return nil, err
		}
		body := ""
		for _, f := range raw {
			if f.NewPath == path || f.OldPath == path {
				body += f.Diff + "\n"
			}
		}
		return blameParseHunks(body), nil
	default: // cb: diff crudo del commit
		headers := map[string]string{}
		if token := os.Getenv("CODEBERG_TOKEN"); token != "" {
			headers["Authorization"] = "token " + token
		}
		target := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s/git/commits/%s.diff",
			url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(cm.Sha))
		text, err := blameGetText(client, target, headers)
		if err != nil {
			return nil, err
		}
		return blameParseHunks(blameExtractFileSection(text, path)), nil
	}
}

// Extrae del diff completo la sección "diff --git" correspondiente al archivo.
func blameExtractFileSection(diff, path string) string {
	sections := strings.Split(diff, "\ndiff --git ")
	for i, s := range sections {
		if i > 0 {
			s = "diff --git " + s
		}
		if blameSectionTargets(s, path) {
			return s
		}
	}
	return ""
}

func blameSectionTargets(section, path string) bool {
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "+++ b/") && strings.TrimPrefix(line, "+++ b/") == path {
			return true
		}
		if strings.HasPrefix(line, "--- a/") && strings.TrimPrefix(line, "--- a/") == path &&
			!strings.Contains(section, "+++ ") {
			return true
		}
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line[len("diff --git "):])
			if len(parts) >= 2 && (strings.TrimPrefix(parts[0], "a/") == path || strings.TrimPrefix(parts[1], "b/") == path) {
				return true
			}
		}
	}
	return false
}

func blameFetchContent(client *http.Client, provider, owner, repo, path, ref string) (string, error) {
	if ref == "" {
		ref = blameDefaultRef(client, provider, owner, repo)
	}
	if ref == "" {
		ref = "HEAD"
	}
	switch provider {
	case "gh":
		headers := map[string]string{}
		if token := tokenpool.NextGitHubToken(); token != "" {
			headers["Authorization"] = "token " + token
		}
		target := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
			url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(ref), blameEncodePath(path))
		return blameGetText(client, target, headers)
	case "gl":
		headers := map[string]string{}
		if token := os.Getenv("GITLAB_TOKEN"); token != "" {
			headers["PRIVATE-TOKEN"] = token
		}
		project := url.PathEscape(owner + "/" + repo)
		target := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/files/%s/raw?ref=%s",
			project, blameEncodePath(path), url.QueryEscape(ref))
		return blameGetText(client, target, headers)
	default:
		headers := map[string]string{}
		if token := os.Getenv("CODEBERG_TOKEN"); token != "" {
			headers["Authorization"] = "token " + token
		}
		target := fmt.Sprintf("https://codeberg.org/%s/%s/raw/branch/%s/%s",
			url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(ref), blameEncodePath(path))
		return blameGetText(client, target, headers)
	}
}

func blameDefaultRef(client *http.Client, provider, owner, repo string) string {
	switch provider {
	case "gh":
		headers := map[string]string{}
		if token := tokenpool.NextGitHubToken(); token != "" {
			headers["Authorization"] = "token " + token
		}
		var data struct {
			DefaultBranch string `json:"default_branch"`
		}
		target := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
		if err := blameGetJSON(client, target, headers, &data); err == nil {
			return data.DefaultBranch
		}
	case "gl":
		headers := map[string]string{}
		if token := os.Getenv("GITLAB_TOKEN"); token != "" {
			headers["PRIVATE-TOKEN"] = token
		}
		var data struct {
			DefaultBranch string `json:"default_branch"`
		}
		target := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s", url.PathEscape(owner+"/"+repo))
		if err := blameGetJSON(client, target, headers, &data); err == nil {
			return data.DefaultBranch
		}
	default:
		headers := map[string]string{}
		if token := os.Getenv("CODEBERG_TOKEN"); token != "" {
			headers["Authorization"] = "token " + token
		}
		var data struct {
			DefaultBranch string `json:"default_branch"`
		}
		target := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
		if err := blameGetJSON(client, target, headers, &data); err == nil {
			return data.DefaultBranch
		}
	}
	return ""
}

func blameEncodePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func firstBlameLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
