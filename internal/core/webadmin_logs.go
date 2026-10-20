package gidbig

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/toksikk/gidbig/internal/logwindow"
)

// logReader is the slice of the log window the /admin/logs page uses.
type logReader interface {
	Lines(q logwindow.Query) []string
	Cap() int
}

// adminLogsPage is the payload web/templates/admin_logs.html renders.
type adminLogsPage struct {
	// Level, Filter and Count echo the query back into the form.
	Level  string
	Filter string
	Count  int
	// Cap is the window capacity, the largest useful count.
	Cap int
	// Text is the rendered lines, one per line.
	Text string
	// Lines is how many lines Text holds.
	Lines int
	// DownloadURL is the download link for the same query. It is built from
	// url.Values.Encode on a fixed path, so it is safe as a template.URL.
	DownloadURL template.URL
}

// logsQuery parses level, filter and count from the request. Invalid values
// fall back to the defaults: level info and the whole window.
func (ws *webServer) logsQuery(r *http.Request) (logwindow.Query, string) {
	capacity := ws.logs.Cap()
	q := logwindow.Query{MinLevel: slog.LevelInfo, Count: capacity}
	level := "info"
	values := r.URL.Query()
	if l, ok := logwindow.ParseLevel(values.Get("level")); ok {
		q.MinLevel = l
		level = strings.ToLower(strings.TrimSpace(values.Get("level")))
	}
	q.Filter = values.Get("filter")
	if c, err := strconv.Atoi(values.Get("count")); err == nil && c >= 1 {
		q.Count = min(c, capacity)
	}
	return q, level
}

// logsText joins rendered lines into the text both the page and the download
// show.
func logsText(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// handleAdminLogs serves the owner-only, read-only log window. Every refusal is
// the same 404 a missing page gives.
func (ws *webServer) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	if r.URL.Path != "/admin/logs" || ws.logs == nil {
		http.NotFound(w, r)
		return
	}
	session := ws.store.Get(r)
	if !ws.isOwner(session) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	q, level := ws.logsQuery(r)
	lines := ws.logs.Lines(q)
	text := logsText(lines)

	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="gidbig-logs.txt"`)
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write([]byte(text)); err != nil {
			slog.Warn("web admin: could not write the log download", "error", err)
		}
		return
	}

	dl := url.Values{}
	dl.Set("level", level)
	if q.Filter != "" {
		dl.Set("filter", q.Filter)
	}
	dl.Set("count", strconv.Itoa(q.Count))
	dl.Set("download", "1")
	page := &adminLogsPage{
		Level:       level,
		Filter:      q.Filter,
		Count:       q.Count,
		Cap:         ws.logs.Cap(),
		Text:        text,
		Lines:       len(lines),
		DownloadURL: template.URL("/admin/logs?" + dl.Encode()),
	}
	td := templateData{
		Username:  session.DiscordUsername,
		AvatarURL: session.DiscordAvatarURL,
		IsOwner:   true,
		Logs:      page,
	}
	tmpl := ws.tmpls["admin_logs.html"]
	if tmpl == nil {
		slog.Error("web admin: admin_logs.html template is not loaded")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.ExecuteTemplate(w, "header", td); err != nil {
		slog.Error("failed to execute template", "template", "admin_logs.html/header", "error", err)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "footer", nil); err != nil {
		slog.Error("failed to execute template", "template", "admin_logs.html/footer", "error", err)
	}
}
