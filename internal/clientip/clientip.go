// Package clientip extracts the real submitter IP and normalizes it into a
// rate-limit key.
package clientip

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

// From returns the submitter's IP address. Fly.io sets Fly-Client-IP to the
// real client address on every request it proxies, so that header is preferred.
// X-Forwarded-For and then RemoteAddr are used as fallbacks.
func From(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("Fly-Client-IP")); net.ParseIP(v) != nil {
		return v
	}
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if net.ParseIP(first) != nil {
			return first
		}
	}
	return c.ClientIP()
}

// RateLimitKey normalizes an IP for use as a rate-limit bucket. IPv4 addresses
// are used as-is. An IPv6 address is replaced by its /64 prefix, because a single
// visitor usually controls the whole /64 and could otherwise rotate addresses to
// bypass per-IP limits.
func RateLimitKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// Key returns the normalized rate-limit key for the request's submitter.
func Key(c *gin.Context) string {
	return RateLimitKey(From(c))
}
