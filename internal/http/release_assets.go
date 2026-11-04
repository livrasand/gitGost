package http

import (
	"errors"
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

var (
	errInvalidURLProvider = errors.New("unsupported provider")
	errInvalidURL         = errors.New("invalid url")
	errURLHostNotAllowed  = errors.New("host not allowed for this provider")
)

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

func resolveUpstreamURL(rawURL, provider string) (*url.URL, error) {
	if _, ok := releaseAssetHosts[provider]; !ok {
		return nil, errInvalidURLProvider
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errInvalidURL
	}
	if parsed.User != nil {
		return nil, errInvalidURL
	}
	if !releaseAssetHostAllowed(provider, parsed.Host) {
		return nil, errURLHostNotAllowed
	}
	return &url.URL{
		Scheme:   "https",
		Host:     parsed.Host,
		Path:     parsed.Path,
		RawPath:  parsed.RawPath,
		RawQuery: parsed.RawQuery,
	}, nil
}

func streamProxiedFile(c *gin.Context) {
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
	rawURL := c.Query("url")
	if rawURL == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "url is required"})
		return
	}
	target, err := resolveUpstreamURL(rawURL, provider)
	switch {
	case errors.Is(err, errURLHostNotAllowed):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": errURLHostNotAllowed.Error()})
		return
	case err != nil:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}
	req.Header.Set("User-Agent", "gitGost/1.0")
	client := &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Scheme != "https" || !releaseAssetHostAllowed(provider, req.URL.Host) {
				return fmt.Errorf("redirect to disallowed destination")
			}
			return nil
		},
	}
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

	filename := path.Base(target.Path)
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
		return
	}
}

func ReleaseAssetDownloadHandler(c *gin.Context) {
	streamProxiedFile(c)
}

func PackageFileDownloadHandler(c *gin.Context) {
	streamProxiedFile(c)
}
