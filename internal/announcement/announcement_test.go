package announcement

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func reset() {
	Clear()
}

func mustProcess(t *testing.T, raw string, now time.Time) {
	t.Helper()
	if err := Process([]byte(raw), now); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
}

func expectActive(t *testing.T, typ, title string) *Announcement {
	t.Helper()
	a := Active(time.Now().UTC())
	if a == nil {
		t.Fatalf("expected active announcement")
	}
	if a.Type != typ || a.Title != title {
		t.Fatalf("got %s/%q, want %s/%q", a.Type, a.Title, typ, title)
	}
	return a
}

func TestPostAndClear(t *testing.T) {
	reset()
	defer reset()
	now := time.Now().UTC()

	mustProcess(t, `{"action":"post","type":"warning","title":"GitHub issues unavailable","message":"Issue pages may not load."}`, now)
	a := expectActive(t, "warning", "GitHub issues unavailable")
	if a.ExpiresAt == nil || time.Until(*a.ExpiresAt) > 26*time.Hour || time.Until(*a.ExpiresAt) < 22*time.Hour {
		t.Fatalf("default TTL wrong: %v", a.ExpiresAt)
	}
	if a.Link != "" {
		t.Fatalf("expected empty link")
	}

	mustProcess(t, `{"action":"clear"}`, now)
	if Active(time.Now().UTC()) != nil {
		t.Fatalf("expected cleared announcement")
	}
}

func TestDefaultTypeAndID(t *testing.T) {
	reset()
	defer reset()
	mustProcess(t, `{"action":"post","title":"Hello","message":"World"}`, time.Now().UTC())
	a := expectActive(t, "note", "Hello")
	if a.ID == "" || len(a.ID) != 16 {
		t.Fatalf("bad id: %q", a.ID)
	}
}

func TestRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"missing title":     `{"action":"post","message":"x"}`,
		"missing message":   `{"action":"post","title":"x"}`,
		"bad type":          `{"action":"post","type":"banana","title":"x","message":"y"}`,
		"non-JSON":          `git push failed`,
		"title too long":    `{"action":"post","title":"` + makeStr(200) + `","message":"y"}`,
		"message too long":  `{"action":"post","title":"t","message":"` + makeStr(600) + `"}`,
		"bad expires":       `{"action":"post","title":"t","message":"m","expires_at":"not-a-date"}`,
		"expired":           `{"action":"post","title":"t","message":"m","expires_at":"2000-01-01T00:00:00Z"}`,
		"bad link":          `{"action":"post","title":"t","message":"m","link":"https://evil.example/hook"}`,
		"non-gitgost link":  `{"action":"post","title":"t","message":"m","link":"https://github.com/other/repo/issues/1"}`,
		"unknown action":    `{"action":"explode","title":"t","message":"m"}`,
		"link not an issue": `{"action":"post","title":"t","message":"m","link":"https://github.com/livrasand/gitGost/actions"}`,
	}
	for name, payload := range cases {
		reset()
		if err := Process([]byte(payload), time.Now().UTC()); err == nil {
			t.Errorf("%s: expected error, got none", name)
		}
		if Active(time.Now().UTC()) != nil {
			t.Errorf("%s: banner must not be created by invalid payload", name)
		}
	}
}

func makeStr(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

func TestLinkValidation(t *testing.T) {
	reset()
	defer reset()
	mustProcess(t, `{"action":"post","title":"t","message":"m","link":"https://github.com/livrasand/gitGost/issues/219"}`, time.Now().UTC())
	if a := Active(time.Now().UTC()); a.Link != "https://github.com/livrasand/gitGost/issues/219" {
		t.Fatalf("bad link: %q", a.Link)
	}
}

func TestExpiryCap(t *testing.T) {
	reset()
	defer reset()
	far := time.Now().UTC().Add(90 * 24 * time.Hour).Format(time.RFC3339)
	mustProcess(t, `{"action":"post","title":"t","message":"m","expires_at":"`+far+`"}`, time.Now().UTC())
	a := Active(time.Now().UTC())
	if d := time.Until(*a.ExpiresAt); d > 7*24*time.Hour+time.Minute {
		t.Fatalf("expiry not capped: %v", d)
	}
}

func TestIDStableFieldsMakeDismissStable(t *testing.T) {
	reset()
	defer reset()
	now := time.Now().UTC()
	mustProcess(t, `{"action":"post","type":"tip","title":"same","message":"same"}`, now)
	a1 := Active(now)
	mustProcess(t, `{"action":"post","type":"tip","title":"same","message":"same"}`, now)
	a2 := Active(now)
	if a1.ID != a2.ID {
		t.Fatalf("id changed with identical payload: %s vs %s", a1.ID, a2.ID)
	}
}

func TestSubscriberEndToEnd(t *testing.T) {
	reset()
	defer reset()

	msgs := []string{
		`{"event":"open"}`,
		`{"event":"message","message":"{\"action\":\"post\",\"type\":\"important\",\"title\":\"E2E\",\"message\":\"works\"}"}`,
		`invalid-line{`,
		`{"event":"message","message":"not json"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		for _, m := range msgs {
			w.Write([]byte(m + "\n"))
			flusher.Flush()
		}
		// El handler devuelve: HTTP chunked cierra el stream (EOF para el
		// cliente) y streamOnce regresa con lo leído.
	}))
	defer srv.Close()

	url := srv.URL + "/mytopic/json?poll=false"
	if err := streamOnce(t.Context(), Options{BaseURL: srv.URL, Topic: "mytopic"}, url, processNtfyMessage); err != nil {
		t.Fatalf("streamOnce: %v", err)
	}
	a := Active(time.Now().UTC())
	if a == nil || a.Title != "E2E" || a.Type != "important" {
		t.Fatalf("expected E2E/important, got %+v", a)
	}
}

func TestClearViaSubscriber(t *testing.T) {
	reset()
	defer reset()
	mustProcess(t, `{"action":"post","title":"x","message":"y"}`, time.Now().UTC())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Write([]byte(`{"event":"message","message":"{\"action\":\"clear\"}"}` + "\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	url := srv.URL + "/mytopic/json?poll=false"
	if err := streamOnce(t.Context(), Options{BaseURL: srv.URL, Topic: "mytopic"}, url, processNtfyMessage); err != nil {
		t.Fatalf("streamOnce: %v", err)
	}
	if Active(time.Now().UTC()) != nil {
		t.Fatalf("expected cleared")
	}
}

func TestExpiryKillsVisibility(t *testing.T) {
	reset()
	defer reset()
	now := time.Now().UTC()
	mustProcess(t, `{"title":"t","message":"m"}`, now)
	if Active(now) == nil {
		t.Fatalf("expected initially active")
	}
	if Active(now.Add(25*time.Hour)) != nil {
		t.Fatalf("expected expired after default TTL")
	}
}

func TestProcessInMemoryOnlyAcrossRestartSim(t *testing.T) {
	// Documenta el contrato: el estado vive en memoria del paquete; nada lo
	// persiste tras un reinicio (decisión del issue). Test directo del store.
	reset()
	mustProcess(t, `{"title":"a","message":"b"}`, time.Now().UTC())
	if Active(time.Now().UTC()) == nil {
		t.Fatal("expected active")
	}
	reset()
	if Active(time.Now().UTC()) != nil {
		t.Fatal("expected nil after reset")
	}
}
