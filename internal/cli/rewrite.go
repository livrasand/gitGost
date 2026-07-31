package cli

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// ServerBase devuelve la URL base del servidor gitGost (env GITGOST_SERVER o default).
func ServerBase() string {
	if v := os.Getenv("GITGOST_SERVER"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://gitgost.fly.dev"
}

// hostPrefix mapea el host del repositorio al prefijo de ruta del servidor gitGost.
var hostPrefix = map[string]string{
	"github.com":   "gh",
	"gitlab.com":   "gl",
	"codeberg.org": "cb",
}

// RewriteURL convierte una URL de repositorio (https o scp-like SSH) en la ruta
// equivalente del servidor gitGost: /v1/<prefix>/owner/repo. Así el tráfico de
// clone/fetch pasa por gitGost sin tocar la URL original del usuario.
func RewriteURL(base, raw string) (string, error) {
	base = strings.TrimRight(base, "/")
	u, err := parseRepoURL(raw)
	if err != nil {
		return "", err
	}

	prefix, ok := hostPrefix[u.Hostname()]
	if !ok {
		return "", fmt.Errorf("host no soportado por gitGost: %s", u.Hostname())
	}

	parts := splitPath(u.Path)
	if len(parts) != 2 {
		return "", fmt.Errorf("URL de repositorio inválida: %s", raw)
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" || !validSegment(owner) || !validSegment(repo) {
		return "", fmt.Errorf("URL de repositorio inválida: %s", raw)
	}

	return fmt.Sprintf("%s/v1/%s/%s/%s", base, prefix, owner, repo), nil
}

// parseRepoURL acepta URLs https://host/owner/repo y scp-like git@host:owner/repo.git.
func parseRepoURL(raw string) (*url.URL, error) {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("URL inválida: %w", err)
		}
		if u.Hostname() == "" {
			return nil, fmt.Errorf("URL inválida: %s", raw)
		}
		return u, nil
	}

	// scp-like: user@host:owner/repo(.git)
	at := strings.LastIndex(raw, "@")
	colon := strings.Index(raw, ":")
	if at >= 0 && colon > at {
		host := raw[at+1 : colon]
		path := raw[colon+1:]
		return &url.URL{Scheme: "https", Host: host, Path: "/" + path}, nil
	}
	return nil, fmt.Errorf("URL inválida: %s", raw)
}

func splitPath(p string) []string {
	var out []string
	for _, part := range strings.Split(p, "/") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// validSegment valida un segmento owner/repo (alfanumérico, -, _, .).
func validSegment(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return !strings.Contains(s, "..")
}
