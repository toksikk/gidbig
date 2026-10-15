package gidbig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/configcmd"
	"github.com/toksikk/gidbig/internal/soundboard"
)

type stubEsoGenerator struct {
	text  string
	thema string
}

func (g *stubEsoGenerator) GenerateText(_ context.Context, thema string) string {
	g.thema = thema
	return g.text
}

type failingResponseWriter struct {
	header http.Header
}

func (w *failingResponseWriter) Header() http.Header { return w.header }
func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated write failure")
}
func (w *failingResponseWriter) WriteHeader(int) {}

func writeAudioDescription(t *testing.T, prefix, name, content string) {
	t.Helper()
	if err := os.MkdirAll("audio", 0o755); err != nil {
		t.Fatalf("mkdir audio: %v", err)
	}
	path := filepath.Join("audio", prefix+"_"+name+".txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestReadSoundDescription_missingFile(t *testing.T) {
	t.Chdir(t.TempDir())

	text, shortText, ok := readSoundDescription("nope", "missing")
	if ok {
		t.Fatal("ok = true, want false for missing file")
	}
	if text != "" || shortText != "" {
		t.Errorf("got (%q, %q), want empty strings", text, shortText)
	}
}

func TestReadSoundDescription_shortText(t *testing.T) {
	t.Chdir(t.TempDir())
	writeAudioDescription(t, "greet", "hi", "hello there")

	text, shortText, ok := readSoundDescription("greet", "hi")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if text != "hello there" {
		t.Errorf("text = %q, want %q", text, "hello there")
	}
	if shortText != "hello there" {
		t.Errorf("shortText = %q, want %q", shortText, "hello there")
	}
}

func TestReadSoundDescription_longTextTruncated(t *testing.T) {
	t.Chdir(t.TempDir())
	long := "this description is definitely longer than twenty characters"
	writeAudioDescription(t, "verbose", "clip", long)

	text, shortText, ok := readSoundDescription("verbose", "clip")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if text != long {
		t.Errorf("text = %q, want full text", text)
	}
	if !strings.HasSuffix(shortText, "...") {
		t.Errorf("shortText = %q, want trailing %q", shortText, "...")
	}
	if len(shortText) != 23 {
		t.Errorf("shortText len = %d, want 23 (20 + len(\"...\"))", len(shortText))
	}
	if !strings.HasPrefix(shortText, long[0:20]) {
		t.Errorf("shortText = %q, want prefix %q", shortText, long[0:20])
	}
}

func TestReadSoundDescription_emptyFile(t *testing.T) {
	t.Chdir(t.TempDir())
	writeAudioDescription(t, "blank", "clip", "")

	text, shortText, ok := readSoundDescription("blank", "clip")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if text != "" || shortText != "" {
		t.Errorf("got (%q, %q), want empty strings", text, shortText)
	}
}

func TestReadSoundDescription_onlyFirstLine(t *testing.T) {
	t.Chdir(t.TempDir())
	writeAudioDescription(t, "multi", "line", "first line\nsecond line\nthird line")

	text, _, ok := readSoundDescription("multi", "line")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if text != "first line" {
		t.Errorf("text = %q, want %q", text, "first line")
	}
}

func TestReadSoundDescription_doesNotLeakFD(t *testing.T) {
	t.Chdir(t.TempDir())
	writeAudioDescription(t, "leak", "test", "some description")

	for i := 0; i < 5000; i++ {
		_, _, ok := readSoundDescription("leak", "test")
		if !ok {
			t.Fatalf("call %d returned ok = false", i)
		}
	}
}

func TestSessionStore_EncryptDecrypt(t *testing.T) {
	s := newSessionStore("secret-key-123", false)
	data := &sessionData{
		DiscordUserID:   "user1",
		DiscordUsername: "user1_name",
	}

	w := httptest.NewRecorder()
	if err := s.Save(w, data); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected cookie in response")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])

	got := s.Get(req)
	if got.DiscordUserID != data.DiscordUserID || got.DiscordUsername != data.DiscordUsername {
		t.Errorf("got %+v, want %+v", got, data)
	}
}

func TestSessionStore_TamperedCookie(t *testing.T) {
	s := newSessionStore("secret-key-123", false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  sessionCookieName,
		Value: "tampered_base64_string",
	})

	got := s.Get(req)
	if got.DiscordUserID != "" {
		t.Errorf("expected empty session for tampered cookie, got %+v", got)
	}
}

