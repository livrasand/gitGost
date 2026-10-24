package announcement

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/livrasand/gitGost/internal/utils"
)

// ntfyEvent es un evento del stream JSON de ntfy (GET /{topic}/json).
// Solo nos interesan los "message"; open/keepalive/heartbeat se descartan.
type ntfyEvent struct {
	Event   string `json:"event"`
	Message string `json:"message"`
}

// Options agrupa la configuración del subscriber, para poder testear con un
// httptest.Server sin tocar variables de entorno.
type Options struct {
	BaseURL string
	Topic   string
	Token   string
	Client  *http.Client
}

// Run mantiene el streaming activo contra ntfy con reconexión exponencial
// (1s → 60s). Se detiene limpio cuando ctx se cancela. El estado vive solo en
// memoria: al reiniciar el proceso el subscriber aguarda el siguiente mensaje.
func Run(ctx context.Context, opts Options) {
	const (
		minBackoff = time.Second
		maxBackoff = 60 * time.Second
	)
	backoff := minBackoff
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 0}
	}
	url := fmt.Sprintf("%s/%s/json?poll=false", strings.TrimSuffix(opts.BaseURL, "/"), opts.Topic)
	utils.Log("announcement: subscriber connecting to ntfy topic")

	for {
		err := streamOnce(ctx, opts, url, processNtfyMessage)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			utils.Log("announcement: subscriber error: %v (retrying in %v)", err, backoff)
		} else {
			utils.Log("announcement: subscriber stream closed, reconnecting")
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if err != nil {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = minBackoff
		}
	}
}

// streamOnce abre la conexión HTTP streaming y la consume hasta cierre/error.
func streamOnce(ctx context.Context, opts Options, url string, handle func(raw []byte)) error {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 0}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/x-ndjson")
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}

	resp, err := opts.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy stream failed: status %s", resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev ntfyEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Event != "message" || ev.Message == "" {
			continue
		}
		handle([]byte(ev.Message))
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// processNtfyMessage valida y aplica el payload recibido. Mensajes
// malformados o no autorizados-ish (JSON inesperado) nunca crean banners.
func processNtfyMessage(raw []byte) {
	if err := Process(raw, time.Now().UTC()); err != nil {
		utils.Log("announcement: discarded message: %v", err)
		return
	}
	if a := Active(time.Now().UTC()); a != nil {
		utils.Log("announcement: active %s %q (expires %s)", a.Type, a.Title, a.ExpiresAt.UTC().Format(time.RFC3339))
	} else {
		utils.Log("announcement: cleared via ntfy")
	}
}
