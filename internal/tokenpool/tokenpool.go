package tokenpool

import (
	"fmt"
	"os"
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
		t := os.Getenv(name)
		if t == "" {
			break
		}
		tokens = append(tokens, t)
	}
	return tokens
}

// NextGitHubToken returns the next available GitHub token in round-robin order.
// Tokens in cooldown (rate-limited) are skipped. Returns "" if none are configured.
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
	// All tokens are in cooldown; return the one that resets first.
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

// MarkGitHubRateLimited puts token in cooldown until resetAt (unix seconds).
// If resetAt is empty, a short default cooldown is applied.
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
