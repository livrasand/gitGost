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
	Port                string
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	GitHubToken         string
	GitLabToken         string
	CodebergToken       string
	LogFormat           string
	SupabaseURL         string
	SupabaseKey         string
	PanicPassword       string
	NtfyAdminTopic      string
	NtfyAnnounceTopic   string
	NtfyAnnounceToken   string
	MaintenanceMode     bool
	SponsorBanner       bool
	MentaAPIEndpoint    string
	MentaAPIKey         string
	MentaEnforceCaptcha bool
	GateKey             string
}

func Load() *Config {
	cfg := &Config{
		Port:                getEnv("PORT", "8080"),
		ReadTimeout:         getDurationEnv("READ_TIMEOUT", 30*time.Second),
		WriteTimeout:        getDurationEnv("WRITE_TIMEOUT", 30*time.Second),
		GitHubToken:         getEnv("GITHUB_TOKEN", ""),
		GitLabToken:         getEnv("GITLAB_TOKEN", ""),
		CodebergToken:       getEnv("CODEBERG_TOKEN", ""),
		LogFormat:           getEnv("LOG_FORMAT", "text"),
		SupabaseURL:         getEnv("SUPABASE_URL", ""),
		SupabaseKey:         getEnv("SUPABASE_KEY", ""),
		PanicPassword:       getEnv("PANIC_PASSWORD", ""),
		NtfyAdminTopic:      getEnv("NTFY_ADMIN_TOPIC", ""),
		NtfyAnnounceTopic:   getEnv("NTFY_ANNOUNCE_TOPIC", ""),
		NtfyAnnounceToken:   getEnv("NTFY_ANNOUNCE_TOKEN", ""),
		MaintenanceMode:     getEnv("MAINTENANCE_MODE", "") == "true",
		SponsorBanner:       getEnv("SPONSOR_BANNER", "") == "true",
		MentaAPIEndpoint:    getEnv("MENTA_API_ENDPOINT", ""),
		MentaAPIKey:         getEnv("MENTA_API_KEY", ""),
		MentaEnforceCaptcha: getEnv("MENTA_CAPTCHA_ENFORCE", "") == "true",
		GateKey:             getEnv("GITGOST_GATE_KEY", ""),
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

func getIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}
