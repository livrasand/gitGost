package http

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/livrasand/gitGost/internal/utils"
)

// --- Data structures ---

// appealTicket stores an anonymous appeal ticket linked to a hash.
type appealTicket struct {
	Hash      string
	Message   string
	CreatedAt time.Time
	Resolved  bool
	Unbanned  bool
}

var (
	appealTicketsMu sync.Mutex
	appealTickets   = make(map[string]*appealTicket)
	appealTicketTTL = 7 * 24 * time.Hour
)

// --- Template ---

var appealStartTmpl = template.Must(template.New("appealStart").Parse(appealHTML))

const appealHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<title>Appeal · gitGost</title>
<style>
body{font-family:Inter,system-ui,-apple-system,Segoe UI,sans-serif;background:#0d1117;color:#c9d1d9;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:32px;}
.shell{background:linear-gradient(145deg,rgba(255,166,87,0.16),rgba(255,107,107,0.14));border:1px solid rgba(255,166,87,0.45);border-radius:16px;padding:1.5px;box-shadow:0 16px 38px rgba(0,0,0,.42);max-width:620px;width:100%;}
.card{background:#0d1117;border-radius:14px;padding:26px;border:1px solid rgba(255,255,255,0.05);}
h1{margin:0 0 6px;font-size:24px;color:#ffa657;}
.eyebrow{display:inline-flex;align-items:center;gap:.35rem;padding:.35rem .75rem;background:rgba(255,166,87,0.12);color:#ffa657;border:1px solid rgba(255,166,87,0.4);border-radius:999px;font-family:'IBM Plex Mono',monospace;font-size:.85rem;margin-bottom:5px;}
.sub{margin:6px 0 14px;color:#9fb3ff;font-size:14px;}
label{display:block;font-weight:700;margin:12px 0 6px;letter-spacing:.01em;}
input[type=text]{width:100%;padding:12px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.04);color:#c9d1d9;font-family:'IBM Plex Mono',monospace;}
button{margin-top:14px;width:100%;padding:12px;border-radius:10px;border:none;background:linear-gradient(135deg,#ffa657,#ff6b6b);color:#0d1117;font-weight:700;font-size:15px;cursor:pointer;box-shadow:0 10px 30px rgba(0,0,0,0.25);}
.note{margin-top:10px;font-size:12px;color:#9fb3ff;}
.error{color:#ffb4c4;font-size:13px;margin-top:10px;}
.info{background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.05);border-radius:12px;padding:14px;margin:14px 0;font-size:13px;line-height:1.55;}
.info strong{color:#ffa657;}
.secret-url{background:rgba(255,166,87,0.08);border:1px solid #ffa657;border-radius:10px;padding:14px;margin:14px 0;word-break:break-all;font-family:'IBM Plex Mono',monospace;font-size:13px;color:#ffa657;}
a{color:#9fb3ff;}
.muted{color:#8b949e;font-size:12px;margin-top:8px;}
textarea{width:100%;min-height:120px;padding:12px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.04);color:#c9d1d9;font-family:'IBM Plex Mono',monospace;resize:vertical;}
</style>
</head>
<body>
<div class="shell"><div class="card">
<div class="eyebrow">Anonymous appeal</div>
<h1>{{.Title}}</h1>
<div class="sub">{{.Subtitle}}</div>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
{{if .SecretURL}}
<div class="info"><strong>Save this link!</strong> This is your only way to access the appeal. If you lose it, it cannot be recovered.</div>
<div class="secret-url">{{.SecretURL}}</div>
<div class="muted">Bookmark it now. You will need it to submit your appeal message.</div>
{{else if .Done}}
<div class="info"><strong>Appeal {{if .Resolved}}resolved{{else}}submitted{{end}}.</strong>
{{if .Resolved}}
  {{if .Unbanned}}The hash has been <strong>unbanned</strong>. You can create content again.{{else}}The ban has been <strong>upheld</strong>. This decision is final.{{end}}
{{else}}An admin will review it. Check back at your secret link for updates.{{end}}
</div>
{{else if .TicketID}}
<form method="POST" action="/appeal/{{.TicketID}}">
<label for="msg">Your appeal message</label>
<textarea id="msg" name="message" placeholder="Explain why your content should be unbanned. Be specific and respectful." required>{{.Message}}</textarea>
<button type="submit">Submit appeal</button>
</form>
<div class="muted">Your identity stays anonymous. No personal data is collected.</div>
{{else}}
<div class="info">Your hash <strong>{{.Hash}}</strong> is <strong>blocked</strong> (6+ reports).<br>To appeal, enter your <strong>appeal token</strong> — you received it when you created the content that produced this hash.</div>
<form method="POST" action="/appeal">
<label for="hash">Hash</label>
<input type="text" id="hash" name="hash" value="{{.Hash}}" readonly />
<label for="appeal_token">Appeal token</label>
<input type="text" id="appeal_token" name="appeal_token" placeholder="Paste your appeal token here" autocomplete="off" />
<button type="submit">Verify &amp; create appeal</button>
</form>
{{end}}
<div class="note"><a href="/v1/moderation/report?hash={{.Hash}}">Back to report page</a> &middot; <a href="https://gitgost.fly.dev/">gitGost</a></div>
</div></div>
</body>
</html>`

// --- Token functions ---

// generateAppealToken creates a deterministic token that proves ownership of a hash.
// The token is HMAC("appeal:"+hash, serverSecretKey) — no storage needed.
func generateAppealToken(hash string) string {
	if hash == "" {
		return ""
	}
	h := hmac.New(sha256.New, getSecretKey())
	h.Write([]byte("appeal:" + hash))
	return hex.EncodeToString(h.Sum(nil))
}

// verifyAppealToken checks whether a token is valid proof of ownership for the given hash.
func verifyAppealToken(hash, token string) bool {
	if hash == "" || token == "" {
		return false
	}
	expected := generateAppealToken(hash)
	return hmac.Equal([]byte(expected), []byte(token))
}

// --- Handlers ---

// AppealStartHandler maneja GET/POST /appeal para iniciar una apelacion.
func AppealStartHandler(c *gin.Context) {
	if c.Request.Method == http.MethodGet {
		hash := strings.TrimSpace(c.Query("hash"))
		if hash == "" {
			_ = appealStartTmpl.Execute(c.Writer, gin.H{
				"Title":    "Appeal a ban",
				"Subtitle": "Enter the blocked hash to start an appeal.",
				"Hash":     "",
				"Error":    "",
			})
			return
		}
		if !isBlockedHash(hash) {
			_ = appealStartTmpl.Execute(c.Writer, gin.H{
				"Title":    "Hash not blocked",
				"Subtitle": "This hash is not currently blocked.",
				"Hash":     hash,
				"Error":    "This hash has fewer than 6 reports and is not blocked. No appeal needed.",
			})
			return
		}
		_ = appealStartTmpl.Execute(c.Writer, gin.H{
			"Title":    "Appeal a ban",
			"Subtitle": "Verify ownership to start an anonymous appeal.",
			"Hash":     hash,
		})
		return
	}

	// POST: verify appeal_token and create ticket
	hash := strings.TrimSpace(c.PostForm("hash"))
	token := strings.TrimSpace(c.PostForm("appeal_token"))

	if hash == "" || token == "" {
		_ = appealStartTmpl.Execute(c.Writer, gin.H{
			"Title":    "Missing fields",
			"Subtitle": "Both hash and appeal token are required.",
			"Hash":     hash,
			"Error":    "Hash and appeal token are required.",
		})
		return
	}

	if !isBlockedHash(hash) {
		_ = appealStartTmpl.Execute(c.Writer, gin.H{
			"Title":    "Hash not blocked",
			"Subtitle": "This hash is not currently blocked.",
			"Hash":     hash,
			"Error":    "This hash has fewer than 6 reports. No appeal needed.",
		})
		return
	}

	if !verifyAppealToken(hash, token) {
		_ = appealStartTmpl.Execute(c.Writer, gin.H{
			"Title":    "Invalid token",
			"Subtitle": "The appeal token does not match this hash.",
			"Hash":     hash,
			"Error":    "Invalid appeal token. Make sure you are using the exact token you received when creating the content.",
		})
		return
	}

	// Generate ticket
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		c.String(http.StatusInternalServerError, "Error creating appeal")
		return
	}
	ticketID := hex.EncodeToString(b)

	appealTicketsMu.Lock()
	appealTickets[ticketID] = &appealTicket{
		Hash:      hash,
		CreatedAt: time.Now(),
	}
	appealTicketsMu.Unlock()

	scheme := getScheme(c.Request)
	secretURL := fmt.Sprintf("%s://%s/appeal/%s", scheme, c.Request.Host, ticketID)

	_ = appealStartTmpl.Execute(c.Writer, gin.H{
		"Title":     "Appeal created",
		"Subtitle":  "Your anonymous appeal ticket is ready.",
		"Hash":      hash,
		"SecretURL": secretURL,
	})
}

// AppealViewHandler maneja GET/POST /appeal/:ticket para ver y enviar el mensaje de apelacion.
func AppealViewHandler(c *gin.Context) {
	ticketID := c.Param("ticket")

	appealTicketsMu.Lock()
	ticket, exists := appealTickets[ticketID]
	appealTicketsMu.Unlock()

	if !exists || time.Since(ticket.CreatedAt) > appealTicketTTL {
		c.String(http.StatusNotFound, "Appeal not found or expired.")
		return
	}

	if c.Request.Method == http.MethodPost {
		message := strings.TrimSpace(c.PostForm("message"))
		if message == "" {
			_ = appealStartTmpl.Execute(c.Writer, gin.H{
				"Title":    "Submit appeal",
				"Subtitle": "Your appeal message cannot be empty.",
				"Hash":     ticket.Hash,
				"TicketID": ticketID,
				"Error":    "Message is required.",
			})
			return
		}

		appealTicketsMu.Lock()
		ticket.Message = message
		appealTicketsMu.Unlock()

		// Notify admin via ntfy if configured
		if ntfyAdminTopic != "" {
			go notifyAdminAppeal(ticketID, ticket.Hash)
		}

		_ = appealStartTmpl.Execute(c.Writer, gin.H{
			"Title":    "Appeal submitted",
			"Subtitle": "Your appeal has been received.",
			"Hash":     ticket.Hash,
			"Done":     true,
		})
		return
	}

	// GET: show the appeal form or status
	if ticket.Resolved {
		status := "closed"
		if ticket.Unbanned {
			status = "approved — hash unbanned"
		} else {
			status = "dismissed — ban upheld"
		}
		_ = appealStartTmpl.Execute(c.Writer, gin.H{
			"Title":    "Appeal resolved",
			"Subtitle": "Status: " + status,
			"Hash":     ticket.Hash,
			"Done":     true,
			"Resolved": true,
			"Unbanned": ticket.Unbanned,
		})
		return
	}

	if ticket.Message != "" {
		_ = appealStartTmpl.Execute(c.Writer, gin.H{
			"Title":    "Appeal submitted",
			"Subtitle": "Waiting for admin review.",
			"Hash":     ticket.Hash,
			"Done":     true,
		})
		return
	}

	_ = appealStartTmpl.Execute(c.Writer, gin.H{
		"Title":    "Submit appeal",
		"Subtitle": "Explain why your content should be unbanned.",
		"Hash":     ticket.Hash,
		"TicketID": ticketID,
	})
}

// notifyAdminAppeal sends a ntfy notification when a new appeal is filed.
func notifyAdminAppeal(ticketID, hash string) {
	if ntfyAdminTopic == "" {
		return
	}
	appealURL := fmt.Sprintf("https://gitgost.fly.dev/appeal/%s", ticketID)
	payload := fmt.Sprintf(`{"topic":"%s","title":"New appeal filed","message":"Hash %s has filed an appeal.\n\n%s","tags":["warning"]}`, ntfyAdminTopic, hash, appealURL)
	resp, err := http.Post("https://ntfy.sh", "application/json", strings.NewReader(payload))
	if err != nil {
		utils.Log("Error sending ntfy appeal notification: %v", err)
		return
	}
	resp.Body.Close()
}

// AdminAppealsHandler lista las apelaciones abiertas (protegido por password).
func AdminAppealsHandler(c *gin.Context) {
	password := c.Query("password")
	if password == "" || panicPassword == "" || password != panicPassword {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	escapedPassword := template.HTMLEscapeString(password)

	appealTicketsMu.Lock()
	type appealView struct {
		TicketID  string
		Hash      string
		Message   string
		CreatedAt time.Time
		Resolved  bool
		Unbanned  bool
	}
	var openList, resolvedList []appealView
	for id, t := range appealTickets {
		v := appealView{
			TicketID:  id,
			Hash:      t.Hash,
			Message:   t.Message,
			CreatedAt: t.CreatedAt,
			Resolved:  t.Resolved,
			Unbanned:  t.Unbanned,
		}
		if t.Resolved {
			resolvedList = append(resolvedList, v)
		} else {
			openList = append(openList, v)
		}
	}
	appealTicketsMu.Unlock()

	c.Header("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(c.Writer, `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><title>Admin · Appeals · gitGost</title>
<style>body{font-family:Inter,system-ui,sans-serif;background:#0d1117;color:#c9d1d9;max-width:800px;margin:0 auto;padding:32px;}
h1{color:#ffa657;}table{width:100%%;border-collapse:collapse;margin:16px 0;}
th,td{text-align:left;padding:10px;border-bottom:1px solid rgba(255,255,255,0.08);}
th{color:#9fb3ff;font-size:13px;text-transform:uppercase;}
td{font-family:'IBM Plex Mono',monospace;font-size:13px;}
.tag{padding:4px 8px;border-radius:6px;font-size:11px;font-weight:600;}
.tag-open{background:rgba(46,160,67,0.2);color:#3fb950;border:1px solid rgba(46,160,67,0.4);}
.tag-closed{background:rgba(248,81,73,0.2);color:#f85149;border:1px solid rgba(248,81,73,0.4);}
a{color:#58a6ff;}.empty{color:#8b949e;}.sep{margin:32px 0;border-color:rgba(255,255,255,0.05);}
form{display:inline;}
button{background:transparent;border:1px solid rgba(255,255,255,0.15);color:#c9d1d9;border-radius:6px;padding:4px 10px;cursor:pointer;font-size:12px;}
button.unban{border-color:#3fb950;color:#3fb950;}
button.dismiss{border-color:#f85149;color:#f85149;}
</style></head><body>
<h1>Appeals</h1>
<p style="color:#9fb3ff;">Password-protected admin panel. All data is ephemeral (in-memory).</p>
<hr class="sep">
<h2>Open (%d)</h2>
<table><tr><th>Ticket</th><th>Hash</th><th>Message</th><th>Age</th><th>Actions</th></tr>`, len(openList))
	if len(openList) == 0 {
		fmt.Fprintf(c.Writer, `<tr><td colspan="5" class="empty">No open appeals.</td></tr>`)
	}
	for _, v := range openList {
		msgPreview := v.Message
		if len(msgPreview) > 60 {
			msgPreview = msgPreview[:60] + "..."
		}
		age := time.Since(v.CreatedAt).Round(time.Minute)
		fmt.Fprintf(c.Writer, `<tr><td><a href="/appeal/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td>
<td>
<form method="POST" action="/admin/appeals/%s/resolve" style="display:inline;">
<input type="hidden" name="password" value="%s">
<input type="hidden" name="outcome" value="unban">
<button type="submit" class="unban">Unban</button>
</form>
<form method="POST" action="/admin/appeals/%s/resolve" style="display:inline;">
<input type="hidden" name="password" value="%s">
<input type="hidden" name="outcome" value="dismiss">
<button type="submit" class="dismiss">Dismiss</button>
</form>
</td></tr>`, v.TicketID, v.TicketID[:8], template.HTMLEscapeString(v.Hash), msgPreview, age, v.TicketID, escapedPassword, v.TicketID, escapedPassword)
	}
	fmt.Fprintf(c.Writer, `</table>
<hr class="sep"><h2>Resolved (%d)</h2><table><tr><th>Ticket</th><th>Hash</th><th>Outcome</th></tr>`, len(resolvedList))
	if len(resolvedList) == 0 {
		fmt.Fprintf(c.Writer, `<tr><td colspan="3" class="empty">No resolved appeals.</td></tr>`)
	}
	for _, v := range resolvedList {
		outcome := `<span class="tag tag-closed">dismissed</span>`
		if v.Unbanned {
			outcome = `<span class="tag tag-open">unbanned</span>`
		}
		fmt.Fprintf(c.Writer, `<tr><td>%s</td><td>%s</td><td>%s</td></tr>`, v.TicketID[:8], template.HTMLEscapeString(v.Hash), outcome)
	}
	fmt.Fprintf(c.Writer, `</table></body></html>`)
}

// AdminAppealResolveHandler resuelve una apelacion (unban o dismiss).
func AdminAppealResolveHandler(c *gin.Context) {
	ticketID := c.Param("ticket")
	password := c.PostForm("password")
	outcome := c.PostForm("outcome")

	if password == "" || panicPassword == "" || password != panicPassword {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}

	appealTicketsMu.Lock()
	ticket, exists := appealTickets[ticketID]
	if !exists {
		appealTicketsMu.Unlock()
		c.String(http.StatusNotFound, "Appeal not found")
		return
	}

	ticket.Resolved = true
	if outcome == "unban" {
		ticket.Unbanned = true
		// Remove from blocked hashes
		delete(blockedHashes, ticket.Hash)
		utils.Log("Appeal %s: hash %s unbanned", ticketID[:8], ticket.Hash)
	} else {
		ticket.Unbanned = false
		utils.Log("Appeal %s: hash %s ban upheld", ticketID[:8], ticket.Hash)
	}
	appealTicketsMu.Unlock()

	// Send ntfy notification about resolution
	if ntfyAdminTopic != "" && ticket.Message != "" {
		go func() {
			status := "upheld"
			if outcome == "unban" {
				status = "unbanned"
			}
			payload := fmt.Sprintf(`{"topic":"%s","title":"Appeal %s","message":"Hash %s appeal %s.","tags":["white_check_mark"]}`, ntfyAdminTopic, status, ticket.Hash, status)
			resp, err := http.Post("https://ntfy.sh", "application/json", strings.NewReader(payload))
			if err == nil {
				resp.Body.Close()
			}
		}()
	}

	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/appeals?password=%s", password))
}
