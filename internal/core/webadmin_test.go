package gidbig

import (
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/configcmd"
	"github.com/toksikk/gidbig/internal/settings"
)

const (
	testOwnerID   = "owner-1"
	testOtherUser = "someone-else"
)

// errTestInvalidCombination stands in for the store's own error when a stored
// row and config.yaml disagree.
var errTestInvalidCombination = errors.New("gippity.tools.max_iterations 0 is not usable while tools are enabled")

// stubConfigAdmin records what the panel asked for and returns canned replies,
// so the handler's gating and flash handling can be tested without a database.
type stubConfigAdmin struct {
	entries    []configcmd.Entry
	entriesErr error

	setCalls   []adminCall
	unsetCalls []adminCall
	message    string
	ok         bool
}

type adminCall struct {
	Key   string
	Value string
	Actor string
}

func (s *stubConfigAdmin) Entries() ([]configcmd.Entry, error) {
	return s.entries, s.entriesErr
}

func (s *stubConfigAdmin) ApplySet(key, value, actor string) (string, bool) {
	s.setCalls = append(s.setCalls, adminCall{Key: key, Value: value, Actor: actor})
	return s.message, s.ok
}

func (s *stubConfigAdmin) ApplyUnset(key, actor string) (string, bool) {
	s.unsetCalls = append(s.unsetCalls, adminCall{Key: key, Actor: actor})
	return s.message, s.ok
}

// newAdminTestServer builds a web server whose clock is fixed, so a test can age
// a session without sleeping.
func newAdminTestServer(admin configAdmin, ownerID, origin string, now time.Time) *webServer {
	return &webServer{
		store:       newSessionStore("test-secret"),
		config:      admin,
		ownerID:     ownerID,
		adminOrigin: origin,
		now:         func() time.Time { return now },
		tmpls:       map[string]*template.Template{},
	}
}

// adminSession returns a session for user that logged in age ago.
func adminSession(user string, age time.Duration, now time.Time) *sessionData {
	return &sessionData{
		DiscordUserID:   user,
		DiscordUsername: user,
		IssuedAt:        now.Add(-age).Unix(),
		AdminCSRF:       "token-" + user,
	}
}

