package http

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/utils"
)

type ethicalPageview struct {
	SiteKey  string `json:"site_key" binding:"required"`
	Path     string `json:"path"`
	Referrer string `json:"referrer"`
	Browser  string `json:"browser"`
	OS       string `json:"os"`
	Device   string `json:"device"`
}

type ethicalBucket struct {
	Pageviews int64 `json:"pageviews"`
}

const maxEthicalSiteKeyLen = 64

var ethicalStore = newBoundedMap[int64](ethicalStoreMax, 24*time.Hour)

func encodeEthicalKey(site, key string) string {
	n := len(site)
	return strconv.Itoa(n) + ":" + site + ":" + key
}

func decodeEthicalKey(full string) (site, key string, ok bool) {
	i := strings.IndexByte(full, ':')
	if i <= 0 {
		return
	}
	n, err := strconv.Atoi(full[:i])
	if err != nil || n < 0 {
		return
	}
	if i+1+n >= len(full) || full[i+1+n] != ':' {
		return
	}
	site = full[i+1 : i+1+n]
	key = full[i+1+n+1:]
	return site, key, true
}

func EthicalMetricsPageviewHandler(c *gin.Context) {
	if c.GetHeader("DNT") == "1" {
		c.Status(http.StatusNoContent)
		return
	}
	var input ethicalPageview
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.SiteKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "site_key is required"})
		return
	}

	siteKey := input.SiteKey
	if len(siteKey) > maxEthicalSiteKeyLen {
		siteKey = siteKey[:maxEthicalSiteKeyLen]
	}

	path := normalizeEthicalPath(input.Path)
	browser := normalizeEthicalCategory(input.Browser, "Chrome", "Firefox", "Safari", "Edge")
	osName := normalizeEthicalCategory(input.OS, "Windows", "macOS", "Linux", "Android", "iOS")
	device := normalizeEthicalCategory(input.Device, "Desktop", "Mobile", "Tablet")
	country := ethicalCountry(c.Request)
	bucket := time.Now().UTC().Truncate(time.Minute).Format(time.RFC3339)

	key := strings.Join([]string{bucket, "page", path, browser, osName, device, country}, "|")
	fullKey := encodeEthicalKey(siteKey, key)
	ethicalStore.Update(fullKey, func(v int64, ok bool) int64 { return v + 1 })
	if dbClient != nil {
		if err := dbClient.UpsertEthicalPageview(c.Request.Context(), siteKey, bucket); err != nil {
			utils.Log("Error upserting pageview for %s/%s: %v", siteKey, bucket, err)
		}
	}
	c.Status(http.StatusAccepted)
}

func EthicalMetricsPrivacyHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version": "1.0", "last_updated": "2026-07-27",
		"stores_ip": false, "stores_user_agent": false, "stores_cookie": false,
		"stores_localstorage": false, "stores_session": false, "stores_fingerprint": false,
		"stores_query_strings": false, "stores_events": false,
		"transient_processing": gin.H{"ip": "used only for country lookup, then discarded"},
	})
}

func EthicalMetricsManifestHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"product": "EthicalMetrics", "version": "0.1.0", "protocol": "1", "api": "v1", "measurement": "pageviews", "tracking": false, "storage": "aggregated", "privacy": "/privacy"})
}

func EthicalMetricsVersionHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"product": "EthicalMetrics", "version": "0.1.0", "protocol": "1"})
}

func EthicalMetricsMetricsHandler(c *gin.Context) {
	site := strings.TrimPrefix(c.Param("site"), "")
	if site == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "site is required"})
		return
	}
	result := make(map[string]ethicalBucket)
	if dbClient != nil {
		if persisted, err := dbClient.GetEthicalPageviews(c.Request.Context(), site); err == nil {
			for bucket, count := range persisted {
				result[bucket] = ethicalBucket{Pageviews: count}
			}
		} else {
			utils.Log("Error loading pageviews for %s: %v", site, err)
		}
	}
	if len(result) == 0 {
		siteParam := site
		ethicalStore.Range(func(fullKey string, count int64) bool {
			s, inner, ok := decodeEthicalKey(fullKey)
			if !ok || s != siteParam {
				return true
			}
			parts := strings.SplitN(inner, "|", 2)
			if len(parts) == 2 {
				result[parts[0]] = ethicalBucket{Pageviews: result[parts[0]].Pageviews + count}
			}
			return true
		})
	}
	c.JSON(http.StatusOK, gin.H{"site": site, "measurement": "pageviews", "buckets": result})
}

func normalizeEthicalPath(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	if value == "" || !strings.HasPrefix(value, "/") {
		return "/" + strings.TrimPrefix(value, "/")
	}
	return value
}

func normalizeEthicalCategory(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "Other"
}

func ethicalCountry(r *http.Request) string {
	ip := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ip = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	_ = ip
	return "Unknown"
}
