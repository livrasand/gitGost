package utils

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"gitGost/internal/config"
)

var (
	logger *log.Logger
	cfg    *config.Config
)

// InitLogger initializes the logger with the given configuration
func InitLogger(config *config.Config) {
	cfg = config
	if config.LogFormat == "json" {
		logger = log.New(os.Stdout, "", 0) // No prefix for JSON
	} else {
		logger = log.New(os.Stdout, "[gitGost] ", log.LstdFlags)
	}
}

func Log(format string, args ...interface{}) {
	if logger == nil {
		// Fallback if not initialized
		log.Printf(format, args...)
		return
	}

	message := fmt.Sprintf(format, args...)

	if cfg != nil && cfg.LogFormat == "json" {
		logEntry := map[string]interface{}{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"level":     "info",
			"message":   message,
		}
		if jsonData, err := json.Marshal(logEntry); err == nil {
			logger.Println(string(jsonData))
		} else {
			logger.Printf("Failed to marshal log entry: %v", err)
		}
	} else {
		logger.Print(message)
	}
}
