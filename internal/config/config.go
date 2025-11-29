package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for the application
type Config struct {
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	APIKey       string
	GitHubToken  string
	LogFormat    string // "text" or "json"
}

// Load reads configuration from environment variables with defaults
func Load() *Config {
	cfg := &Config{
		Port:         getEnv("PORT", "8080"),
		ReadTimeout:  getDurationEnv("READ_TIMEOUT", 30*time.Second),
		WriteTimeout: getDurationEnv("WRITE_TIMEOUT", 30*time.Second),
		APIKey:       getEnv("GITGOST_API_KEY", ""),
		GitHubToken:  getEnv("GITHUB_TOKEN", ""),
		LogFormat:    getEnv("LOG_FORMAT", "text"), // "text" or "json"
	}

	return cfg
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getDurationEnv gets a duration environment variable or returns a default value
func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// getIntEnv gets an integer environment variable or returns a default value
func getIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}
