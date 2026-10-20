package gidbig

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/logscmd"
	"github.com/toksikk/gidbig/internal/logwindow"
)

// newLogsTestServer returns an owner-gated server whose log window holds a few
// records, including an HTML payload.
func newLogsTestServer(t *testing.T, now time.Time) (*webServer, *logwindow.Window) {
	t.Helper()
	w := logwindow.New(100)
	log := slog.New(w.Handler(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.Debug("debug detail")
	log.Info("voice connected", "guild", "g1")
	log.Warn("gateway slow <script>alert(1)</script>")
	log.Error("voice failed", "guild", "g2")
	ws := newAdminTestServer(nil, testOwnerID, "", now)
	ws.logs = w
	return ws, w
}

func TestHandleAdminLogs_DeniesEveryoneButTheOwner(t *testing.T) {
	now := time.Now()
	ws, _ := newLogsTestServer(t, now)
	for name, session := range map[string]*sessionData{
		"anonymous": nil,
		"non-owner": adminSession(testOtherUser, time.Minute, now),
		"expired":   adminSession(testOwnerID, adminMaxSessionAge+time.Minute, now),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ws.handleAdminLogs(w, formRequest(t, ws, session, http.MethodGet, "/admin/logs?download=1", nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
			if strings.Contains(w.Body.String(), "voice") {
				t.Error("refusal leaked log text")
			}
		})
	}
}

func TestHandleAdminLogs_NotFoundWithoutWindowOrOnAnotherPath(t *testing.T) {
	now := time.Now()
	ws, _ := newLogsTestServer(t, now)
	owner := adminSession(testOwnerID, time.Minute, now)
	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, owner, http.MethodGet, "/admin/logs/x", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("other path status = %d, want 404", w.Code)
	}
	ws.logs = nil
	w = httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, owner, http.MethodGet, "/admin/logs", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("no window status = %d, want 404", w.Code)
	}
}

func TestHandleAdminLogs_RejectsNonGet(t *testing.T) {
	now := time.Now()
	ws, _ := newLogsTestServer(t, now)
	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, adminSession(testOwnerID, time.Minute, now), http.MethodPost, "/admin/logs", nil))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status = %d Allow = %q", w.Code, w.Header().Get("Allow"))
	}
}

func TestHandleAdminLogs_OwnerPageFiltersAndEscapes(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	ws, _ := newLogsTestServer(t, now)
	ws.loadTemplates()
	owner := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, owner, http.MethodGet, "/admin/logs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "debug detail") {
		t.Error("default level floor should hide debug")
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("log line was not HTML-escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("escaped log line missing")
	}

	w = httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, owner, http.MethodGet, "/admin/logs?level=debug&filter=VOICE&count=1", nil))
	body = w.Body.String()
	if !strings.Contains(body, "voice failed") || strings.Contains(body, "voice connected") {
		t.Errorf("filtered page wrong: %s", body)
	}
}

// TestLogsSlashAndDownloadMatch is the no-drift check: the slash reply and the
// download carry the same lines for the same query.
func TestLogsSlashAndDownloadMatch(t *testing.T) {
	now := time.Now()
	ws, window := newLogsTestServer(t, now)
	owner := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, owner, http.MethodGet, "/admin/logs?level=warn&filter=&count=50&download=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="gidbig-logs.txt"` {
		t.Errorf("Content-Disposition = %q", cd)
	}

	q := logscmd.QueryFromOptions([]*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "level", Type: discordgo.ApplicationCommandOptionString, Value: "warn"},
		{Name: "count", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(50)},
	})
	msgs := logscmd.Render(window.Lines(q), logscmd.MaxMessages, 1900)
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	slash := strings.TrimSuffix(strings.TrimPrefix(msgs[0], "```\n"), "```")
	if slash != w.Body.String() {
		t.Errorf("slash text %q != download %q", slash, w.Body.String())
	}
	if !strings.Contains(slash, "voice failed") || strings.Contains(slash, "voice connected") {
		t.Errorf("unexpected lines: %q", slash)
	}
}

func TestRoutesRegistersAdminLogs(t *testing.T) {
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil, logwindow.New(1))
	mux := http.NewServeMux()
	ws.routes(mux)
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/admin/logs", nil))
	if pattern != "/admin/logs" {
		t.Errorf("pattern = %q, want /admin/logs", pattern)
	}
}

func TestSetupLoggingCapturesIntoWindow(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev); logLevel.Set(slog.LevelInfo) })
	c := &cfg.Config{}
	c.Logs.WindowLines = 5
	window := setupLogging(c)
	if window.Cap() != 5 {
		t.Errorf("Cap = %d, want 5", window.Cap())
	}
	slog.Info("captured by the window", "k", "v")
	lines := window.Lines(logwindow.Query{})
	if len(lines) != 1 || !strings.Contains(lines[0], "captured by the window k=v") {
		t.Errorf("lines = %v", lines)
	}
}

func TestHandleAdminLogs_DownloadLinkKeepsTheQuery(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	ws, _ := newLogsTestServer(t, now)
	ws.loadTemplates()
	w := httptest.NewRecorder()
	ws.handleAdminLogs(w, formRequest(t, ws, adminSession(testOwnerID, time.Minute, now), http.MethodGet, "/admin/logs?level=warn&filter=a+b&count=3&evil=x&download=0", nil))
	want := `href="/admin/logs?count=3&amp;download=1&amp;filter=a&#43;b&amp;level=warn"`
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("download link missing %s in:\n%s", want, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "evil") {
		t.Error("unknown query parameter leaked into the download link")
	}
}
