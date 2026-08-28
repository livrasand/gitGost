package github

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var ntfyClient = &http.Client{Timeout: 10 * time.Second}

func NtfyTopicForPR(prHash string) string {
	return fmt.Sprintf("gitgost-%s", prHash)
}

// NtfyTopicForIssue devuelve el topic de ntfy asociado a una issue. El topic es
// determinista a partir de provider/owner/repo/número para que el cliente pueda
// suscribirse sin una cuenta y el servidor publicar las novedades de la issue.
func NtfyTopicForIssue(owner, repo, number string) string {
	return fmt.Sprintf("gitgost-%s-%s-issue-%s", sanitizeTopicSegment(owner), sanitizeTopicSegment(repo), number)
}

// sanitizeTopicSegment limpia un segmento del topic para que solo contenga
// caracteres seguros en un nombre de topic de ntfy ([a-zA-Z0-9_-]).
func sanitizeTopicSegment(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func NtfyBaseURL() string {
	if base := os.Getenv("NTFY_BASE_URL"); base != "" {
		return base
	}
	return "https://ntfy.sh"
}

func NtfyServiceURL() string {
	if u := os.Getenv("SERVICE_URL"); u != "" {
		return u
	}
	return "https://gitgost.fly.dev"
}

// NtfyToken devuelve un token opcional (NTFY_TOKEN) para autenticar las
// publicaciones contra un servidor ntfy con control de acceso. Sin él,
// cualquier persona que conozca el topic puede leerlo y publicar en él.
func NtfyToken() string {
	return os.Getenv("NTFY_TOKEN")
}

func setNtfyAuth(req *http.Request) {
	if tok := NtfyToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

func PublishNtfyEvent(prHash, title, message, actions string) error {
	topic := NtfyTopicForPR(prHash)
	url := fmt.Sprintf("%s/%s", NtfyBaseURL(), topic)

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "bell")
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if actions != "" {
		req.Header.Set("Actions", actions)
	}
	setNtfyAuth(req)

	resp, err := ntfyClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy publish failed: status %s", resp.Status)
	}

	return nil
}

// PublishNtfyIssueComment publica una actualización en el topic de una issue
// (p. ej. un comentario nuevo) para que los suscriptores del topic reciban la
// novedad. El topic es determinista: NtfyTopicForIssue.
func PublishNtfyIssueComment(owner, repo, number, title, message, actions string) error {
	topic := NtfyTopicForIssue(owner, repo, number)
	url := fmt.Sprintf("%s/%s", NtfyBaseURL(), topic)

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "speech_balloon")
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if actions != "" {
		req.Header.Set("Actions", actions)
	}
	setNtfyAuth(req)

	resp, err := ntfyClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy issue publish failed: status %s", resp.Status)
	}

	return nil
}

func PublishNtfyAdmin(topic, title, message, actions string) error {
	url := fmt.Sprintf("%s/%s", NtfyBaseURL(), topic)

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "rotating_light")
	req.Header.Set("Priority", "high")
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if actions != "" {
		req.Header.Set("Actions", actions)
	}
	setNtfyAuth(req)

	resp, err := ntfyClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy admin publish failed: status %s", resp.Status)
	}

	return nil
}