func TestNewWebServer_NilModulesAreNilInterfaces(t *testing.T) {
	// A nil *eso.Module / *soundboard.Module must become a nil interface so the
	// handlers' nil checks still work; a typed nil in an interface would not be
	// caught by `ws.eso == nil`.
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil)
	if ws.soundboard != nil {
		t.Error("nil soundboard module should be a nil interface")
	}
	if ws.eso != nil {
		t.Error("nil eso module should be a nil interface")
	}
}

func TestHandleAPIQueue_unauthorized(t *testing.T) {
	ws := &webServer{store: newSessionStore("test-secret", false)}

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	w := httptest.NewRecorder()
	ws.handleAPIQueue(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestHandleAPIQueue_emptyQueue(t *testing.T) {
	ws := &webServer{store: newSessionStore("test-secret", false)}

	// Create a session with a logged-in user by saving it to a recorder first.
	setRec := httptest.NewRecorder()
	sess := &sessionData{DiscordUserID: "12345", IssuedAt: time.Now().Unix()}
	if err := ws.store.Save(setRec, sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	for _, c := range setRec.Result().Cookies() {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	ws.handleAPIQueue(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected %d, got %d", http.StatusOK, w.Code)
	}
	var result map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if _, ok := result["guilds"]; !ok {
		t.Error("expected 'guilds' key in response")
	}
}

func TestHandleAPIEso_success(t *testing.T) {
	generator := &stubEsoGenerator{text: "Kosmische Energie fließt."}
	req := httptest.NewRequest(http.MethodPost, "/api/eso", strings.NewReader(`{"thema":"Kristalle"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleAPIEsoWithGenerator(w, req, generator)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	if generator.thema != "Kristalle" {
		t.Errorf("thema = %q, want %q", generator.thema, "Kristalle")
	}
	var response map[string]string
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["text"] != generator.text {
		t.Errorf("text = %q, want %q", response["text"], generator.text)
	}
}

func TestHandleAPIEso_moduleUnavailable(t *testing.T) {
	ws := &webServer{}

	req := httptest.NewRequest(http.MethodPost, "/api/eso", nil)
	w := httptest.NewRecorder()

	ws.handleAPIEso(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleAPIEso_acceptsGetWithoutTopic(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/eso", nil)
	w := httptest.NewRecorder()
	generator := &stubEsoGenerator{text: "response"}

	handleAPIEsoWithGenerator(w, req, generator)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if generator.thema != "" {
		t.Errorf("thema = %q, want empty", generator.thema)
	}
}

func TestHandleAPIEso_rejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/api/eso", nil)
	w := httptest.NewRecorder()

	handleAPIEsoWithGenerator(w, req, &stubEsoGenerator{})

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
	if allow := w.Header().Get("Allow"); allow != "GET, POST" {
		t.Errorf("Allow = %q, want %q", allow, "GET, POST")
	}
}

func TestHandleAPIEso_rejectsLongThema(t *testing.T) {
	thema := strings.Repeat("ä", 201)
	req := httptest.NewRequest(http.MethodPost, "/api/eso", strings.NewReader(`{"thema":"`+thema+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	generator := &stubEsoGenerator{}

	handleAPIEsoWithGenerator(w, req, generator)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if generator.thema != "" {
		t.Error("generator called for invalid thema")
	}
}

func TestHandleAPIEso_acceptsMissingBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/eso", nil)
	w := httptest.NewRecorder()
	generator := &stubEsoGenerator{text: "response"}

	handleAPIEsoWithGenerator(w, req, generator)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if generator.thema != "" {
		t.Errorf("thema = %q, want empty", generator.thema)
	}
}

func TestHandleAPIEso_rejectsInvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/eso", strings.NewReader(`{"thema":`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleAPIEsoWithGenerator(w, req, &stubEsoGenerator{})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAPIEso_requiresJSONContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/eso", strings.NewReader(`{"thema":"Kristalle"}`))
	w := httptest.NewRecorder()

	handleAPIEsoWithGenerator(w, req, &stubEsoGenerator{})

	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnsupportedMediaType)
	}
}

func TestHandleAPIEso_logsRequestAndEncodingFailure(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	req := httptest.NewRequest(http.MethodPost, "/api/eso", nil)
	w := &failingResponseWriter{header: make(http.Header)}
	handleAPIEsoWithGenerator(w, req, &stubEsoGenerator{text: "response"})

	output := logs.String()
	if !strings.Contains(output, "ESO API request") {
		t.Errorf("request log missing: %s", output)
	}
	if !strings.Contains(output, "ESO API response encoding failed") {
		t.Errorf("encoding error log missing: %s", output)
	}
}

func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected %d, got %d", http.StatusOK, w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	if w.Body.String() != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", w.Body.String(), `{"status":"ok"}`)
	}
}

