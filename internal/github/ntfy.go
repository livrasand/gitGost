package github

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"time"
)

var ntfyClient = &http.Client{Timeout: 10 * time.Second}

// NtfyTopicForPR returns the ntfy topic for a given PR hash.
func NtfyTopicForPR(prHash string) string {
	return fmt.Sprintf("gitgost-%s", prHash)
}

// NtfyBaseURL returns the ntfy base URL (configurable via NTFY_BASE_URL, default ntfy.sh).
func NtfyBaseURL() string {
	if base := os.Getenv("NTFY_BASE_URL"); base != "" {
		return base
	}
	return "https://ntfy.sh"
}

// NtfyServiceURL returns the public-facing service URL used in admin action buttons.
// Configurable via SERVICE_URL env var; falls back to the default deployed URL.
func NtfyServiceURL() string {
	if u := os.Getenv("SERVICE_URL"); u != "" {
		return u
	}
	return "https://gitgost.leapcell.app"
}

// PublishNtfyEvent publishes an event to the ntfy topic corresponding to a PR hash.
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

// PublishNtfyAdmin publishes an admin alert with an optional ntfy action button.
// actions: ntfy Actions header value (e.g. HTTP POST button to activate panic mode).
// Pass empty string to send without action buttons.
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
