package http

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/tokenpool"
)

// Historial de un archivo (vista tipo GitHub): lista paginada de commits que
// tocaron el path indicado. Reutiliza el listado de commits del blame pero con
// paginación real por página, para poder mostrar más allá del límite del blame.

const (
	fileHistoryPerPage    = 30
	fileHistoryMaxPerPage = 100
)

type fileHistoryCommit struct {
	Sha     string `json:"sha"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

func FileHistoryHandler(c *gin.Context) {
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
	page := atoiDefault(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	if page > 200 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "page out of range"})
		return
	}

	client := &http.Client{Timeout: blameHTTPTimeout}
	commits, err := listPathCommits(client, provider, owner, repo, path, ref, fileHistoryPerPage, page)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "failed to list commits"})
		return
	}
	out := make([]fileHistoryCommit, 0, len(commits))
	for _, cm := range commits {
		out = append(out, fileHistoryCommit{Sha: cm.Sha, Author: cm.Author, Date: cm.Date, Message: cm.Message})
	}
	hasMore := len(out) >= fileHistoryPerPage
	c.JSON(http.StatusOK, gin.H{
		"commits":   out,
		"page":      page,
		"hasMore":   hasMore,
		"truncated": hasMore && page >= fileHistoryMaxPerPage,
	})
}

// listPathCommits lista los commits que tocaron un archivo, con paginación.
// page empieza en 1; blame usa esta misma función con page=1.
func listPathCommits(client *http.Client, provider, owner, repo, path, ref string, perPage, page int) ([]blameCommitInfo, error) {
	q := url.Values{}
	q.Set("path", path)
	switch provider {
	case "gh":
		q.Set("per_page", fmt.Sprint(perPage))
		if page > 1 {
			q.Set("page", fmt.Sprint(page))
		}
		if ref != "" {
			q.Set("sha", ref)
		}
		target := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?%s", url.PathEscape(owner), url.PathEscape(repo), q.Encode())
		headers := map[string]string{}
		if token := tokenpool.NextGitHubToken(); token != "" {
			headers["Authorization"] = "token " + token
		}
		var raw []struct {
			Sha    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"author"`
				Committer struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
		}
		if err := blameGetJSON(client, target, headers, &raw); err != nil {
			return nil, err
		}
		out := make([]blameCommitInfo, 0, len(raw))
		for _, r := range raw {
			name := r.Commit.Author.Name
			if name == "" && r.Author != nil {
				name = r.Author.Login
			}
			date := r.Commit.Author.Date
			if date == "" {
				date = r.Commit.Committer.Date
			}
			out = append(out, blameCommitInfo{
				Sha:     r.Sha,
				Author:  name,
				Date:    date,
				Message: firstBlameLine(r.Commit.Message),
			})
		}
		return out, nil
	case "gl":
		q.Set("per_page", fmt.Sprint(perPage))
		if page > 1 {
			q.Set("page", fmt.Sprint(page))
		}
		if ref != "" {
			q.Set("ref_name", ref)
		}
		project := url.PathEscape(owner + "/" + repo)
		target := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/commits?%s", project, q.Encode())
		headers := map[string]string{}
		if token := os.Getenv("GITLAB_TOKEN"); token != "" {
			headers["PRIVATE-TOKEN"] = token
		}
		var raw []struct {
			Id            string `json:"id"`
			Title         string `json:"title"`
			Message       string `json:"message"`
			AuthorName    string `json:"author_name"`
			CommittedDate string `json:"committed_date"`
			CreatedAt     string `json:"created_at"`
		}
		if err := blameGetJSON(client, target, headers, &raw); err != nil {
			return nil, err
		}
		out := make([]blameCommitInfo, 0, len(raw))
		for _, r := range raw {
			msg := r.Title
			if msg == "" {
				msg = firstBlameLine(r.Message)
			}
			date := r.CommittedDate
			if date == "" {
				date = r.CreatedAt
			}
			out = append(out, blameCommitInfo{Sha: r.Id, Author: r.AuthorName, Date: date, Message: msg})
		}
		return out, nil
	default: // cb (Gitea)
		q.Set("limit", fmt.Sprint(perPage))
		if page > 1 {
			q.Set("page", fmt.Sprint(page))
		}
		if ref != "" {
			q.Set("sha", ref)
		}
		target := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s/commits?%s", url.PathEscape(owner), url.PathEscape(repo), q.Encode())
		headers := map[string]string{}
		if token := os.Getenv("CODEBERG_TOKEN"); token != "" {
			headers["Authorization"] = "token " + token
		}
		var raw []struct {
			Sha    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"author"`
				Committer struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
		}
		if err := blameGetJSON(client, target, headers, &raw); err != nil {
			return nil, err
		}
		out := make([]blameCommitInfo, 0, len(raw))
		for _, r := range raw {
			name := r.Commit.Author.Name
			if name == "" {
				name = r.Commit.Committer.Name
			}
			date := r.Commit.Author.Date
			if date == "" {
				date = r.Commit.Committer.Date
			}
			out = append(out, blameCommitInfo{
				Sha:     r.Sha,
				Author:  name,
				Date:    date,
				Message: firstBlameLine(r.Commit.Message),
			})
		}
		return out, nil
	}
}
