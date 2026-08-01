package gidbig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	s := newSessionStore("secret-key-123")
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
	s := newSessionStore("secret-key-123")
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

func TestHandleAPIQueue_unauthorized(t *testing.T) {
	store = newSessionStore("test-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	w := httptest.NewRecorder()
	handleAPIQueue(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestHandleAPIQueue_emptyQueue(t *testing.T) {
	store = newSessionStore("test-secret")

	// Create a session with a logged-in user by saving it to a recorder first.
	setRec := httptest.NewRecorder()
	sess := &sessionData{DiscordUserID: "12345"}
	if err := store.Save(setRec, sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	for _, c := range setRec.Result().Cookies() {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleAPIQueue(w, req)

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
	previousEsoMod := esoMod
	esoMod = nil
	t.Cleanup(func() { esoMod = previousEsoMod })

	req := httptest.NewRequest(http.MethodPost, "/api/eso", nil)
	w := httptest.NewRecorder()

	handleAPIEso(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleAPIEso_rejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/eso", nil)
	w := httptest.NewRecorder()

	handleAPIEsoWithGenerator(w, req, &stubEsoGenerator{})

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
	if allow := w.Header().Get("Allow"); allow != http.MethodPost {
		t.Errorf("Allow = %q, want %q", allow, http.MethodPost)
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