func formRequest(t *testing.T, ws *webServer, session *sessionData, method, target string, form url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if session != nil {
		rec := httptest.NewRecorder()
		if err := ws.store.Save(rec, session); err != nil {
			t.Fatalf("save session: %v", err)
		}
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
	}
	if form != nil {
		body := form.Encode()
		req.Body = io.NopCloser(strings.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

// sessionFromResponse reads the session the handler saved back out of the
// response cookies.
func sessionFromResponse(t *testing.T, ws *webServer, w *httptest.ResponseRecorder) *sessionData {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("response set no session cookie")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return ws.store.Get(req)
}

// TestHandleAdmin_DeniesEveryoneButTheOwner covers every refusal path: each one
// must look exactly like a missing page, so the panel cannot be discovered.
func TestHandleAdmin_DeniesEveryoneButTheOwner(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		admin   configAdmin
		ownerID string
		session *sessionData
	}{
		{name: "logged out", admin: &stubConfigAdmin{}, ownerID: testOwnerID},
		{name: "not the owner", admin: &stubConfigAdmin{}, ownerID: testOwnerID,
			session: adminSession(testOtherUser, time.Minute, now)},
		{name: "no owner configured", admin: &stubConfigAdmin{},
			session: adminSession(testOwnerID, time.Minute, now)},
		{name: "session without an issue time", admin: &stubConfigAdmin{}, ownerID: testOwnerID,
			session: &sessionData{DiscordUserID: testOwnerID, AdminCSRF: "token"}},
		{name: "session older than the window", admin: &stubConfigAdmin{}, ownerID: testOwnerID,
			session: adminSession(testOwnerID, adminMaxSessionAge+time.Minute, now)},
		{name: "no settings store", ownerID: testOwnerID,
			session: adminSession(testOwnerID, time.Minute, now)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := newAdminTestServer(tc.admin, tc.ownerID, "", now)

			for _, method := range []string{http.MethodGet, http.MethodPost} {
				form := url.Values{"action": {"set"}, "key": {"llm.model"}, "value": {"x"}, "csrf": {"token" + testOtherUser}}
				w := httptest.NewRecorder()
				ws.handleAdmin(w, formRequest(t, ws, tc.session, method, "/admin", form))

				if w.Code != http.StatusNotFound {
					t.Errorf("%s status = %d, want %d", method, w.Code, http.StatusNotFound)
				}
			}
			if stub, ok := tc.admin.(*stubConfigAdmin); ok {
				if len(stub.setCalls) != 0 || len(stub.unsetCalls) != 0 {
					t.Fatalf("a refused request reached the store: set=%v unset=%v", stub.setCalls, stub.unsetCalls)
				}
			}
		})
	}
}

// TestHandleAdmin_OwnerSessionAtTheWindowEdge pins the boundary: a session
// exactly at the limit is still usable, one second past it is not.
func TestHandleAdmin_OwnerSessionAtTheWindowEdge(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	admin := &stubConfigAdmin{entries: []configcmd.Entry{}}

	ws := newAdminTestServer(admin, testOwnerID, "", now)
	if !ws.isOwner(adminSession(testOwnerID, adminMaxSessionAge, now)) {
		t.Error("a session exactly at the window was rejected")
	}
	if ws.isOwner(adminSession(testOwnerID, adminMaxSessionAge+time.Second, now)) {
		t.Error("a session past the window was accepted")
	}
	if ws.isOwner(adminSession(testOwnerID, -time.Minute, now)) {
		t.Error("a session issued in the future was accepted")
	}
}

// TestHandleAdmin_NotFoundOnAnotherPath keeps the handler from acting as a
// catch-all behind the "/" route.
func TestHandleAdmin_NotFoundOnAnotherPath(t *testing.T) {
	now := time.Now()
	ws := newAdminTestServer(&stubConfigAdmin{}, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodGet, "/admin/anything", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleAdmin_RejectsUnsupportedMethod(t *testing.T) {
	now := time.Now()
	ws := newAdminTestServer(&stubConfigAdmin{}, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPut, "/admin", nil))

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
	if got := w.Header().Get("Allow"); !strings.Contains(got, http.MethodGet) || !strings.Contains(got, http.MethodPost) {
		t.Errorf("Allow = %q, want GET and POST", got)
	}
}

// TestHandleAdmin_GetNeverWrites makes sure a crafted query string cannot turn a
// page load into a change.
func TestHandleAdmin_GetNeverWrites(t *testing.T) {
	now := time.Now()
	admin := &stubConfigAdmin{}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodGet, "/admin?action=set&key=llm.model&value=evil", nil))

	if len(admin.setCalls) != 0 || len(admin.unsetCalls) != 0 {
		t.Fatalf("a GET reached the store: set=%v unset=%v", admin.setCalls, admin.unsetCalls)
	}
}

func TestHandleAdmin_PostAppliesSetAndRedirects(t *testing.T) {
	now := time.Now()
	admin := &stubConfigAdmin{message: "Set `llm.model=x`.", ok: true}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)

	form := url.Values{
		"action": {"set"},
		"key":    {settings.KeyLLMModel},
		"value":  {"new-model"},
		"csrf":   {session.AdminCSRF},
	}
	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPost, "/admin", form))

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if loc := w.Header().Get("Location"); loc != "/admin" {
		t.Errorf("Location = %q, want /admin", loc)
	}
	if len(admin.setCalls) != 1 {
		t.Fatalf("ApplySet calls = %v, want exactly one", admin.setCalls)
	}
	call := admin.setCalls[0]
	if call.Key != settings.KeyLLMModel || call.Value != "new-model" || call.Actor != testOwnerID {
		t.Errorf("ApplySet(%q, %q, %q), want the submitted key, value and the session user as actor", call.Key, call.Value, call.Actor)
	}

	// The outcome is handed to the next page load and not kept afterwards.
	after := sessionFromResponse(t, ws, w)
	if after.Flash != admin.message || !after.FlashOK {
		t.Errorf("flash = %q (ok=%v), want %q (ok=true)", after.Flash, after.FlashOK, admin.message)
	}
}

