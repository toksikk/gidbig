package gidbig

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/logbuf"
	"github.com/toksikk/gidbig/internal/logs"
)

// stubLogWindow answers with a canned window and records the query it was asked
// for, so the handler's parameter parsing can be checked without a buffer.
type stubLogWindow struct {
	window  logs.Window
	queries []logs.Query
}

func (s *stubLogWindow) Window(q logs.Query) logs.Window {
	s.queries = append(s.queries, q)
	w := s.window
	w.Query = logs.Query{MinLevel: q.MinLevel, Filter: q.Filter, Count: q.Count}
	return w
}

// newLogsTestServer builds a web server with the log page's template loaded.
func newLogsTestServer(t *testing.T, window logWindow, now time.Time) *webServer {
	t.Helper()
	t.Chdir("../..")
	ws := &webServer{
		store:   newSessionStore("test-secret", false),
		logs:    window,
		ownerID: testOwnerID,
		now:     func() time.Time { return now },
		tmpls:   map[string]*template.Template{},
	}
	ws.loadTemplates()
	return ws
}

// TestHandleAdminLogs_DeniesEveryoneButTheOwner reuses the admin panel's rule:
// every refusal is the 404 of a missing page, so the page cannot be discovered.
func TestHandleAdminLogs_DeniesEveryoneButTheOwner(t *testing.T) {
	now := time.Now()
	ws := newLogsTestServer(t, &stubLogWindow{}, now)

	cases := []struct {
		name    string
		path    string
		session *sessionData
	}{
		{name: "anonymous", path: "/admin/logs"},
		{name: "another user", path: "/admin/logs", session: adminSession(testOtherUser, time.Minute, now)},
		{name: "stale owner session", path: "/admin/logs", session: adminSession(testOwnerID, adminMaxSessionAge+time.Minute, now)},
		{name: "no issue time", path: "/admin/logs", session: &sessionData{DiscordUserID: testOwnerID}},
		{name: "wrong path", path: "/admin/log", session: adminSession(testOwnerID, time.Minute, now)},
	}

	for _, tc := range cases {
		w := httptest.NewRecorder()
		ws.handleAdminLogs(w, formRequest(t, ws, tc.session, http.MethodGet, tc.path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want %d", tc.name, w.Code, http.StatusNotFound)
		}
		if wires := len(ws.logs.(*stubLogWindow).queries); wires != 0 {
			t.Errorf("%s: the window was read for a refused request", tc.name)
		}
	}
}

// TestHandleAdminLogs_RendersTheWindow covers the owner path: the page has to
// carry the window the module returned, with its header.
func TestHandleAdminLogs_RendersTheWindow(t *testing.T) {
	now := time.Now()
	window := &stubLogWindow{window: logs.Window{
		Lines: []logbuf.Line{{
			Time:    now,
			Level:   slog.LevelWarn,
			Message: "gateway reconnect failed",
			Attrs:   "shard=0",
			Source:  "discordwatchdog.go:42",
		}},
		Held: 1,
	}}
	ws := newLogsTestServer(t, window, now)

	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, adminSession(testOwnerID, time.Minute, now), http.MethodGet, "/admin/logs", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Log lines",
		"1 of 1 held lines",
		"WARN gateway reconnect failed shard=0 (discordwatchdog.go:42)",
		`<select name="level"`,
		`action="/admin/logs"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

// TestHandleAdminLogs_ParsesTheQuery pins the parameter handling against the real
// module: the level, the filter and the count reach it, the count has a default,
// and the form shows back the size the module actually applied.
func TestHandleAdminLogs_ParsesTheQuery(t *testing.T) {
	now := time.Now()
	buf := logbuf.New(10)
	for i := 0; i < 3; i++ {
		buf.Add(logbuf.Line{Time: now, Level: slog.LevelWarn, Message: "timeout on shard 0"})
	}
	ws := newLogsTestServer(t, logs.New(buf), now)
	session := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, session, http.MethodGet, "/admin/logs", nil))
	body := w.Body.String()
	for _, want := range []string{`name="count" value="50"`, `<option value="all" selected>`, "3 of 3 held lines"} {
		if !strings.Contains(body, want) {
			t.Errorf("default page is missing %q", want)
		}
	}

	w = httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, session, http.MethodGet, "/admin/logs?level=warn&count=7&filter=timeout", nil))
	body = w.Body.String()
	// html/template escapes the quotes the header puts around the filter.
	for _, want := range []string{`name="count" value="7"`, `value="timeout"`, `<option value="warn" selected>`, `filter &#34;timeout&#34;`} {
		if !strings.Contains(body, want) {
			t.Errorf("filtered page is missing %q", want)
		}
	}

	// A count the window cannot honour is clamped by the module, and the form
	// then shows the applied value rather than the requested one.
	w = httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, session, http.MethodGet, "/admin/logs?level=warn&count=1000", nil))
	if body := w.Body.String(); !strings.Contains(body, `name="count" value="100"`) {
		t.Errorf("the form does not show the applied count: %s", body)
	}
}

