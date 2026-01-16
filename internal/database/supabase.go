package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type SupabaseClient struct {
	URL string
	key string
}

type PRRecord struct {
	Owner     string    `json:"owner"`
	Repo      string    `json:"repo"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

type Stats struct {
	TotalPRs int `json:"total_prs"`
}

func NewSupabaseClient(url, key string) *SupabaseClient {
	return &SupabaseClient{
		URL: url,
		key: key,
	}
}

func (c *SupabaseClient) InsertPR(ctx context.Context, owner, repo, prURL string) error {
	record := PRRecord{
		Owner:     owner,
		Repo:      repo,
		URL:       prURL,
		CreatedAt: time.Now(),
	}

	jsonData, err := json.Marshal(record)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/v1/prs", c.URL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req = req.WithContext(ctx)
	req.Header.Set("apikey", c.key)
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=minimal")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		// 409 Conflict - URL already exists (UNIQUE constraint violation)
		return fmt.Errorf("PR already recorded: duplicate URL %s", prURL)
	}

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to insert PR: status %d", resp.StatusCode)
	}

	return nil
}

func (c *SupabaseClient) GetTotalPRs(ctx context.Context) (int, error) {
	url := fmt.Sprintf("%s/rest/v1/prs", c.URL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	req = req.WithContext(ctx)
	req.Header.Set("apikey", c.key)
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "count=exact")
	req.Header.Set("Range-Unit", "items")
	req.Header.Set("Range", "0-0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("failed to get count: status %d", resp.StatusCode)
	}

	contentRange := resp.Header.Get("Content-Range")
	if contentRange == "" {
		return 0, fmt.Errorf("missing Content-Range header in response")
	}

	// Parse Content-Range header format: "0-0/{total}" or "*/{total}"
	slashIdx := strings.LastIndex(contentRange, "/")
	if slashIdx == -1 {
		return 0, fmt.Errorf("invalid Content-Range format: %s", contentRange)
	}

	totalStr := contentRange[slashIdx+1:]
	count, err := strconv.Atoi(totalStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse total count from Content-Range '%s': %v", contentRange, err)
	}

	return count, nil
}

func (c *SupabaseClient) GetRecentPRs(ctx context.Context, limit int) ([]PRRecord, error) {
	// Validar y sanitizar el límite
	if limit <= 0 {
		limit = 10 // Default sensible
	}
	if limit > 100 {
		limit = 100 // Máximo razonable
	}

	url := fmt.Sprintf("%s/rest/v1/prs?select=owner,repo,url,created_at&order=created_at.desc&limit=%d", c.URL, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req = req.WithContext(ctx)
	req.Header.Set("apikey", c.key)
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get PRs: status %d", resp.StatusCode)
	}

	var prs []PRRecord
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return nil, err
	}

	return prs, nil
}

// GetLatestPRCreatedAt obtiene el timestamp del PR más reciente
func (c *SupabaseClient) GetLatestPRCreatedAt(ctx context.Context) (*time.Time, error) {
	url := fmt.Sprintf("%s/rest/v1/prs?select=created_at&order=created_at.desc&limit=1", c.URL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req = req.WithContext(ctx)
	req.Header.Set("apikey", c.key)
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get latest PR: status %d", resp.StatusCode)
	}

	var prs []PRRecord
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return nil, err
	}

	// Si no hay PRs, retornar nil
	if len(prs) == 0 {
		return nil, nil
	}

	return &prs[0].CreatedAt, nil
}
