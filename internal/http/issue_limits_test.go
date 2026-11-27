package http

import (
	"strings"
	"testing"
	"time"

	"github.com/livrasand/gitGost/internal/clientip"
	"github.com/livrasand/gitGost/internal/config"
)

func testIssueLimits(t *testing.T, repoLimit, burst, daily int) {
	t.Helper()
	cfg := &config.Config{
		IssueRepoLimit:     repoLimit,
		IssueRepoWindow:    6 * time.Hour,
		IssueIPBurstLimit:  burst,
		IssueIPBurstWindow: 10 * time.Minute,
		IssueIPDailyLimit:  daily,
		IssueIPDailyWindow: 24 * time.Hour,
	}
	setIssueLimits(cfg)
}

func TestIssueBurstLimitPerIP(t *testing.T) {
	testIssueLimits(t, 100, 1, 6)
	t0 := time.Now()
	if _, ok := reserveIssueSlot("203.0.113.1", "/v1/gh/a/one", t0); !ok {
		t.Fatal("first issue should be allowed")
	}
	reason, ok := reserveIssueSlot("203.0.113.1", "/v1/gh/a/two", t0.Add(5*time.Minute))
	if ok {
		t.Fatal("second issue within 10 minutes should be refused")
	}
	if !strings.Contains(reason, "10 minutes") {
		t.Errorf("reason = %q, want it to mention the burst window", reason)
	}
	if _, ok := reserveIssueSlot("203.0.113.1", "/v1/gh/a/two", t0.Add(11*time.Minute)); !ok {
		t.Error("issue after the burst window should be allowed")
	}
}

func TestIssueDailyLimitPerIP(t *testing.T) {
	testIssueLimits(t, 100, 1, 6)
	t0 := time.Now()
	for i := 0; i < 6; i++ {
		at := t0.Add(time.Duration(i) * 11 * time.Minute)
		if _, ok := reserveIssueSlot("198.51.100.2", "/v1/gh/b/repo", at); !ok {
			t.Fatalf("issue %d should be allowed", i+1)
		}
	}
	reason, ok := reserveIssueSlot("198.51.100.2", "/v1/gh/b/repo", t0.Add(66*time.Minute))
	if ok {
		t.Fatal("7th issue in a day should be refused")
	}
	if !strings.Contains(reason, "24 hours") {
		t.Errorf("reason = %q, want it to mention the daily window", reason)
	}
}

func TestIssueRepoLimitAcrossIPs(t *testing.T) {
	testIssueLimits(t, 2, 100, 100)
	t0 := time.Now()
	if _, ok := reserveIssueSlot("192.0.2.1", "/v1/gh/c/shared", t0); !ok {
		t.Fatal("1st issue should be allowed")
	}
	if _, ok := reserveIssueSlot("192.0.2.2", "/v1/gh/c/shared", t0); !ok {
		t.Fatal("2nd issue from another IP should be allowed")
	}
	reason, ok := reserveIssueSlot("192.0.2.3", "/v1/gh/c/shared", t0)
	if ok {
		t.Fatal("3rd issue to the same repo should be refused across IPs")
	}
	if !strings.Contains(reason, "per repository") {
		t.Errorf("reason = %q", reason)
	}
	if _, ok := reserveIssueSlot("192.0.2.3", "/v1/gh/c/shared", t0.Add(6*time.Hour+time.Second)); !ok {
		t.Error("repo slot should free up after the 6h window")
	}
}

func TestIssueIPv6SubnetSharesBucket(t *testing.T) {
	testIssueLimits(t, 100, 1, 6)
	t0 := time.Now()
	a := "2001:db8:1:2::1"
	b := "2001:db8:1:2:ffff:ffff:ffff:ffff"
	if _, ok := reserveIssueSlot(clientip.RateLimitKey(a), "/v1/gh/d/e", t0); !ok {
		t.Fatal("first address should be allowed")
	}
	if _, ok := reserveIssueSlot(clientip.RateLimitKey(b), "/v1/gh/d/e", t0.Add(time.Minute)); ok {
		t.Error("another address in the same /64 should share the burst limit")
	}
}

func TestReleaseSlotAfterFailedRequest(t *testing.T) {
	testIssueLimits(t, 1, 100, 100)
	t0 := time.Now()
	if _, ok := reserveIssueSlot("192.0.2.9", "/v1/gh/f/g", t0); !ok {
		t.Fatal("first issue should be allowed")
	}
	releaseIssueSlot("192.0.2.9", "/v1/gh/f/g", t0)
	if _, ok := reserveIssueSlot("192.0.2.9", "/v1/gh/f/g", t0); !ok {
		t.Error("released slot should be available again")
	}
}

func TestIssueLimitsFromEnv(t *testing.T) {
	t.Setenv("ISSUE_REPO_LIMIT", "5")
	t.Setenv("ISSUE_IP_BURST_WINDOW", "1m")
	t.Setenv("ISSUE_IP_DAILY_LIMIT", "0") // invalid, falls back to default
	cfg := config.Load()
	if cfg.IssueRepoLimit != 5 {
		t.Errorf("IssueRepoLimit = %d, want 5", cfg.IssueRepoLimit)
	}
	if cfg.IssueIPBurstWindow != time.Minute {
		t.Errorf("IssueIPBurstWindow = %v, want 1m", cfg.IssueIPBurstWindow)
	}
	if cfg.IssueIPDailyLimit != 6 {
		t.Errorf("IssueIPDailyLimit = %d, want default 6", cfg.IssueIPDailyLimit)
	}
	if cfg.IssueRepoWindow != 6*time.Hour {
		t.Errorf("IssueRepoWindow = %v, want default 6h", cfg.IssueRepoWindow)
	}
}
