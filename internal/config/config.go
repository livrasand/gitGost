package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Port              string
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	GitHubToken       string
	GitLabToken       string
	CodebergToken     string
	LogFormat         string
	SupabaseURL       string
	SupabaseKey       string
	PanicPassword     string
	NtfyAdminTopic    string
	NtfyAnnounceTopic string
	NtfyAnnounceToken string
	MaintenanceMode   bool
	SponsorBanner     bool
	GateKey           string

	// Anonymous issue limits. All are configurable so they can be relaxed or
	// tightened without a redeploy.
	IssueRepoLimit     int
	IssueRepoWindow    time.Duration
	IssueIPBurstLimit  int
	IssueIPBurstWindow time.Duration
	IssueIPDailyLimit  int
	IssueIPDailyWindow time.Duration
}

func Load() *Config {
	cfg := &Config{
		Port:               getEnv("PORT", "8080"),
		ReadTimeout:        getDurationEnv("READ_TIMEOUT", 30*time.Second),
		WriteTimeout:       getDurationEnv("WRITE_TIMEOUT", 30*time.Second),
		GitHubToken:        getEnv("GITHUB_TOKEN", ""),
		GitLabToken:        getEnv("GITLAB_TOKEN", ""),
		CodebergToken:      getEnv("CODEBERG_TOKEN", ""),
		LogFormat:          getEnv("LOG_FORMAT", "text"),
		SupabaseURL:        getEnv("SUPABASE_URL", ""),
		SupabaseKey:        getEnv("SUPABASE_KEY", ""),
		PanicPassword:      getEnv("PANIC_PASSWORD", ""),
		NtfyAdminTopic:     getEnv("NTFY_ADMIN_TOPIC", ""),
		NtfyAnnounceTopic:  getEnv("NTFY_ANNOUNCE_TOPIC", ""),
		NtfyAnnounceToken:  getEnv("NTFY_ANNOUNCE_TOKEN", ""),
		MaintenanceMode:    getEnv("MAINTENANCE_MODE", "") == "true",
		SponsorBanner:      getEnv("SPONSOR_BANNER", "") == "true",
		GateKey:            getEnv("GITGOST_GATE_KEY", ""),
		IssueRepoLimit:     positiveIntEnv("ISSUE_REPO_LIMIT", 2),
		IssueRepoWindow:    getDurationEnv("ISSUE_REPO_WINDOW", 6*time.Hour),
		IssueIPBurstLimit:  positiveIntEnv("ISSUE_IP_BURST_LIMIT", 1),
		IssueIPBurstWindow: getDurationEnv("ISSUE_IP_BURST_WINDOW", 10*time.Minute),
		IssueIPDailyLimit:  positiveIntEnv("ISSUE_IP_DAILY_LIMIT", 6),
		IssueIPDailyWindow: getDurationEnv("ISSUE_IP_DAILY_WINDOW", 24*time.Hour),
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := sanitize(os.Getenv(key)); value != "" {
		return value
	}
	return defaultValue
}

var (
	exampleOnce    sync.Once
	exampleMarkers []string
)

func exampleMarkerValues() []string {
	exampleOnce.Do(func() { exampleMarkers = loadExampleMarkers() })
	return exampleMarkers
}

func loadExampleMarkers() []string {
	var markers []string
	for _, path := range []string{".env.example", "../.env.example", "../../.env.example"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq < 0 {
				continue
			}
			value := strings.TrimSpace(line[eq+1:])
			if looksLikePlaceholder(value) {
				markers = append(markers, value)
			}
		}
		f.Close()
		break
	}
	return markers
}

func looksLikePlaceholder(value string) bool {
	return strings.Contains(value, "your_") || strings.Contains(value, "your-")
}

func sanitize(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	for _, marker := range exampleMarkerValues() {
		if strings.Contains(v, marker) {
			return ""
		}
	}
	if strings.HasPrefix(v, "your_") || strings.HasPrefix(v, "your-") {
		return ""
	}
	return v
}

func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// positiveIntEnv returns the integer value of key, or defaultValue when the
// variable is unset, invalid, or not greater than zero.
func positiveIntEnv(key string, defaultValue int) int {
	if n := getIntEnv(key, defaultValue); n > 0 {
		return n
	}
	return defaultValue
}

func getIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}
