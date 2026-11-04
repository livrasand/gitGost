package announcement

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	apiLimiterMax  = 30
	apiLimiterWin  = time.Minute
	apiLimiterSize = 10000
)

type limiterEntry struct {
	count  int
	expiry time.Time
}

var (
	apiLimiterMu    sync.Mutex
	apiLimiterStore = make(map[string]*limiterEntry, apiLimiterSize)
)

func RateLimit() gin.HandlerFunc {
	return rateLimitWith(apiLimiterMax, apiLimiterWin)
}

func rateLimitWith(max int, win time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()
		apiLimiterMu.Lock()
		if e, ok := limiterGet(ip); ok && now.Before(e.expiry) {
			e.count++
			if e.count > max {
				apiLimiterMu.Unlock()
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "announcement rate limit exceeded"})
				return
			}
		} else {
			if len(apiLimiterStore) >= apiLimiterSize {
				for k, v := range apiLimiterStore {
					if now.After(v.expiry) {
						delete(apiLimiterStore, k)
					}
				}
				if len(apiLimiterStore) >= apiLimiterSize {
					apiLimiterMu.Unlock()
					c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "announcement rate limit exceeded"})
					return
				}
			}
			apiLimiterStore[ip] = &limiterEntry{count: 1, expiry: now.Add(win)}
		}
		apiLimiterMu.Unlock()
		c.Next()
	}
}

func limiterGet(ip string) (*limiterEntry, bool) {
	e, ok := apiLimiterStore[ip]
	return e, ok
}

func Handler(c *gin.Context) {
	a := Active(time.Now().UTC())
	if a == nil {
		c.Header("Cache-Control", "no-store")
		c.Status(http.StatusNoContent)
		return
	}
	expiry := ""
	if a.ExpiresAt != nil {
		expiry = a.ExpiresAt.UTC().Format(time.RFC3339)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"id":         a.ID,
		"type":       a.Type,
		"title":      a.Title,
		"message":    a.Message,
		"link":       a.Link,
		"expires_at": expiry,
	})
}
