package http

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Descarga de assets de releases (experiencia tipo GitHub): el navegador pide
// el asset a gitGost y este lo transmite en stream desde la forja original,
// forzando Content-Disposition: attachment. Así la descarga funciona en un
// solo clic desde el mismo origen (incluido el WebView de Capacitor), sin
// navegaciones externas ni bloqueadores de popups.

var releaseAssetHosts = map[string][]string{
	"gh": {
		"github.com",
		"api.github.com",
		"codeload.github.com",
		"objects.githubusercontent.com",
		".githubusercontent.com",
	},
	"gl": {
		"gitlab.com",
	},
	"cb": {
		"codeberg.org",
	},
}

func releaseAssetHostAllowed(provider, host string) bool {
	host = strings.ToLower(host)
	for _, h := range releaseAssetHosts[provider] {
		if strings.HasPrefix(h, ".") {
			if strings.HasSuffix(host, h) || host == strings.TrimPrefix(h, ".") {
				return true
			}
			continue
		}
		if host == h {
			return true
		}
	}
	return false
}

func ReleaseAssetDownloadHandler(c *gin.Context) {
	provider := c.Param("provider")
	switch provider {
	case "github":
		provider = "gh"
	case "gitlab":
		provider = "gl"
	case "codeberg":
		provider = "cb"
	}
	if _, ok := releaseAssetHosts[provider]; !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unsupported provider"})
		return
	}
	rawURL := strings.TrimSpace(c.Query("url"))
	if rawURL == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "url is required"})
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid url"})
		return
	}
	if !releaseAssetHostAllowed(provider, parsed.Host) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "host not allowed for this provider"})
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}
	req.Header.Set("User-Agent", "gitGost/1.0")
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "failed to reach the forge"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.AbortWithStatusJSON(resp.StatusCode, gin.H{"error": fmt.Sprintf("upstream status %d", resp.StatusCode)})
		return
	}

	filename := path.Base(parsed.Path)
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, e := mime.ParseMediaType(cd); e == nil && params["filename"] != "" {
			filename = params["filename"]
		}
	}
	if filename == "" || filename == "." || filename == "/" {
		filename = "download"
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Writer.Header().Set("Content-Type", ct)
	} else {
		c.Writer.Header().Set("Content-Type", "application/octet-stream")
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		c.Writer.Header().Set("Content-Length", cl)
	}
	c.Writer.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"%s\"", strings.ReplaceAll(filename, "\"", "")))
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		// La descarga ya empezó; solo registrar el corte.
		return
	}
}
