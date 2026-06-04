package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port           string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	APIKey         string
	GitHubToken    string
	GitLabToken    string
	LogFormat      string
	SupabaseURL    string
	SupabaseKey    string
	PanicPassword  string
	NtfyAdminTopic string
}

func Load() *Config {
	cfg := &Config{
		Port:           getEnv("PORT", "8080"),
		ReadTimeout:    getDurationEnv("READ_TIMEOUT", 30*time.Second),
		WriteTimeout:   getDurationEnv("WRITE_TIMEOUT", 30*time.Second),
		APIKey:         getEnv("GITGOST_API_KEY", ""),
		GitHubToken:    getEnv("GITHUB_TOKEN", ""),
		GitLabToken:    getEnv("GITLAB_TOKEN", ""),
		LogFormat:      getEnv("LOG_FORMAT", "text"),
		SupabaseURL:    getEnv("SUPABASE_URL", ""),
		SupabaseKey:    getEnv("SUPABASE_KEY", ""),
		PanicPassword:  getEnv("PANIC_PASSWORD", ""),
		NtfyAdminTopic: getEnv("NTFY_ADMIN_TOPIC", ""),
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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
