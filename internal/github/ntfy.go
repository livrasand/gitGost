package github

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"time"
)

var ntfyClient = &http.Client{Timeout: 10 * time.Second}

// NtfyTopicForPR retorna el topic ntfy para un PR hash dado.
// Formato: gitgost-{hash}
func NtfyTopicForPR(prHash string) string {
	return fmt.Sprintf("gitgost-%s", prHash)
}

// NtfyBaseURL retorna la base URL de ntfy (configurable vía NTFY_BASE_URL, default ntfy.sh).
func NtfyBaseURL() string {
	if base := os.Getenv("NTFY_BASE_URL"); base != "" {
		return base
	}
	return "https://ntfy.sh"
}

// PublishNtfyEvent publica un evento al topic ntfy correspondiente al PR hash.
// title: título de la notificación
// message: cuerpo del mensaje
// prHash: hash del PR (8 chars)
func PublishNtfyEvent(prHash, title, message string) error {
	topic := NtfyTopicForPR(prHash)
	url := fmt.Sprintf("%s/%s", NtfyBaseURL(), topic)

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "bell")
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

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
