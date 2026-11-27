package http

import (
	"fmt"
	"sync"
	"time"

	"github.com/livrasand/gitGost/internal/config"
)

// issueLimits holds the anonymous issue thresholds, set from config at startup.
type issueLimits struct {
	repoLimit   int
	repoWindow  time.Duration
	burstLimit  int
	burstWindow time.Duration
	dailyLimit  int
	dailyWindow time.Duration
}

const issueLimitStoreMax = 100000

var (
	issueMu        sync.Mutex
	issueCfg       = issueLimitsFrom(config.Load())
	issueIPStore   = newBoundedMap[[]time.Time](issueLimitStoreMax, issueCfg.maxWindow())
	issueRepoStore = newBoundedMap[[]time.Time](issueLimitStoreMax, issueCfg.maxWindow())
)

func issueLimitsFrom(cfg *config.Config) issueLimits {
	return issueLimits{
		repoLimit:   cfg.IssueRepoLimit,
		repoWindow:  cfg.IssueRepoWindow,
		burstLimit:  cfg.IssueIPBurstLimit,
		burstWindow: cfg.IssueIPBurstWindow,
		dailyLimit:  cfg.IssueIPDailyLimit,
		dailyWindow: cfg.IssueIPDailyWindow,
	}
}

func (l issueLimits) maxWindow() time.Duration {
	w := l.repoWindow
	if l.burstWindow > w {
		w = l.burstWindow
	}
	if l.dailyWindow > w {
		w = l.dailyWindow
	}
	return w
}

// setIssueLimits applies the configured thresholds. It is called once from SetupRouter.
func setIssueLimits(cfg *config.Config) {
	issueMu.Lock()
	defer issueMu.Unlock()
	issueCfg = issueLimitsFrom(cfg)
	issueIPStore = newBoundedMap[[]time.Time](issueLimitStoreMax, issueCfg.maxWindow())
	issueRepoStore = newBoundedMap[[]time.Time](issueLimitStoreMax, issueCfg.maxWindow())
}

// countWithin returns how many timestamps fall inside the window ending at now.
func countWithin(times []time.Time, now time.Time, window time.Duration) int {
	n := 0
	for _, t := range times {
		if now.Sub(t) < window {
			n++
		}
	}
	return n
}

// pruneOlderThan keeps only timestamps newer than the longest window.
func pruneOlderThan(times []time.Time, now time.Time, window time.Duration) []time.Time {
	out := times[:0]
	for _, t := range times {
		if now.Sub(t) < window {
			out = append(out, t)
		}
	}
	return out
}

// reserveIssueSlot checks the per-IP and per-repo limits and, if they allow it,
// records an attempt for both. It returns a reason when the request is refused.
// Recording happens before the provider call so concurrent requests cannot
// exceed a limit; failed attempts are released with releaseIssueSlot.
func reserveIssueSlot(ipKey, repoKey string, now time.Time) (string, bool) {
	issueMu.Lock()
	defer issueMu.Unlock()

	ipTimes, _ := issueIPStore.Peek(ipKey)
	ipTimes = pruneOlderThan(ipTimes, now, issueCfg.dailyWindow)
	repoTimes, _ := issueRepoStore.Peek(repoKey)
	repoTimes = pruneOlderThan(repoTimes, now, issueCfg.repoWindow)

	switch {
	case countWithin(ipTimes, now, issueCfg.burstWindow) >= issueCfg.burstLimit:
		return "rate limit exceeded: max " + fmt.Sprint(issueCfg.burstLimit) + " issue(s) per " + durationText(issueCfg.burstWindow) + " per IP", false
	case countWithin(ipTimes, now, issueCfg.dailyWindow) >= issueCfg.dailyLimit:
		return "rate limit exceeded: max " + fmt.Sprint(issueCfg.dailyLimit) + " issues per " + durationText(issueCfg.dailyWindow) + " per IP", false
	case countWithin(repoTimes, now, issueCfg.repoWindow) >= issueCfg.repoLimit:
		return "rate limit exceeded: max " + fmt.Sprint(issueCfg.repoLimit) + " issues per " + durationText(issueCfg.repoWindow) + " per repository", false
	}

	issueIPStore.Set(ipKey, append(ipTimes, now))
	issueRepoStore.Set(repoKey, append(repoTimes, now))
	return "", true
}

// releaseIssueSlot removes an attempt recorded by reserveIssueSlot, for requests
// that failed before an issue was created.
func releaseIssueSlot(ipKey, repoKey string, at time.Time) {
	issueMu.Lock()
	defer issueMu.Unlock()
	removeTime(issueIPStore, ipKey, at)
	removeTime(issueRepoStore, repoKey, at)
}

func removeTime(store *boundedMap[[]time.Time], key string, at time.Time) {
	times, ok := store.Peek(key)
	if !ok {
		return
	}
	for i := len(times) - 1; i >= 0; i-- {
		if times[i].Equal(at) {
			times = append(times[:i], times[i+1:]...)
			break
		}
	}
	store.Set(key, times)
}

// durationText renders a window for error messages, e.g. "10 minutes" or "6 hours".
func durationText(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	default:
		return d.String()
	}
}