func TestHandleAdmin_PostAppliesUnset(t *testing.T) {
	now := time.Now()
	admin := &stubConfigAdmin{message: "Removed the runtime override.", ok: true}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)

	form := url.Values{"action": {"unset"}, "key": {settings.KeyLLMModel}, "csrf": {session.AdminCSRF}}
	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPost, "/admin", form))

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if len(admin.unsetCalls) != 1 || admin.unsetCalls[0].Key != settings.KeyLLMModel {
		t.Fatalf("ApplyUnset calls = %v, want one for the submitted key", admin.unsetCalls)
	}
	if len(admin.setCalls) != 0 {
		t.Errorf("unset also called ApplySet: %v", admin.setCalls)
	}
}

// TestHandleAdmin_PostRejectsABadToken covers the cross-site case: without the
// token the browser cannot produce, nothing is written.
func TestHandleAdmin_PostRejectsABadToken(t *testing.T) {
	now := time.Now()
	tokens := map[string]string{
		"missing": "",
		"wrong":   "not-the-token",
		"stolen":  "token-" + testOtherUser,
	}

	for name, token := range tokens {
		t.Run(name, func(t *testing.T) {
			admin := &stubConfigAdmin{}
			ws := newAdminTestServer(admin, testOwnerID, "", now)
			session := adminSession(testOwnerID, time.Minute, now)

			form := url.Values{"action": {"set"}, "key": {settings.KeyLLMModel}, "value": {"evil"}, "csrf": {token}}
			w := httptest.NewRecorder()
			ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPost, "/admin", form))

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
			}
			if len(admin.setCalls) != 0 {
				t.Fatalf("a submission without a valid token reached the store: %v", admin.setCalls)
			}
		})
	}
}

// TestHandleAdmin_PostRejectsAForeignOrigin covers the second defence, for a
// browser that sends the token it kept from an earlier page load.
func TestHandleAdmin_PostRejectsAForeignOrigin(t *testing.T) {
	now := time.Now()
	form := url.Values{"action": {"set"}, "key": {settings.KeyLLMModel}, "value": {"evil"}}

	t.Run("foreign origin", func(t *testing.T) {
		admin := &stubConfigAdmin{}
		ws := newAdminTestServer(admin, testOwnerID, "https://gidbig.example", now)
		session := adminSession(testOwnerID, time.Minute, now)
		form.Set("csrf", session.AdminCSRF)

		req := formRequest(t, ws, session, http.MethodPost, "/admin", form)
		req.Header.Set("Origin", "https://attacker.example")
		w := httptest.NewRecorder()
		ws.handleAdmin(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
		if len(admin.setCalls) != 0 {
			t.Fatalf("a foreign-origin submission reached the store: %v", admin.setCalls)
		}
	})

	t.Run("matching origin", func(t *testing.T) {
		admin := &stubConfigAdmin{ok: true}
		ws := newAdminTestServer(admin, testOwnerID, "https://gidbig.example", now)
		session := adminSession(testOwnerID, time.Minute, now)
		form.Set("csrf", session.AdminCSRF)

		req := formRequest(t, ws, session, http.MethodPost, "/admin", form)
		req.Header.Set("Origin", "https://gidbig.example")
		w := httptest.NewRecorder()
		ws.handleAdmin(w, req)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
		}
		if len(admin.setCalls) != 1 {
			t.Fatalf("ApplySet calls = %v, want one", admin.setCalls)
		}
	})

	t.Run("no configured origin falls back to the request host", func(t *testing.T) {
		admin := &stubConfigAdmin{ok: true}
		ws := newAdminTestServer(admin, testOwnerID, "", now)
		session := adminSession(testOwnerID, time.Minute, now)
		form.Set("csrf", session.AdminCSRF)

		req := formRequest(t, ws, session, http.MethodPost, "/admin", form)
		req.Header.Set("Origin", "http://example.com")
		w := httptest.NewRecorder()
		ws.handleAdmin(w, req)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
		}
	})
}

func TestHandleAdmin_PostRejectsUnknownAction(t *testing.T) {
	now := time.Now()
	admin := &stubConfigAdmin{}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)

	form := url.Values{"action": {"delete"}, "key": {settings.KeyLLMModel}, "csrf": {session.AdminCSRF}}
	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPost, "/admin", form))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if len(admin.setCalls) != 0 || len(admin.unsetCalls) != 0 {
		t.Errorf("an unknown action reached the store: set=%v unset=%v", admin.setCalls, admin.unsetCalls)
	}
}

