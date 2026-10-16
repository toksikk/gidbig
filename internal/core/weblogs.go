package gidbig

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/toksikk/gidbig/internal/logs"
)

const (
	// logsRawFilename is what a downloaded window is called.
	logsRawFilename = "gidbig-logs.txt"
	// logsDefaultCount is the window the page shows when the caller asks for no
	// particular size.
	logsDefaultCount = logs.DefaultShowLines
)

// logsPage is the payload web/templates/logs.html renders.
type logsPage struct {
	// Header describes the shown window; Text is the window itself, one line per
	// record, and both come from the logs module, so the page and the /admin logs
	// command cannot describe the same window differently.
	Header string
	Text   string
	// Unavailable is set when no log window is wired.
	Unavailable string
	// Level, Filter and Count are the requested values, kept so the form shows
	// what produced the window.
	Level  string
	Filter string
	Count  int
	// Levels are the selectable floors with the requested one marked.
	Levels []logsLevelChoice
	// RawURL downloads the same window as text/plain.
	RawURL string
}

// logsLevelChoice is one option of the level select.
type logsLevelChoice struct {
	Value    string
	Label    string
	Selected bool
}

// handleAdminLogs serves the owner-only log viewer. Like /admin, every refusal is
// the same 404 a missing page gives, so a non-owner cannot learn it exists.
func (ws *webServer) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	if r.URL.Path != "/admin/logs" {
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

	if ws.logs == nil {
		// Reachable when the module failed to register; say why instead of
		// answering with an empty page.
		ws.renderLogsPage(w, session, logsPage{Unavailable: logs.UnavailableMessage, Levels: levelChoices("")})
		return
	}

	query := r.URL.Query()
	floor, err := logs.ParseLevel(query.Get("level"))
	if err != nil {
		// Only a hand-built URL can get here: the form offers the valid values.
		http.Error(w, "unknown log level", http.StatusBadRequest)
		return
	}
	filter := query.Get("filter")
	count := logsDefaultCount
	if raw := query.Get("count"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "count must be a number", http.StatusBadRequest)
			return
		}
		// A count the window cannot honour is clamped by the module; the page
		// reports the count it actually applied.
		count = parsed
	}

	window := ws.logs.Window(logs.Query{MinLevel: floor.Level, Filter: filter, Count: count})

	if query.Get("format") == "raw" {
		ws.writeLogsDownload(w, window)
		return
	}

	ws.renderLogsPage(w, session, logsPage{
		Header: window.Header(),
		Text:   window.Text(),
		Level:  floor.Value,
		Filter: window.Query.Filter,
		Count:  window.Query.Count,
		Levels: levelChoices(floor.Value),
		RawURL: logsDownloadURL(floor.Value, window.Query.Filter, window.Query.Count),
	})
}

// renderLogsPage renders the filter form and the window behind it.
func (ws *webServer) renderLogsPage(w http.ResponseWriter, session *sessionData, page logsPage) {
	if page.Levels == nil {
		page.Levels = levelChoices("")
	}
	tmpl := ws.tmpls["logs.html"]
	if tmpl == nil {
		// Only reachable when the templates were never loaded. Refuse instead of
		// panicking inside a request.
		slog.Error("web logs: logs.html template is not loaded")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	td := templateData{
		Username:  session.DiscordUsername,
		AvatarURL: session.DiscordAvatarURL,
		IsOwner:   true,
		Logs:      &page,
	}
	if err := tmpl.ExecuteTemplate(w, "header", td); err != nil {
		slog.Error("failed to execute template", "template", "logs.html/header", "error", err)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "footer", nil); err != nil {
		slog.Error("failed to execute template", "template", "logs.html/footer", "error", err)
	}
}

// writeLogsDownload answers with the window as a text/plain attachment, so the
// owner can keep or grep the lines the bot's own log already rotated away.
func (ws *webServer) writeLogsDownload(w http.ResponseWriter, window logs.Window) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+logsRawFilename+`"`)
	// The body is plain text; nosniff is also set for every response.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.WriteString(w, window.Header()+"\n\n"+window.Text()+"\n"); err != nil {
		slog.Error("web logs: download write failed", "error", err)
	}
}

// levelChoices marks the requested floor in the select the module's level
// vocabulary defines.
func levelChoices(selected string) []logsLevelChoice {
	options := logs.LevelOptions()
	choices := make([]logsLevelChoice, 0, len(options))
	for _, o := range options {
		choices = append(choices, logsLevelChoice{Value: o.Value, Label: o.Label, Selected: o.Value == selected})
	}
	return choices
}

// logsDownloadURL builds the download link for the window on screen, so a
// download always matches what the page shows.
func logsDownloadURL(level, filter string, count int) string {
	params := url.Values{}
	params.Set("level", level)
	params.Set("count", strconv.Itoa(count))
	if filter != "" {
		params.Set("filter", filter)
	}
	params.Set("format", "raw")
	return "/admin/logs?" + params.Encode()
}
