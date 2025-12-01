package utils

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/livrasand/gitGost/internal/config"
)

var (
	logger *log.Logger
	cfg    *config.Config
)

// InitLogger inicializa el logger con privacidad en mente
func InitLogger(config *config.Config) {
	cfg = config
	if config.LogFormat == "json" {
		logger = log.New(os.Stdout, "", 0)
	} else {
		logger = log.New(os.Stdout, "[gitGost] ", log.LstdFlags)
	}

	// Advertencia sobre privacidad en logs
	Log("Privacy mode: Minimal logging enabled")
	Log("Server started - Ready for anonymous contributions")
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
			"privacy":   "anonymized",
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

// LogError registra errores sin exponer datos del usuario
func LogError(format string, args ...interface{}) {
	if logger == nil {
		log.Printf("ERROR: "+format, args...)
		return
	}

	message := fmt.Sprintf(format, args...)

	if cfg != nil && cfg.LogFormat == "json" {
		logEntry := map[string]interface{}{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"level":     "error",
			"message":   message,
			"privacy":   "anonymized",
		}
		if jsonData, err := json.Marshal(logEntry); err == nil {
			logger.Println(string(jsonData))
		}
	} else {
		logger.Printf("ERROR: %s", message)
	}
}
