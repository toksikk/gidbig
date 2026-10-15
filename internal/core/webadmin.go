package gidbig

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/toksikk/gidbig/internal/configcmd"
)

const (
	// sessionMaxAge bounds how old the login behind any session route may be.
	// The cookie's MaxAge matches it, but the server-side check is what binds:
	// a stolen cookie stops working after this even if replayed.
	sessionMaxAge = 7 * 24 * time.Hour
	// adminMaxSessionAge is the stricter bound for configuration writes.
	adminMaxSessionAge = 12 * time.Hour
	// adminFormMaxBytes bounds one admin form submission. Every field is short,
	// so anything larger is not a form this panel rendered.
	adminFormMaxBytes = 16 << 10
	// adminCSRFBytes is the entropy of the per-session form token.
	adminCSRFBytes = 32
)

// configAdmin is the slice of the configcmd module the admin panel uses. It is
// the same code the /admin config command calls, so a value is validated and
// applied identically whichever surface the owner uses.
type configAdmin interface {
	Entries() ([]configcmd.Entry, error)
	ApplySet(key, value, actor string) (string, bool)
	ApplyUnset(key, actor string) (string, bool)
}

// adminPage is the payload web/templates/admin.html renders.
type adminPage struct {
	// Entries is every runtime-editable key with its effective value.
	Entries []configcmd.Entry
	// CSRF is the token every form on the page carries.
	CSRF string
	// Flash is the outcome of the last submission, shown once.
	Flash string
	// FlashOK marks the flash as a success rather than a rejection.
	FlashOK bool
	// Invalid is set when the stored overrides disagree with config.yaml, which
	// makes every write fail until one of them is removed.
	Invalid string
	// Unavailable is set when no settings store is wired.
	Unavailable string
}

// handleAdmin serves the owner-only runtime settings panel. Every refusal is the
// same 404 a missing page gives, so a non-owner cannot even learn the panel
// exists.
func (ws *webServer) handleAdmin(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	if r.URL.Path != "/admin" || ws.config == nil {
		http.NotFound(w, r)
		return
	}
	session := ws.store.Get(r)
	if !ws.isOwner(session) {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		ws.renderAdminPage(w, session)
	case http.MethodPost:
		ws.applyAdminPost(w, r, session)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// isOwner reports whether the session may see and change runtime settings. The
// owner ID is the one /admin gates on; a session without an issue time predates
// this check and counts as expired, because a sound board login is not proof
// that someone just authenticated for a configuration write.
func (ws *webServer) isOwner(session *sessionData) bool {
	if ws.ownerID == "" || !ws.validSession(session) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(session.DiscordUserID), []byte(ws.ownerID)) != 1 {
		return false
	}
	return ws.sessionAge(session) <= adminMaxSessionAge
}

// validSession reports whether the session belongs to a logged-in user whose
// login is at most sessionMaxAge old. A session without an issue time predates
// the check and counts as expired.
func (ws *webServer) validSession(session *sessionData) bool {
	if session == nil || session.DiscordUserID == "" || session.IssuedAt == 0 {
		return false
	}
	age := ws.sessionAge(session)
	return age >= 0 && age <= sessionMaxAge
}

// sessionAge is how long ago the session's login completed.
func (ws *webServer) sessionAge(session *sessionData) time.Duration {
	return ws.currentTime().Sub(time.Unix(session.IssuedAt, 0))
}

// currentTime is the process clock, injectable so a test can age a session.
func (ws *webServer) currentTime() time.Time {
	if ws.now == nil {
		return time.Now()
	}
	return ws.now()
}

// renderAdminPage renders the settings table, minting a form token when the
// session has none and consuming the flash left by the previous submission.
func (ws *webServer) renderAdminPage(w http.ResponseWriter, session *sessionData) {
	entries, err := ws.config.Entries()
	page := &adminPage{Entries: entries, CSRF: session.AdminCSRF}
	switch {
	case errors.Is(err, configcmd.ErrUnavailable):
		page.Unavailable = configcmd.UnavailableMessage
	case err != nil:
		// A config.yaml edit can invalidate a row that was fine when it was
		// written; show it, because every write is rejected until it is fixed.
		page.Invalid = err.Error()
	}

	if page.CSRF == "" {
		token, err := newAdminCSRFToken()
		if err != nil {
			slog.Error("web admin: could not mint a form token", "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		session.AdminCSRF = token
		page.CSRF = token
	}

	page.Flash = session.Flash
	page.FlashOK = session.FlashOK
	session.Flash = ""
	session.FlashOK = false
	if err := ws.store.Save(w, session); err != nil {
		slog.Error("web admin: could not save the session", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	td := templateData{
		Username:  session.DiscordUsername,
		AvatarURL: session.DiscordAvatarURL,
		IsOwner:   true,
		Admin:     page,
	}
	tmpl := ws.tmpls["admin.html"]
	if tmpl == nil {
		// Only reachable when the templates were never loaded. Refuse instead
		// of panicking inside a request.
		slog.Error("web admin: admin.html template is not loaded")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "header", td); err != nil {
		slog.Error("failed to execute template", "template", "admin.html/header", "error", err)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "footer", nil); err != nil {
		slog.Error("failed to execute template", "template", "admin.html/footer", "error", err)
	}
}

// applyAdminPost applies one set or unset and redirects back to the table, so a
// reload cannot repeat the change.
func (ws *webServer) applyAdminPost(w http.ResponseWriter, r *http.Request, session *sessionData) {
	r.Body = http.MaxBytesReader(w, r.Body, adminFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	if !ws.validOrigin(r) {
		slog.Warn("web admin: rejected a submission from a foreign origin", "origin", r.Header.Get("Origin"))
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if session.AdminCSRF == "" ||
		subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(session.AdminCSRF)) != 1 {
		slog.Warn("web admin: rejected a submission without the form token", "actor", session.DiscordUserID)
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	action := r.PostFormValue("action")
	key := r.PostFormValue("key")
	var message string
	var ok bool
	switch action {
	case "set":
		message, ok = ws.config.ApplySet(key, r.PostFormValue("value"), session.DiscordUserID)
	case "unset":
		message, ok = ws.config.ApplyUnset(key, session.DiscordUserID)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	// The value is deliberately absent from the log line: a key can name a
	// channel or a model, and the audit trail only needs who changed what.
	slog.Info("web admin: runtime setting change", "action", action, "key", key, "actor", session.DiscordUserID, "applied", ok)

	session.Flash = message
	session.FlashOK = ok
	if err := ws.store.Save(w, session); err != nil {
		slog.Error("web admin: could not save the session", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// validOrigin checks the browser's Origin header against the address the UI
// is served from; /admin and /playsound share it. A missing Origin is accepted:
// a non-browser client cannot be driven by a hostile page.
func (ws *webServer) validOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if ws.uiOrigin != "" {
		return origin == ws.uiOrigin
	}
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}

// newAdminCSRFToken returns a fresh form token.
func newAdminCSRFToken() (string, error) {
	b := make([]byte, adminCSRFBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
