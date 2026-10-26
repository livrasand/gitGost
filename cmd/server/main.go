package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/livrasand/gitGost/internal/announcement"
	"github.com/livrasand/gitGost/internal/config"
	handler "github.com/livrasand/gitGost/internal/http"
	"github.com/livrasand/gitGost/internal/utils"

	"github.com/joho/godotenv"
)

// getenvDefault es getEnv de config pero solo desde el entorno nativo (el
// .env ya fue cargado por godotenv antes de llegar aquí).
func getenvDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

var (
	commitHash = "main"
	buildTime  = "unknown"
	sourceRepo = "https://github.com/livrasand/gitGost"
)

func main() {
	// Load .env file (ignore error if file doesn't exist)
	_ = godotenv.Load()

	// Fallback a variables de entorno si el valor -ldflags no fue inyectado correctamente
	if commitHash == "main" || commitHash == "" {
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

		// Try execute git command if still not set
		if commitHash == "main" || commitHash == "" {
			cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
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

	// Initialize Menta CAPTCHA
	handler.InitMentaConfig(cfg.MentaAPIEndpoint, cfg.MentaAPIKey, cfg.MentaEnforceCaptcha)

	// Initialize the CAPTCHA gate signing key. A stable key keeps the
	// "gost_gate" sessions valid across restarts: with a random key every
	// restart invalidates every visitor session.
	if secret := gateSecret(cfg.GateKey); secret != nil {
		handler.InitCaptchaGate(secret)
	}

	// Initialize panic button
	handler.InitPanicConfig(cfg.PanicPassword, cfg.NtfyAdminTopic)
	if cfg.NtfyAdminTopic != "" {
		if os.Getenv("NTFY_TOKEN") == "" {
			utils.Log("WARNING: NTFY_TOKEN is unset: the admin ntfy topic is publicly readable/writable. Action tokens (panic/rollback) are exposed to anyone who learns NTFY_ADMIN_TOPIC")
		}
		if len(cfg.NtfyAdminTopic) < 24 {
			utils.Log("WARNING: NTFY_ADMIN_TOPIC looks guessable: use a long random value (e.g. 32+ chars) to prevent topic enumeration")
		}
	}

	// Announcement banners via ntfy/UnifiedPush: el subscriber consume el
	// topic privado (NTFY_ANNOUNCE_TOPIC) y mantiene solo un anuncio activo
	// en memoria. Sin topic configurado, el feature queda apagado.
	if cfg.NtfyAnnounceTopic != "" {
		subCtx, subCancel := context.WithCancel(context.Background())
		defer subCancel()
		go announcement.Run(subCtx, announcement.Options{
			BaseURL: getenvDefault("NTFY_BASE_URL", "https://ntfy.sh"),
			Topic:   cfg.NtfyAnnounceTopic,
			Token:   cfg.NtfyAnnounceToken,
		})
		if cfg.NtfyAnnounceToken == "" {
			utils.Log("WARNING: NTFY_ANNOUNCE_TOKEN is unset: the announce topic is publicly readable/writable. Anyone who learns NTFY_ANNOUNCE_TOPIC can publish banners")
		}
		if len(cfg.NtfyAnnounceTopic) < 24 {
			utils.Log("WARNING: NTFY_ANNOUNCE_TOPIC looks guessable: use a long random value (e.g. 32+ chars) to prevent topic enumeration")
		}
	} else {
		utils.Log("Announcement banners disabled (NTFY_ANNOUNCE_TOPIC unset)")
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

func gateKeyPath() string {
	if v := os.Getenv("GITGOST_HOME"); v != "" {
		return v + "/gate.key"
	}
	return os.ExpandEnv("$HOME/.gitgost/gate.key")
}

// gateSecret resolves the secret used to sign the CAPTCHA session cookie:
// GITGOST_GATE_KEY first, then the persisted key file, and finally a new
// random key that is written to that file so it survives restarts.
func gateSecret(configured string) []byte {
	if key := strings.TrimSpace(configured); key != "" {
		return []byte(key)
	}
	path := gateKeyPath()
	if data, err := os.ReadFile(path); err == nil {
		if key := strings.TrimSpace(string(data)); key != "" {
			return []byte(key)
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		utils.Log("WARNING: could not generate the CAPTCHA gate key: %v", err)
		return nil
	}
	encoded := hex.EncodeToString(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		utils.Log("WARNING: could not create the CAPTCHA gate key directory: %v", err)
		return []byte(encoded)
	}
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		utils.Log("WARNING: could not persist the CAPTCHA gate key: %v", err)
		return []byte(encoded)
	}
	utils.Log("CAPTCHA gate key generated and stored in %s", path)
	return []byte(encoded)
}
