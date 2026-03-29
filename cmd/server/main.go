package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

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
		// Try common environment variables
		envVars := []string{
			"COMMIT_HASH",
			"LEAPCELL_COMMIT_SHA",
			"GITHUB_SHA",
			"GIT_COMMIT",
			"COMMIT_SHA",
			"GIT_SHA",
			"VERCEL_GIT_COMMIT_SHA",
			"RENDER_GIT_COMMIT",
		}
		for _, v := range envVars {
			if val := os.Getenv(v); val != "" {
				commitHash = val
				break
			}
		}

		// Try execute git command if still not set (with timeout)
		if commitHash == "main" || commitHash == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD")
			if out, err := cmd.Output(); err == nil {
				commitHash = strings.TrimSpace(string(out))
			}
		}

		// Try Go build info
		if commitHash == "main" || commitHash == "" {
			if info, ok := debug.ReadBuildInfo(); ok {
				for _, setting := range info.Settings {
					if setting.Key == "vcs.revision" {
						commitHash = setting.Value
						if len(commitHash) > 7 {
							commitHash = commitHash[:7]
						}
						break
					}
				}
			}
		}
	}

	// Set a reasonable build time if unknown
	if buildTime == "unknown" || buildTime == "" {
		buildTime = time.Now().UTC().Format(time.RFC3339)
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

	// Initialize panic button
	handler.InitPanicConfig(cfg.PanicPassword, cfg.NtfyAdminTopic)

	// Setup router
	router := handler.SetupRouter(cfg)

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Port)
	utils.Log("Starting server on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
