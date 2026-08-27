package node

import (
	"encoding/base64"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// validRepoName matches the same rules the rest of gitGost applies to repos.
var validRepoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// validRelPath allows only relative paths inside a repository working tree.
var validRelPath = regexp.MustCompile(`^[A-Za-z0-9._/@{}:+,-]{1,512}$`)

func safeRepo(name string) bool {
	return !strings.Contains(name, "..") && validRepoName.MatchString(name)
}

func safePath(p string) bool {
	if p == "" {
		return true
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.Contains(p, `\`) {
		return false
	}
	return validRelPath.MatchString(p)
}

func getLiveNode(c *gin.Context) *Conn {
	id := strings.ToUpper(c.Param("id"))
	
	// Try to get owner from ZKP identity first
	account, exists := c.Get(IdentityKey)
	var owner string
	if exists {
		owner, _ = account.(string)
	}
	
	// If no ZKP identity, try to get from API key (gitGost Account mode)
	if owner == "" {
		apiKey := c.GetHeader("X-Gitgost-Key")
		if apiKey != "" {
			owner = apiKey
		}
	}
	
	if owner == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authenticated identity required"})
		return nil
	}
	
	conn := GetConn(id)
	if conn == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "node offline",
			"note":  "repository exists but storage is unavailable",
		})
		return nil
	}
	if AccountOf(id) != owner {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied to this node"})
		return nil
	}
	return conn
}

type fsRequest struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Offset uint64 `json:"offset"`
	Length int    `json:"len"`
	Data   string `json:"data_b64"`
	Name   string `json:"name"`
}

type repoCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	Readme      string `json:"readme"`
	Gitignore   string `json:"gitignore"`
	License     string `json:"license"`
}

// NodeReposHandler asks a node to report its repository index.
func NodeReposHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	resp, err := conn.Call("repo_list", nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	repos := resp["repositories"]
	if repos == nil {
		repos = []interface{}{}
	}
	c.JSON(http.StatusOK, gin.H{"repositories": repos})
}

// NodeRefsHandler lists refs of a bare repo stored on a node.
func NodeRefsHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	repo := c.Param("repo")
	if !safeRepo(repo) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo name"})
		return
	}
	resp, err := conn.Call("refs_list", map[string]interface{}{"repo": repo})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// NodeRepoCreateHandler initializes an empty bare repository on the node.
func NodeRepoCreateHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	var body fsRequest
	if err := c.ShouldBindJSON(&body); err != nil || !safeRepo(body.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing name"})
		return
	}
	if _, err := conn.Call("repo_create", map[string]interface{}{"repo": body.Name}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[node] repo created on %s: %s", conn.ID, body.Name)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// NodeRepoCreateFullHandler creates a repository with optional README, .gitignore, and LICENSE.
func NodeRepoCreateFullHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	var body repoCreateRequest
	if err := c.ShouldBindJSON(&body); err != nil || !safeRepo(body.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing name"})
		return
	}

	// Create the bare repository
	if _, err := conn.Call("repo_create", map[string]interface{}{"repo": body.Name}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// Initialize repository with optional files
	if body.Readme == "yes" {
		readmeContent := "# " + body.Name + "\n\n" + body.Description
		if _, err := conn.Call("fs_write", map[string]interface{}{
			"repo":     body.Name,
			"path":     "README.md",
			"data_b64": base64.StdEncoding.EncodeToString([]byte(readmeContent)),
		}); err != nil {
			log.Printf("[node] warning: failed to create README.md on %s: %v", conn.ID, err)
		}
	}

	if body.Gitignore != "none" {
		gitignoreTemplates := map[string]string{
			"Node":   "node_modules/\n.env\n.DS_Store\n",
			"Python": "__pycache__/\n*.py[cod]\n*$py.class\n.env\n.DS_Store\n",
			"Go":     "bin/\npkg/\n*.mod\n*.sum\n.DS_Store\n",
			"Java":   "target/\n*.class\n*.jar\n.DS_Store\n",
			"C++":    "bin/\nobj/\n*.o\n*.a\n.DS_Store\n",
		}
		if template, ok := gitignoreTemplates[body.Gitignore]; ok {
			if _, err := conn.Call("fs_write", map[string]interface{}{
				"repo":     body.Name,
				"path":     ".gitignore",
				"data_b64": base64.StdEncoding.EncodeToString([]byte(template)),
			}); err != nil {
				log.Printf("[node] warning: failed to create .gitignore on %s: %v", conn.ID, err)
			}
		}
	}

	if body.License != "none" {
		licenseTemplates := map[string]string{
			"MIT":        "MIT License\n\nPermission is hereby granted...",
			"Apache-2.0": "Apache License 2.0\n\nCopyright...",
			"GPL-3.0":    "GNU GPL 3.0\n\nGNU GENERAL PUBLIC LICENSE...",
			"AGPL-3.0":   "GNU AGPL 3.0\n\nGNU AFFERO GENERAL PUBLIC LICENSE...",
		}
		if template, ok := licenseTemplates[body.License]; ok {
			if _, err := conn.Call("fs_write", map[string]interface{}{
				"repo":     body.Name,
				"path":     "LICENSE",
				"data_b64": base64.StdEncoding.EncodeToString([]byte(template)),
			}); err != nil {
				log.Printf("[node] warning: failed to create LICENSE on %s: %v", conn.ID, err)
			}
		}
	}

	log.Printf("[node] repo created on %s: %s", conn.ID, body.Name)
	c.JSON(http.StatusOK, gin.H{"ok": true, "repo_id": body.Name})
}

// NodeRepoDeleteHandler removes a bare repository from the node.
func NodeRepoDeleteHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	var body fsRequest
	if err := c.ShouldBindJSON(&body); err != nil || !safeRepo(body.Repo) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing repo"})
		return
	}
	if _, err := conn.Call("repo_delete", map[string]interface{}{"repo": body.Repo}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[node] repo deleted on %s: %s", conn.ID, body.Repo)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// NodeFSReadHandler streams bytes out of a file stored on the node's microSD.
// Response contains base64 data and eof flag.
func NodeFSReadHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	var body fsRequest
	if err := c.ShouldBindJSON(&body); err != nil ||
		!safeRepo(body.Repo) || !safePath(body.Path) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if body.Length <= 0 || body.Length > 32*1024 {
		body.Length = 8192
	}
	resp, err := conn.Call("fs_read", map[string]interface{}{
		"repo":   body.Repo,
		"path":   body.Path,
		"offset": body.Offset,
		"len":    body.Length,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// NodeFSWriteHandler writes base64 data at an offset of a node-side file.
func NodeFSWriteHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	var body fsRequest
	if err := c.ShouldBindJSON(&body); err != nil ||
		!safeRepo(body.Repo) || !safePath(body.Path) || len(body.Data) > 64*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if _, err := base64.StdEncoding.DecodeString(body.Data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid base64"})
		return
	}
	resp, err := conn.Call("fs_write", map[string]interface{}{
		"repo":   body.Repo,
		"path":   body.Path,
		"offset": body.Offset,
		"data":   body.Data,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// NodeFSListHandler lists a directory of a repository on the node.
func NodeFSListHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	var body fsRequest
	if err := c.ShouldBindJSON(&body); err != nil ||
		!safeRepo(body.Repo) || !safePath(body.Path) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	resp, err := conn.Call("fs_list", map[string]interface{}{
		"repo": body.Repo,
		"path": body.Path,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// NodeCmdHandler is a generic debug/admin command proxy.
func NodeCmdHandler(c *gin.Context) {
	conn := getLiveNode(c)
	if conn == nil {
		return
	}
	var body struct {
		Action string                 `json:"action"`
		Params map[string]interface{} `json:"params"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Action == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action required"})
		return
	}
	switch body.Action {
	case "ping":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported action"})
		return
	}
	resp, err := conn.Call(body.Action, body.Params)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// getNodeForAccountRepo resolves the node that stores a given account's repo.
// gitGost native repo content (tree/raw/info/branches/tags) is publicly readable
// for repos hosted on an online node, so the request identity is no longer
// required here — the router-level middleware already gates writes.
func getNodeForAccountRepo(c *gin.Context, account, repo string) *Conn {
	nodes := ListByAccount(account)
	for _, n := range nodes {
		id, _ := n["id"].(string)
		conn := GetConn(id)
		if conn == nil {
			continue
		}
		rawRepos, _ := n["repositories"].([]interface{})
		for _, r := range rawRepos {
			obj, _ := r.(map[string]interface{})
			if obj["name"] == repo {
				return conn
			}
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "repo not found on any online node"})
	return nil
}

// ggBranchesAndTags asks the node for the repo refs and splits them into
// branches and tags. It tolerates both a structured response ({"branches":[],
// "tags":[]}) and a flat list of full ref names ({"refs":[]}).
func ggBranchesAndTags(conn *Conn, repo string) ([]string, []string, error) {
	resp, err := conn.Call("refs_list", map[string]interface{}{"repo": repo})
	if err != nil {
		return nil, nil, err
	}
	var branches, tags []string
	if raw, ok := resp["branches"].([]interface{}); ok {
		for _, b := range raw {
			if s, ok := b.(string); ok {
				branches = append(branches, s)
			}
		}
	}
	if raw, ok := resp["tags"].([]interface{}); ok {
		for _, t := range raw {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}
	if len(branches) == 0 && len(tags) == 0 {
		if raw, ok := resp["refs"].([]interface{}); ok {
			for _, r := range raw {
				s, ok := r.(string)
				if !ok {
					continue
				}
				switch {
				case strings.HasPrefix(s, "refs/heads/"):
					branches = append(branches, strings.TrimPrefix(s, "refs/heads/"))
				case strings.HasPrefix(s, "refs/tags/"):
					tags = append(tags, strings.TrimPrefix(s, "refs/tags/"))
				}
			}
		}
	}
	return branches, tags, nil
}

func GGRepoTreeHandler(c *gin.Context) {
	conn := getNodeForAccountRepo(c, c.Param("owner"), c.Param("repo"))
	if conn == nil {
		return
	}
	path := c.Query("path")
	resp, err := conn.Call("fs_list", map[string]interface{}{
		"repo": c.Param("repo"),
		"path": path,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func GGRepoRawHandler(c *gin.Context) {
	conn := getNodeForAccountRepo(c, c.Param("owner"), c.Param("repo"))
	if conn == nil {
		return
	}
	path := c.Query("path")
	resp, err := conn.Call("fs_read", map[string]interface{}{
		"repo": c.Param("repo"),
		"path": path,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func GGRepoInfoHandler(c *gin.Context) {
	conn := getNodeForAccountRepo(c, c.Param("owner"), c.Param("repo"))
	if conn == nil {
		return
	}
	repo := c.Param("repo")
	resp, err := conn.Call("repo_list", nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	rawRepos, _ := resp["repositories"].([]interface{})
	var size uint64
	for _, r := range rawRepos {
		obj, _ := r.(map[string]interface{})
		if obj["name"] == repo {
			if s, ok := obj["size"].(float64); ok {
				size = uint64(s)
			}
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"name": repo,
		"size": size,
	})
}

// GGRepoBranchesHandler returns the list of branch names for a gitGost repo.
func GGRepoBranchesHandler(c *gin.Context) {
	conn := getNodeForAccountRepo(c, c.Param("owner"), c.Param("repo"))
	if conn == nil {
		return
	}
	branches, _, err := ggBranchesAndTags(conn, c.Param("repo"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"branches": branches})
}

// GGRepoTagsHandler returns the list of tag names for a gitGost repo.
func GGRepoTagsHandler(c *gin.Context) {
	conn := getNodeForAccountRepo(c, c.Param("owner"), c.Param("repo"))
	if conn == nil {
		return
	}
	_, tags, err := ggBranchesAndTags(conn, c.Param("repo"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}
