package http

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadBundleToR2(t *testing.T) {
	payload := []byte("fake bundle bytes")
	dir := t.TempDir()
	bundle := filepath.Join(dir, "repo.bundle")
	if err := os.WriteFile(bundle, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	var gotPath, gotMethod string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("R2_ENDPOINT", srv.URL)
	t.Setenv("R2_ACCOUNT_ID", "")
	t.Setenv("R2_ACCESS_KEY_ID", "test-key")
	t.Setenv("R2_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("R2_BUCKET", "gitgost-bundles")

	const hash = "abc123"
	res, err := uploadBundleToR2(context.Background(), bundle, "owner-repo.bundle", int64(len(payload)), hash)
	if err != nil {
		t.Fatalf("uploadBundleToR2: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if want := "/gitgost-bundles/bundles/" + hash + ".bundle"; gotPath != want {
		t.Errorf("path = %s, want %s", gotPath, want)
	}
	if !bytes.Equal(gotBody, payload) {
		t.Errorf("uploaded body mismatch")
	}
	if !strings.HasPrefix(res.DownloadURL, srv.URL+"/gitgost-bundles/bundles/"+hash+".bundle?") {
		t.Errorf("download URL = %s", res.DownloadURL)
	}
	if !strings.Contains(res.DownloadURL, "X-Amz-Expires=604800") {
		t.Errorf("download URL does not expire in 7 days: %s", res.DownloadURL)
	}
	if res.Sha256 != hash || res.Size != int64(len(payload)) || res.Filename != "owner-repo.bundle" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestR2NotConfigured(t *testing.T) {
	t.Setenv("R2_ENDPOINT", "")
	t.Setenv("R2_ACCOUNT_ID", "")
	t.Setenv("R2_ACCESS_KEY_ID", "")
	t.Setenv("R2_SECRET_ACCESS_KEY", "")
	t.Setenv("R2_BUCKET", "")
	if _, err := newR2StoreFromEnv(); err == nil {
		t.Fatal("expected error when R2 is not configured")
	}
}