func TestHandleAdmin_PostRejectsAnOversizedBody(t *testing.T) {
	now := time.Now()
	admin := &stubConfigAdmin{}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)

	form := url.Values{
		"action": {"set"},
		"key":    {settings.KeyLLMModel},
		"value":  {strings.Repeat("x", adminFormMaxBytes+1)},
		"csrf":   {session.AdminCSRF},
	}
	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPost, "/admin", form))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if len(admin.setCalls) != 0 {
		t.Fatalf("an oversized submission reached the store: %v", admin.setCalls)
	}
}

// TestHandleAdmin_RendersTheSettingsTable exercises the template: the values,
// the sources, the escaping of a value that contains markup, and the nav link.
func TestHandleAdmin_RendersTheSettingsTable(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	admin := &stubConfigAdmin{
		entries: []configcmd.Entry{
			{Name: settings.KeyLLMModel, Kind: settings.KindString, Value: "gpt-5", Source: settings.SourceDB},
			{Name: settings.KeyGippityDebugChannel, Kind: settings.KindString, Value: "<script>alert(1)</script>", Source: settings.SourceDefault},
		},
		message: "Set `llm.model=gpt-5`.",
		ok:      true,
	}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	ws.loadTemplates()
	session := adminSession(testOwnerID, time.Minute, now)
	session.Flash = "Removed the runtime override for `llm.model`."

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodGet, "/admin", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{
		settings.KeyLLMModel,
		"gpt-5",
		string(settings.SourceDB),
		"Removed the runtime override",
		`href="/admin"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("a stored value was rendered without escaping")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("the escaped value is missing from the page")
	}

	// The flash is consumed: a second load does not repeat it.
	next := sessionFromResponse(t, ws, w)
	if next.Flash != "" {
		t.Errorf("flash = %q after being rendered, want it cleared", next.Flash)
	}

	w2 := httptest.NewRecorder()
	ws.handleAdmin(w2, formRequest(t, ws, next, http.MethodGet, "/admin", nil))
	if strings.Contains(w2.Body.String(), "Removed the runtime override") {
		t.Error("the flash was shown twice")
	}
}

// TestHandleAdmin_RendersTheInvalidCombinationBanner shows the state a
// config.yaml edit can create: writes are rejected, so the page must say so.
func TestHandleAdmin_RendersTheInvalidCombinationBanner(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	admin := &stubConfigAdmin{entriesErr: errTestInvalidCombination}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	ws.loadTemplates()
	session := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodGet, "/admin", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "INVALID CONFIGURATION") {
		t.Error("the page does not warn about the invalid combination")
	}
}

// TestHandleAdmin_RendersTheUnavailableBanner keeps a half-wired deployment
// honest instead of showing an empty table.
func TestHandleAdmin_RendersTheUnavailableBanner(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	admin := &stubConfigAdmin{entriesErr: configcmd.ErrUnavailable}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	ws.loadTemplates()
	session := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodGet, "/admin", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "unavailable") {
		t.Error("the page does not report the missing settings store")
	}
}

// TestHandleMain_ShowsTheAdminLinkOnlyForTheOwner checks the only place the
// panel is advertised.
func TestHandleMain_ShowsTheAdminLinkOnlyForTheOwner(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()

	render := func(t *testing.T, session *sessionData) string {
		t.Helper()
		ws := newAdminTestServer(&stubConfigAdmin{}, testOwnerID, "", now)
		ws.loadTemplates()

		w := httptest.NewRecorder()
		ws.handleMain(w, formRequest(t, ws, session, http.MethodGet, "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		return w.Body.String()
	}

	t.Run("owner", func(t *testing.T) {
		if body := render(t, adminSession(testOwnerID, time.Minute, now)); !strings.Contains(body, `href="/admin"`) {
			t.Error("the owner does not see the admin link")
		}
	})

	t.Run("another user", func(t *testing.T) {
		if body := render(t, adminSession(testOtherUser, time.Minute, now)); strings.Contains(body, `href="/admin"`) {
			t.Error("a non-owner sees the admin link")
		}
	})

	t.Run("stale owner session", func(t *testing.T) {
		if body := render(t, adminSession(testOwnerID, adminMaxSessionAge+time.Minute, now)); strings.Contains(body, `href="/admin"`) {
			t.Error("a stale owner session still sees the admin link")
		}
	})
}
