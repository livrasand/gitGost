package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/livrasand/gitGost/internal/config"
	handler "github.com/livrasand/gitGost/internal/http"
	"github.com/livrasand/gitGost/internal/utils"

	"github.com/joho/godotenv"
)

var (
	commitHash = "main"
	buildTime  = "unknown"
	sourceRepo = "https://github.com/livrasand/gitGost"
)

func main() {
	// Load .env file (ignore error if file doesn't exist)
	_ = godotenv.Load()

	// Fallback a variable de entorno si el valor -ldflags no fue inyectado
	if commitHash == "main" {
		if v := os.Getenv("COMMIT_HASH"); v != "" {
			commitHash = v
		}
	}

	// Inject build info (set via -ldflags at compile time or env var)
	handler.SetBuildInfo(commitHash, buildTime, sourceRepo)

	// Load configuration
	cfg := config.Load()

	// Initialize logger
	utils.InitLogger(cfg)

	// Initialize database
	if cfg.SupabaseURL != "" && cfg.SupabaseKey != "" {
		handler.InitDatabase(cfg.SupabaseURL, cfg.SupabaseKey)
		utils.Log("Supabase database initialized (Central Europe - Zurich)")
	} else {
		utils.Log("Warning: Supabase not configured, stats will not be persisted")
	}

	// Setup router
	router := handler.SetupRouter(cfg)

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Port)
	utils.Log("Starting server on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
