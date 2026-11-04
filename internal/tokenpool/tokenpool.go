package tokenpool

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	mu       sync.Mutex
	index    uint64
	cooldown = map[string]time.Time{}
)

func GitHubTokens() []string {
	var tokens []string
	for i := 1; ; i++ {
		name := "GITHUB_TOKEN"
		if i > 1 {
			name = fmt.Sprintf("GITHUB%d_TOKEN", i)
		}
		t := strings.TrimSpace(os.Getenv(name))
		if t == "" {
			break
		}
		if isPlaceholder(t) {
			continue
		}
		tokens = append(tokens, t)
	}
	return tokens
}

func isPlaceholder(token string) bool {
	return strings.HasPrefix(token, "your_") || strings.HasPrefix(token, "your-")
}

func NextGitHubToken() string {
	tokens := GitHubTokens()
	if len(tokens) == 0 {
		return ""
	}
	mu.Lock()
	defer mu.Unlock()
	n := uint64(len(tokens))
	now := time.Now()
	for i := uint64(0); i < n; i++ {
		idx := (index + i) % n
		t := tokens[idx]
		if until, ok := cooldown[t]; ok && now.Before(until) {
			continue
		}
		index = idx + 1
		return t
	}
	var best string
	var bestUntil time.Time
	for _, t := range tokens {
		until := cooldown[t]
		if best == "" || until.Before(bestUntil) {
			best, bestUntil = t, until
		}
	}
	return best
}

func MarkGitHubRateLimited(token, resetAt string) {
	if token == "" {
		return
	}
	until := time.Now().Add(60 * time.Second)
	if resetAt != "" {
		var ts int64
		if _, err := fmt.Sscanf(resetAt, "%d", &ts); err == nil && ts > 0 {
			until = time.Unix(ts, 0)
		}
	}
	mu.Lock()
	cooldown[token] = until
	mu.Unlock()
}
