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
	Port             string
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	GitHubToken      string
	GitLabToken      string
	CodebergToken    string
	LogFormat        string
	SupabaseURL      string
	SupabaseKey      string
	PanicPassword    string
	NtfyAdminTopic  string
	MaintenanceMode     bool
	SponsorBanner       bool
	MentaAPIEndpoint    string
	MentaAPIKey         string
	MentaEnforceCaptcha bool
	GateKey             string
}

func Load() *Config {
	cfg := &Config{
		Port:             getEnv("PORT", "8080"),
		ReadTimeout:      getDurationEnv("READ_TIMEOUT", 30*time.Second),
		WriteTimeout:     getDurationEnv("WRITE_TIMEOUT", 30*time.Second),
		GitHubToken:      getEnv("GITHUB_TOKEN", ""),
		GitLabToken:      getEnv("GITLAB_TOKEN", ""),
		CodebergToken:    getEnv("CODEBERG_TOKEN", ""),
		LogFormat:        getEnv("LOG_FORMAT", "text"),
		SupabaseURL:      getEnv("SUPABASE_URL", ""),
		SupabaseKey:      getEnv("SUPABASE_KEY", ""),
		PanicPassword:    getEnv("PANIC_PASSWORD", ""),
		NtfyAdminTopic:  getEnv("NTFY_ADMIN_TOPIC", ""),
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

// Los marcadores contra los que limpia sanitize() se leen del propio
// .env.example en lugar de estar escritos a mano en el código: si el archivo
// cambia (nuevos valores de ejemplo), el sanitizador se adapta solo.
var (
	exampleOnce    sync.Once
	exampleMarkers []string
)

func exampleMarkerValues() []string {
	exampleOnce.Do(func() { exampleMarkers = loadExampleMarkers() })
	return exampleMarkers
}

// loadExampleMarkers busca .env.example cerca del directorio de trabajo y
// devuelve sus valores que parecen placeholders sin rellenar ("your_github_token_here",
// "https://your-project.supabase.co", …). Los valores con apariencia de valor
// real (p. ej. GITHUB_TOKEN=ghp_xxx) no se consideran basura.
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

// sanitize descarta los valores de ejemplo del .env.example ("your_github_token_here",
// "https://your-project.supabase.co", …): si alguien copia el ejemplo sin rellenar,
// tratamos lo mismo que vacío en lugar de enviar basura a las APIs upstream.
// Los marcadores exactos se leen de .env.example (exampleMarkerValues); los
// prefijos "your_" / "your-" quedan como red de seguridad por si el archivo no
// está disponible en el directorio de trabajo del binario.
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
