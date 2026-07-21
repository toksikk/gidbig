package gidbig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