// guardedTestWebServer builds a web server whose only endpoint is /api/eso on a
// generator, with the production guards installed.
func guardedTestWebServer(gen esoGenerator) *webServer {
	return &webServer{
		eso:            gen,
		esoLimiter:     newIPRateLimiter(esoRateLimitBurst, esoRateLimitPerSecond),
		esoConcurrency: newConcurrencyLimiter(esoMaxConcurrentRequests),
		esoTimeout:     esoRequestTimeout,
	}
}

func TestNewWebServer_AppliesEsoConfig(t *testing.T) {
	config := &cfg.Config{}
	config.Web.Eso.RateLimitBurst = 2
	config.Web.Eso.RateLimitPerMinute = 60
	config.Web.Eso.MaxConcurrentRequests = 1
	config.Web.Eso.RequestTimeoutSeconds = 3

	ws := newWebServer(config, nil, nil, nil, nil)

	if ws.esoLimiter.burst != 2 {
		t.Errorf("burst = %v, want 2", ws.esoLimiter.burst)
	}
	if ws.esoLimiter.refillRate != 1 {
		t.Errorf("refillRate = %v, want 1", ws.esoLimiter.refillRate)
	}
	if ws.esoTimeout != 3*time.Second {
		t.Errorf("esoTimeout = %v, want %v", ws.esoTimeout, 3*time.Second)
	}
	if !ws.esoConcurrency.tryAcquire() {
		t.Fatal("first slot rejected with max_concurrent_requests = 1")
	}
	if ws.esoConcurrency.tryAcquire() {
		t.Error("configured concurrency cap of 1 not enforced")
	}
	ws.esoConcurrency.release()

	// The configured burst is what the endpoint actually enforces.
	ws.eso = &stubEsoGenerator{text: "response"}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		ws.handleAPIEso(w, httptest.NewRequest(http.MethodGet, "/api/eso", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d", i+1, w.Code, http.StatusOK)
		}
	}
	w := httptest.NewRecorder()
	ws.handleAPIEso(w, httptest.NewRequest(http.MethodGet, "/api/eso", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
}

func TestNewWebServer_DefaultsEsoLimitsWhenUnset(t *testing.T) {
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil)

	if ws.esoLimiter.burst != esoRateLimitBurst || ws.esoLimiter.refillRate != esoRateLimitPerSecond {
		t.Errorf("unset config changed the rate limit: burst=%v refill=%v",
			ws.esoLimiter.burst, ws.esoLimiter.refillRate)
	}
	if ws.esoTimeout != esoRequestTimeout {
		t.Errorf("esoTimeout = %v, want %v", ws.esoTimeout, esoRequestTimeout)
	}
}

func TestHandleAPIEso_RateLimitsPerClient(t *testing.T) {
	generator := &stubEsoGenerator{text: "response"}
	ws := guardedTestWebServer(generator)

	for i := 0; i < esoRateLimitBurst; i++ {
		w := httptest.NewRecorder()
		ws.handleAPIEso(w, httptest.NewRequest(http.MethodGet, "/api/eso", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d", i+1, w.Code, http.StatusOK)
		}
	}

	w := httptest.NewRecorder()
	ws.handleAPIEso(w, httptest.NewRequest(http.MethodGet, "/api/eso", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	retryAfter, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Errorf("Retry-After = %q, want a positive integer", w.Header().Get("Retry-After"))
	}
}

func TestHandleAPIEso_RateLimitIsPerAddress(t *testing.T) {
	ws := guardedTestWebServer(&stubEsoGenerator{text: "response"})

	for i := 0; i < esoRateLimitBurst; i++ {
		ws.handleAPIEso(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/eso", nil))
	}
	// Same address again is rejected.
	w := httptest.NewRecorder()
	ws.handleAPIEso(w, httptest.NewRequest(http.MethodGet, "/api/eso", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("same address status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// A different client still gets a full budget.
	other := httptest.NewRequest(http.MethodGet, "/api/eso", nil)
	other.RemoteAddr = "198.51.100.7:5555"
	w = httptest.NewRecorder()
	ws.handleAPIEso(w, other)
	if w.Code != http.StatusOK {
		t.Errorf("other address status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleAPIEso_RejectsWhenConcurrencySaturated(t *testing.T) {
	generator := &stubEsoGenerator{text: "response"}
	ws := guardedTestWebServer(generator)
	for i := 0; i < esoMaxConcurrentRequests; i++ {
		if !ws.esoConcurrency.tryAcquire() {
			t.Fatalf("could not fill concurrency slot %d", i+1)
		}
	}

	w := httptest.NewRecorder()
	ws.handleAPIEso(w, httptest.NewRequest(http.MethodGet, "/api/eso", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}
	if generator.thema != "" {
		t.Error("generator called while saturated")
	}
}

func TestHandleAPIEso_ReleasesConcurrencySlot(t *testing.T) {
	ws := guardedTestWebServer(&stubEsoGenerator{text: "response"})

	ws.handleAPIEso(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/eso", nil))

	for i := 0; i < esoMaxConcurrentRequests; i++ {
		if !ws.esoConcurrency.tryAcquire() {
			t.Fatalf("slot %d not released after the request", i+1)
		}
	}
}

// ctxObservingEsoGenerator blocks until its context is done and reports the
// cancellation cause, so a test can prove the request timeout reached it.
type ctxObservingEsoGenerator struct {
	ctxErr chan error
}

func (g *ctxObservingEsoGenerator) GenerateText(ctx context.Context, _ string) string {
	select {
	case <-ctx.Done():
		g.ctxErr <- ctx.Err()
	case <-time.After(5 * time.Second):
		g.ctxErr <- nil
	}
	return "late response"
}

func TestHandleAPIEso_AppliesRequestTimeout(t *testing.T) {
	generator := &ctxObservingEsoGenerator{ctxErr: make(chan error, 1)}
	ws := guardedTestWebServer(generator)
	ws.esoTimeout = 20 * time.Millisecond

	w := httptest.NewRecorder()
	ws.handleAPIEso(w, httptest.NewRequest(http.MethodGet, "/api/eso", nil))

	if err := <-generator.ctxErr; err != context.DeadlineExceeded {
		t.Errorf("generator context error = %v, want %v", err, context.DeadlineExceeded)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestNewWebServer_WiresTheAdminPanel covers the wiring the composition root
// does: the settings module, the owner the panel gates on, and the origin a
// submission is checked against.
func TestNewWebServer_WiresTheAdminPanel(t *testing.T) {
	config := &cfg.Config{}
	config.Discord.OwnerID = "owner-9"
	config.Web.Oauth.RedirectURI = "https://gidbig.example"

	ws := newWebServer(config, nil, nil, nil, configcmd.New())

	if ws.config == nil {
		t.Error("the settings module was not wired into the panel")
	}
	if ws.ownerID != "owner-9" {
		t.Errorf("ownerID = %q, want the configured owner", ws.ownerID)
	}
	if ws.uiOrigin != "https://gidbig.example" {
		t.Errorf("uiOrigin = %q, want the scheme and host of the redirect URI", ws.uiOrigin)
	}
	if ws.currentTime().IsZero() {
		t.Error("no clock was wired")
	}
}

// TestNewWebServer_AdminPanelDefaults keeps a deployment without a settings
// module, or without a parseable redirect URI, from producing a half-wired
// panel.
func TestNewWebServer_AdminPanelDefaults(t *testing.T) {
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil)
	if ws.config != nil {
		t.Error("a nil settings module should stay a nil interface")
	}
	if ws.ownerID != "" || ws.uiOrigin != "" {
		t.Errorf("ownerID = %q, uiOrigin = %q, want both empty", ws.ownerID, ws.uiOrigin)
	}

	config := &cfg.Config{}
	config.Web.Oauth.RedirectURI = "localhost:8080"
	ws = newWebServer(config, nil, nil, nil, nil)
	if ws.uiOrigin != "" {
		t.Errorf("uiOrigin = %q for a redirect URI without a scheme, want empty", ws.uiOrigin)
	}
}

// TestCompleteLogin_StampsTheLoginTime covers the line the panel's freshness gate
// depends on: an OAuth login must record when it happened and drop the previous
// session's form token.
func TestCompleteLogin_StampsTheLoginTime(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ws := &webServer{now: func() time.Time { return now }}
	session := &sessionData{
		AdminCSRF: "token-from-the-previous-login",
		Flash:     "Removed the runtime override for `llm.model`.",
		FlashOK:   true,
	}

	ws.completeLogin(session, &discordgo.User{ID: "42", Username: "someone", Avatar: "abc"})

	if session.IssuedAt != now.Unix() {
		t.Errorf("IssuedAt = %d, want %d", session.IssuedAt, now.Unix())
	}
	if session.AdminCSRF != "" {
		t.Errorf("AdminCSRF = %q, want it cleared by the new login", session.AdminCSRF)
	}
	if session.Flash != "" || session.FlashOK {
		t.Errorf("flash = %q (ok=%v), want it cleared by the new login", session.Flash, session.FlashOK)
	}
	if session.DiscordUserID != "42" || session.DiscordUsername != "someone" {
		t.Errorf("session = %+v, want the authenticated user", session)
	}
	if want := "https://cdn.discordapp.com/avatars/42/abc.png"; session.DiscordAvatarURL != want {
		t.Errorf("DiscordAvatarURL = %q, want %q", session.DiscordAvatarURL, want)
	}

	// The stamped session is exactly what the panel accepts, while one without
	// the stamp is refused.
	admin := &webServer{ownerID: "42", now: func() time.Time { return now }}
	if !admin.isOwner(session) {
		t.Error("a freshly stamped session is not accepted by the panel")
	}
	stale := &sessionData{DiscordUserID: "42"}
	if admin.isOwner(stale) {
		t.Error("a session without a login time is accepted by the panel")
	}

	// A user without an avatar keeps an empty URL.
	noAvatar := &sessionData{}
	ws.completeLogin(noAvatar, &discordgo.User{ID: "42", Username: "someone"})
	if noAvatar.DiscordAvatarURL != "" {
		t.Errorf("DiscordAvatarURL = %q, want empty without an avatar", noAvatar.DiscordAvatarURL)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", csp)
	}
}

func TestNewHTTPServer_Timeouts(t *testing.T) {
	esoTimeout := 20 * time.Second
	srv := newHTTPServer(8080, http.NewServeMux(), esoTimeout)
	if srv.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Errorf("timeouts not set: %+v", srv)
	}
	if srv.WriteTimeout <= esoTimeout {
		t.Errorf("WriteTimeout = %v, want > eso timeout %v", srv.WriteTimeout, esoTimeout)
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Errorf("MaxHeaderBytes = %d, want > 0", srv.MaxHeaderBytes)
	}
}

func TestFinishDiscordLogin_UserLookupFailure(t *testing.T) {
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil)
	ws.fetchDiscordUser = func(string) (*discordgo.User, error) { return nil, errors.New("boom") }
	sess := &sessionData{}
	rec := httptest.NewRecorder()
	ws.finishDiscordLogin(rec, httptest.NewRequest(http.MethodGet, "/discordCallback", nil), sess, "token")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if sess.DiscordUserID != "" {
		t.Errorf("session populated on failure: %+v", sess)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Error("session cookie written on failure")
	}
}

func TestFinishDiscordLogin_Success(t *testing.T) {
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil)
	ws.store = newSessionStore("test-secret-test-secret-test-secret", false)
	ws.fetchDiscordUser = func(string) (*discordgo.User, error) {
		return &discordgo.User{ID: "42", Username: "alice"}, nil
	}
	sess := &sessionData{}
	rec := httptest.NewRecorder()
	ws.finishDiscordLogin(rec, httptest.NewRequest(http.MethodGet, "/discordCallback", nil), sess, "token")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}
	if sess.DiscordUserID != "42" {
		t.Errorf("session = %+v, want user 42", sess)
	}
}

// TestDiscordLogin_StateIsPerSession checks that each login keeps its OAuth
// state in its own cookie and nowhere shared.
func TestDiscordLogin_StateIsPerSession(t *testing.T) {
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil)

	login := func() (string, string) {
		rec := httptest.NewRecorder()
		ws.handleDiscordLogin(rec, httptest.NewRequest(http.MethodGet, "/discordLogin", nil))
		if rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
		}
		loc, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatalf("parse redirect: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		return ws.store.Get(req).State, loc.Query().Get("state")
	}

	stored1, sent1 := login()
	stored2, sent2 := login()
	if stored1 == "" || stored1 != sent1 || stored2 != sent2 {
		t.Fatalf("state not stored in the session: %q/%q, %q/%q", stored1, sent1, stored2, sent2)
	}
	if stored1 == stored2 {
		t.Error("two logins share one state")
	}
}

func callbackRequest(t *testing.T, ws *webServer, sessionState, queryState string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/discordCallback?code=c&state="+url.QueryEscape(queryState), nil)
	rec := httptest.NewRecorder()
	if err := ws.store.Save(rec, &sessionData{State: sessionState}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

func TestDiscordCallback_RejectsStateMismatch(t *testing.T) {
	logs := captureLogs(t)
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil)
	rec := httptest.NewRecorder()
	ws.handleDiscordCallback(rec, callbackRequest(t, ws, "expected-state-value", "attacker-state-value"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if out := logs.String(); strings.Contains(out, "expected-state-value") || strings.Contains(out, "attacker-state-value") {
		t.Errorf("state value logged: %s", out)
	}
}

func TestDiscordCallback_RejectsEmptyState(t *testing.T) {
	ws := newWebServer(&cfg.Config{}, nil, nil, nil, nil)
	rec := httptest.NewRecorder()
	ws.handleDiscordCallback(rec, callbackRequest(t, ws, "", ""))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestSessionStore_SaveSetsSecureAndMaxAge(t *testing.T) {
	for _, secure := range []bool{true, false} {
		s := newSessionStore("secret", secure)
		rec := httptest.NewRecorder()
		if err := s.Save(rec, &sessionData{DiscordUserID: "1"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		c := rec.Result().Cookies()[0]
		if c.Secure != secure || !c.HttpOnly || c.MaxAge != int(sessionMaxAge.Seconds()) {
			t.Errorf("secure=%v: cookie = %+v", secure, c)
		}
		rec = httptest.NewRecorder()
		s.Clear(rec)
		c = rec.Result().Cookies()[0]
		if c.Secure != secure || c.MaxAge >= 0 {
			t.Errorf("secure=%v: cleared cookie = %+v", secure, c)
		}
	}
}

func TestNewWebServer_SecureFromRedirectURI(t *testing.T) {
	for uri, want := range map[string]bool{
		"https://gidbig.example": true,
		"http://localhost:8080":  false,
		"":                       false,
	} {
		config := &cfg.Config{}
		config.Web.Oauth.RedirectURI = uri
		if got := newWebServer(config, nil, nil, nil, nil).store.secure; got != want {
			t.Errorf("%q: secure = %v, want %v", uri, got, want)
		}
	}
}

func TestCompleteLogin_ClearsStateAndStoresNoToken(t *testing.T) {
	ws := &webServer{store: newSessionStore("secret", false), now: time.Now}
	session := &sessionData{State: "pending-state"}
	ws.completeLogin(session, &discordgo.User{ID: "42", Username: "someone"})
	if session.State != "" {
		t.Errorf("State = %q, want it cleared", session.State)
	}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "accesstoken") {
		t.Errorf("session JSON carries an access token: %s", raw)
	}

	// An old cookie that still carries the key decodes fine.
	old := &sessionData{}
	if err := json.Unmarshal([]byte(`{"discordUserID":"1","accessToken":"x"}`), old); err != nil || old.DiscordUserID != "1" {
		t.Errorf("old cookie decode: %+v, %v", old, err)
	}
}

func TestValidSession(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ws := &webServer{now: func() time.Time { return now }}
	at := func(age time.Duration) int64 { return now.Add(-age).Unix() }
	cases := []struct {
		name    string
		session *sessionData
		want    bool
	}{
		{"nil", nil, false},
		{"no user", &sessionData{IssuedAt: at(time.Minute)}, false},
		{"no issued at", &sessionData{DiscordUserID: "1"}, false},
		{"fresh", &sessionData{DiscordUserID: "1", IssuedAt: at(time.Minute)}, true},
		{"7d edge", &sessionData{DiscordUserID: "1", IssuedAt: at(sessionMaxAge)}, true},
		{"7d+1s", &sessionData{DiscordUserID: "1", IssuedAt: at(sessionMaxAge + time.Second)}, false},
		{"future", &sessionData{DiscordUserID: "1", IssuedAt: at(-time.Minute)}, false},
	}
	for _, tc := range cases {
		if got := ws.validSession(tc.session); got != tc.want {
			t.Errorf("%s: validSession = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHandleMain_ExpiredSessionIsLoggedOut(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	ws := newAdminTestServer(&stubConfigAdmin{}, testOwnerID, "", now)
	ws.loadTemplates()
	expired := adminSession(testOtherUser, sessionMaxAge+time.Minute, now)
	w := httptest.NewRecorder()
	ws.handleMain(w, formRequest(t, ws, expired, http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "sound-search") {
		t.Error("an expired session still sees the sound board")
	}

	w = httptest.NewRecorder()
	ws.handleMain(w, formRequest(t, ws, adminSession(testOtherUser, sessionMaxAge-time.Minute, now), http.MethodGet, "/", nil))
	if !strings.Contains(w.Body.String(), "sound-search") {
		t.Error("a valid session does not see the sound board")
	}
}

// fakeSoundboard records Enqueue calls and serves a fixed queue snapshot.
type fakeSoundboard struct {
	enqueued []string
	queue    []soundboard.QueueStatus
}

func (f *fakeSoundboard) Enqueue(_ *discordgo.User, _ *discordgo.Guild, command, soundname string) bool {
	f.enqueued = append(f.enqueued, command+" "+soundname)
	return true
}
func (f *fakeSoundboard) QueueStatus() []soundboard.QueueStatus { return f.queue }
func (f *fakeSoundboard) Collections() []soundboard.Collection  { return nil }

func playSoundServer(sb *fakeSoundboard) *webServer {
	ws := newAdminTestServer(nil, "", "https://gidbig.example", time.Now())
	ws.soundboard = sb
	return ws
}

func playSoundRequest(t *testing.T, ws *webServer, session *sessionData, method, origin string) *http.Request {
	t.Helper()
	req := formRequest(t, ws, session, method, "/playsound", url.Values{"command": {"!x"}, "soundname": {"y"}})
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func TestHandlePlaySound_RejectsGet(t *testing.T) {
	sb := &fakeSoundboard{}
	ws := playSoundServer(sb)
	w := httptest.NewRecorder()
	ws.handlePlaySound(w, playSoundRequest(t, ws, adminSession("u", time.Minute, time.Now()), http.MethodGet, ""))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
		t.Errorf("status = %d, Allow = %q", w.Code, w.Header().Get("Allow"))
	}
	if len(sb.enqueued) != 0 {
		t.Error("GET enqueued a sound")
	}
}

func TestHandlePlaySound_RejectsForeignOrigin(t *testing.T) {
	sb := &fakeSoundboard{}
	ws := playSoundServer(sb)
	w := httptest.NewRecorder()
	ws.handlePlaySound(w, playSoundRequest(t, ws, adminSession("u", time.Minute, time.Now()), http.MethodPost, "https://evil.example"))
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	if len(sb.enqueued) != 0 {
		t.Error("a foreign origin enqueued a sound")
	}
}

func TestHandlePlaySound_AcceptsSameOrigin(t *testing.T) {
	for _, origin := range []string{"https://gidbig.example", ""} {
		sb := &fakeSoundboard{}
		ws := playSoundServer(sb)
		w := httptest.NewRecorder()
		ws.handlePlaySound(w, playSoundRequest(t, ws, adminSession("u", time.Minute, time.Now()), http.MethodPost, origin))
		if w.Code != http.StatusOK || len(sb.enqueued) != 1 || sb.enqueued[0] != "!x y" {
			t.Errorf("origin %q: status = %d, enqueued = %v", origin, w.Code, sb.enqueued)
		}
	}
}

func TestHandlePlaySound_Unauthorized(t *testing.T) {
	sb := &fakeSoundboard{}
	ws := playSoundServer(sb)
	w := httptest.NewRecorder()
	ws.handlePlaySound(w, playSoundRequest(t, ws, nil, http.MethodPost, ""))
	if w.Code != http.StatusUnauthorized || len(sb.enqueued) != 0 {
		t.Errorf("status = %d, enqueued = %v", w.Code, sb.enqueued)
	}
}

func TestHandlePlaySound_ExpiredSession(t *testing.T) {
	sb := &fakeSoundboard{}
	ws := playSoundServer(sb)
	w := httptest.NewRecorder()
	ws.handlePlaySound(w, playSoundRequest(t, ws, adminSession("u", sessionMaxAge+time.Minute, time.Now()), http.MethodPost, ""))
	if w.Code != http.StatusUnauthorized || len(sb.enqueued) != 0 {
		t.Errorf("status = %d, enqueued = %v", w.Code, sb.enqueued)
	}
}

func TestHandleAPIQueue_OnlyCallersVoiceGuild(t *testing.T) {
	sb := &fakeSoundboard{queue: []soundboard.QueueStatus{
		{GuildID: "g1", NowPlaying: "mine", QueueLength: 1},
		{GuildID: "g2", NowPlaying: "theirs", QueueLength: 2},
	}}
	ws := playSoundServer(sb)

	query := func(voice map[string]bool) []guildQueueStatus {
		t.Helper()
		var gotUser string
		ws.voiceGuilds = func(userID string) map[string]bool { gotUser = userID; return voice }
		w := httptest.NewRecorder()
		ws.handleAPIQueue(w, formRequest(t, ws, adminSession("u", time.Minute, time.Now()), http.MethodGet, "/api/queue", nil))
		if w.Code != http.StatusOK || gotUser != "u" {
			t.Fatalf("status = %d, user = %q", w.Code, gotUser)
		}
		var body struct {
			Guilds []guildQueueStatus `json:"guilds"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Guilds == nil {
			t.Fatal("guilds is null, want an array")
		}
		return body.Guilds
	}

	if got := query(map[string]bool{"g1": true}); len(got) != 1 || got[0].GuildID != "g1" {
		t.Errorf("in voice on g1: guilds = %+v", got)
	}
	if got := query(map[string]bool{}); len(got) != 0 {
		t.Errorf("not in voice: guilds = %+v", got)
	}
}

func TestStateVoiceGuilds(t *testing.T) {
	state := discordgo.NewState()
	for _, g := range []*discordgo.Guild{
		{ID: "g1", VoiceStates: []*discordgo.VoiceState{{UserID: "u", GuildID: "g1"}}},
		{ID: "g2", VoiceStates: []*discordgo.VoiceState{{UserID: "other", GuildID: "g2"}}},
	} {
		if err := state.GuildAdd(g); err != nil {
			t.Fatalf("GuildAdd: %v", err)
		}
	}
	ws := &webServer{session: &discordgo.Session{State: state}}
	got := ws.stateVoiceGuilds("u")
	if len(got) != 1 || !got["g1"] {
		t.Errorf("voice guilds = %v, want only g1", got)
	}
	if got := (&webServer{}).stateVoiceGuilds("u"); len(got) != 0 {
		t.Errorf("no session: %v", got)
	}
}

func TestInternalTemplate_NoInnerHTML(t *testing.T) {
	raw, err := os.ReadFile("../../web/templates/internal.html")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if strings.Contains(string(raw), "innerHTML") {
		t.Error("internal.html uses innerHTML; build the DOM with textContent")
	}
}