// TestHandleAdminLogs_RejectsBadInput answers a hand-built URL with a client
// error instead of silently showing a window nobody asked for.
func TestHandleAdminLogs_RejectsBadInput(t *testing.T) {
	now := time.Now()
	ws := newLogsTestServer(t, &stubLogWindow{}, now)
	session := adminSession(testOwnerID, time.Minute, now)

	cases := map[string]int{
		"/admin/logs?level=trace": http.StatusBadRequest,
		"/admin/logs?count=abc":   http.StatusBadRequest,
	}
	for target, want := range cases {
		w := httptest.NewRecorder()
		ws.handleAdminLogs(w, formRequest(t, ws, session, http.MethodGet, target, nil))
		if w.Code != want {
			t.Errorf("%s: status = %d, want %d", target, w.Code, want)
		}
	}

	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, session, http.MethodPost, "/admin/logs", url.Values{}))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
	if got := w.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow = %q, want %q", got, http.MethodGet)
	}
}

// TestHandleAdminLogs_DownloadsTheWindow keeps the download link honest: it has
// to carry the window on screen and arrive as a text/plain attachment.
func TestHandleAdminLogs_DownloadsTheWindow(t *testing.T) {
	now := time.Now()
	window := &stubLogWindow{window: logs.Window{
		Lines: []logbuf.Line{{Time: now, Level: slog.LevelError, Message: "boom"}},
		Held:  1,
	}}
	ws := newLogsTestServer(t, window, now)

	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, adminSession(testOwnerID, time.Minute, now),
		http.MethodGet, "/admin/logs?level=error&count=5&filter=boom&format=raw", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, logsRawFilename) {
		t.Errorf("Content-Disposition = %q, want an attachment named %s", got, logsRawFilename)
	}
	body := w.Body.String()
	if !strings.Contains(body, "ERROR boom") {
		t.Errorf("download body = %q, want the window", body)
	}
	if got := window.queries[len(window.queries)-1]; got.Filter != "boom" || got.Count != 5 {
		t.Errorf("download query = %+v, want the page's window", got)
	}
}

// TestHandleAdminLogs_WithoutAWindow reports the missing module instead of
// asking anyone to guess why the page is empty.
func TestHandleAdminLogs_WithoutAWindow(t *testing.T) {
	now := time.Now()
	ws := newLogsTestServer(t, nil, now)

	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, adminSession(testOwnerID, time.Minute, now), http.MethodGet, "/admin/logs", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); !strings.Contains(body, logs.UnavailableMessage) {
		t.Errorf("page does not report the missing window: %s", body)
	}
}

// TestRoutes_ExposeTheLogPage proves the page is reachable at all: a route that
// was never registered is the same as a feature that was never added.
func TestRoutes_ExposeTheLogPage(t *testing.T) {
	now := time.Now()
	ws := newLogsTestServer(t, &stubLogWindow{}, now)

	// A non-owner still gets the handler's 404 rather than the mux's, which is
	// only true when the route exists.
	w := httptest.NewRecorder()
	ws.routes().ServeHTTP(w, formRequest(t, ws, nil, http.MethodGet, "/admin/logs", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	w = httptest.NewRecorder()
	ws.routes().ServeHTTP(w, formRequest(t, ws, adminSession(testOwnerID, time.Minute, now), http.MethodGet, "/admin/logs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "Log lines") {
		t.Error("the routed page is not the log viewer")
	}
}

// TestHandleAdminLogs_EscapesTheWindow keeps a log line from injecting markup
// into the page: log content is untrusted text.
func TestHandleAdminLogs_EscapesTheWindow(t *testing.T) {
	now := time.Now()
	window := &stubLogWindow{window: logs.Window{
		Lines: []logbuf.Line{{Time: now, Level: slog.LevelInfo, Message: "<script>alert(1)</script>"}},
		Held:  1,
	}}
	ws := newLogsTestServer(t, window, now)

	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, adminSession(testOwnerID, time.Minute, now), http.MethodGet, "/admin/logs", nil))

	body := w.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("a log line was rendered as markup")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("the log line is missing from the page: %s", body)
	}
}
