package announcement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var validTypes = map[string]bool{
	"note":      true,
	"tip":       true,
	"important": true,
	"warning":   true,
	"caution":   true,
}

var githubLinkRe = regexp.MustCompile(`^https://github\.com/livrasand/gitGost/(issues|pull)/[0-9]+$`)

const (
	maxTitleLen   = 120
	maxMessageLen = 500
	defaultTTL    = 24 * time.Hour
	maxTTL        = 7 * 24 * time.Hour
)

type Announcement struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Message   string     `json:"message"`
	Link      string     `json:"link,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type urgentPayload struct {
	Action    string  `json:"action"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Message   string  `json:"message"`
	Link      *string `json:"link"`
	ExpiresAt *string `json:"expires_at"`
}

type store struct {
	mu   sync.RWMutex
	cur  *Announcement
	exAt time.Time
}

var current store

func isExpired(now time.Time) bool {
	current.mu.RLock()
	defer current.mu.RUnlock()
	return current.cur == nil || now.After(current.exAt)
}

func Active(now time.Time) *Announcement {
	current.mu.RLock()
	defer current.mu.RUnlock()
	if current.cur == nil || now.After(current.exAt) {
		return nil
	}
	a := *current.cur
	return &a
}

func SetPublic(a *Announcement, expires time.Time) {
	storeSet(a, expires)
}

func Clear() {
	current.mu.Lock()
	current.cur = nil
	current.exAt = time.Time{}
	current.mu.Unlock()
}

func storeSet(a *Announcement, expires time.Time) {
	current.mu.Lock()
	current.cur = a
	current.exAt = expires
	current.mu.Unlock()
}

func Process(raw []byte, now time.Time) error {
	if len(raw) == 0 {
		return fmt.Errorf("empty payload")
	}
	var p urgentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "clear":
		Clear()
		return nil
	case "post", "":
		a, expires, err := normalize(&p, now)
		if err != nil {
			return err
		}
		storeSet(a, expires)
		return nil
	default:
		return fmt.Errorf("unknown action %q", p.Action)
	}
}

func normalize(p *urgentPayload, now time.Time) (*Announcement, time.Time, error) {
	title := strings.TrimSpace(p.Title)
	message := strings.TrimSpace(p.Message)
	if title == "" {
		return nil, time.Time{}, fmt.Errorf("missing title")
	}
	if len(title) > maxTitleLen {
		return nil, time.Time{}, fmt.Errorf("title too long (%d > %d)", len(title), maxTitleLen)
	}
	if message == "" {
		return nil, time.Time{}, fmt.Errorf("missing message")
	}
	if len(message) > maxMessageLen {
		return nil, time.Time{}, fmt.Errorf("message too long (%d > %d)", len(message), maxMessageLen)
	}

	atype := strings.ToLower(strings.TrimSpace(p.Type))
	if atype == "" {
		atype = "note"
	}
	if !validTypes[atype] {
		return nil, time.Time{}, fmt.Errorf("invalid type %q", p.Type)
	}

	link := ""
	if p.Link != nil {
		l := strings.TrimSpace(*p.Link)
		if l != "" {
			if !githubLinkRe.MatchString(l) {
				return nil, time.Time{}, fmt.Errorf("link must be a gitGost GitHub issue/PR URL")
			}
			link = l
		}
	}

	expires := now.Add(defaultTTL)
	if p.ExpiresAt != nil && strings.TrimSpace(*p.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*p.ExpiresAt))
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("invalid expires_at: %w", err)
		}
		if d := t.Sub(now); d <= 0 {
			return nil, time.Time{}, fmt.Errorf("expires_at is in the past")
		} else if d > maxTTL {
			expires = now.Add(maxTTL)
		} else {
			expires = t
		}
	}

	a := &Announcement{
		ID:        announcementID(title, message, expires),
		Type:      atype,
		Title:     title,
		Message:   message,
		Link:      link,
		ExpiresAt: &expires,
	}
	return a, expires, nil
}

func announcementID(title, message string, expires time.Time) string {
	h := sha256.Sum256([]byte(title + "\x00" + message + "\x00" + expires.UTC().Format(time.RFC3339)))
	return hex.EncodeToString(h[:8])
}
