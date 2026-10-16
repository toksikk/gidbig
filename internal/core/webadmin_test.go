package gidbig

import (
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/configcmd"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/shareddb"
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

	setCalls    []adminCall
	unsetCalls  []adminCall
	addCalls    []adminCall
	removeCalls []adminCall
	message     string
	ok          bool
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

func (s *stubConfigAdmin) ApplyAdd(key, item, actor string) (string, bool) {
	s.addCalls = append(s.addCalls, adminCall{Key: key, Value: item, Actor: actor})
	return s.message, s.ok
}

func (s *stubConfigAdmin) ApplyRemove(key, item, actor string) (string, bool) {
	s.removeCalls = append(s.removeCalls, adminCall{Key: key, Value: item, Actor: actor})
	return s.message, s.ok
}

// newAdminTestServer builds a web server whose clock is fixed, so a test can age
// a session without sleeping.
func newAdminTestServer(admin configAdmin, ownerID, origin string, now time.Time) *webServer {
	return &webServer{
		store:    newSessionStore("test-secret", false),
		config:   admin,
		ownerID:  ownerID,
		uiOrigin: origin,
		now:      func() time.Time { return now },
		tmpls:    map[string]*template.Template{},
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

// csrfFromPage returns the token the rendered forms carry.
func csrfFromPage(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`name="csrf" value="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("the page has no csrf field")
	}
	return m[1]
}

// TestHandleAdmin_MintsAFormTokenForAFreshSession covers the token the forms are
// useless without: a session that has none gets a fresh one, the page carries it,
// the cookie keeps it, and a submission with it is accepted.
func TestHandleAdmin_MintsAFormTokenForAFreshSession(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	admin := &stubConfigAdmin{
		entries: []configcmd.Entry{{Name: settings.KeyLLMModel, Kind: settings.KindString, Value: "gpt-5", Source: settings.SourceDefault}},
		message: "Set `llm.model=x`.",
		ok:      true,
	}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	ws.loadTemplates()
	// A session straight from completeLogin: logged in, no form token yet.
	session := &sessionData{DiscordUserID: testOwnerID, DiscordUsername: testOwnerID, IssuedAt: now.Unix()}

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodGet, "/admin", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	token := csrfFromPage(t, w.Body.String())
	if token == "" {
		t.Fatal("the minted token is empty")
	}

	stored := sessionFromResponse(t, ws, w)
	if stored.AdminCSRF != token {
		t.Errorf("session token = %q, page token = %q, want them equal", stored.AdminCSRF, token)
	}

	// The token the page handed out is the one a submission must carry.
	form := url.Values{
		"action": {"set"},
		"key":    {settings.KeyLLMModel},
		"value":  {"x"},
		"csrf":   {token},
	}
	w2 := httptest.NewRecorder()
	ws.handleAdmin(w2, formRequest(t, ws, stored, http.MethodPost, "/admin", form))

	if w2.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w2.Code, http.StatusSeeOther)
	}
	if len(admin.setCalls) != 1 {
		t.Fatalf("ApplySet calls = %v, want one", admin.setCalls)
	}
}

// TestHandleAdmin_ControlPerKind gives each kind a control that fits it: a
// checkbox for a boolean, a number field for an integer, a text field otherwise.
func TestHandleAdmin_ControlPerKind(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	admin := &stubConfigAdmin{
		entries: []configcmd.Entry{
			{Name: settings.KeyGippityReplyInThread, Kind: settings.KindBool, Value: "true", Source: settings.SourceDefault},
			{Name: settings.KeyGippityToolsMaxIterations, Kind: settings.KindInt, Value: "3", Source: settings.SourceYAML},
			{Name: settings.KeyLLMModel, Kind: settings.KindString, Value: "gpt-5", Source: settings.SourceDB},
		},
	}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	ws.loadTemplates()
	session := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodGet, "/admin", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()

	for _, want := range []string{
		`type="checkbox" name="value" value="true"`,
		`type="number" step="1" name="value" value="3"`,
		`type="text" name="value" value="gpt-5"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	// The ticked box matches the effective value, and the unticked fallback
	// carries the false case.
	if !strings.Contains(body, `value="true" class="admin-check" checked`) {
		t.Error("the checkbox of a true setting is not ticked")
	}
	if !strings.Contains(body, `<input type="hidden" name="value" value="false">`) {
		t.Error("the checkbox has no hidden fallback for the unticked case")
	}
}

// TestHandleAdmin_BoolValueIsTrueWhenTheRowSaysSo covers a stored value that is
// not the literal "true": the checkbox has to reflect it too.
func TestHandleAdmin_BoolValueIsTrueWhenTheRowSaysSo(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	for value, wantChecked := range map[string]bool{"1": true, "TRUE": true, "false": false, "0": false} {
		admin := &stubConfigAdmin{
			entries: []configcmd.Entry{{Name: settings.KeyGippityReplyInThread, Kind: settings.KindBool, Value: value}},
		}
		ws := newAdminTestServer(admin, testOwnerID, "", now)
		ws.loadTemplates()

		w := httptest.NewRecorder()
		ws.handleAdmin(w, formRequest(t, ws, adminSession(testOwnerID, time.Minute, now), http.MethodGet, "/admin", nil))

		if got := strings.Contains(w.Body.String(), `class="admin-check" checked`); got != wantChecked {
			t.Errorf("value %q: checkbox checked = %v, want %v", value, got, wantChecked)
		}
	}
}

// TestHandleAdmin_BoolCheckboxSubmitsTrueOrFalse pins how the two cases arrive:
// a ticked box sends both values and the first one wins, an unticked box sends
// only the hidden fallback. Reordering the two inputs would break the ticked
// case, so it is asserted rather than assumed.
func TestHandleAdmin_BoolCheckboxSubmitsTrueOrFalse(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name  string
		value []string
		want  string
	}{
		{name: "ticked", value: []string{"true", "false"}, want: "true"},
		{name: "unticked", value: []string{"false"}, want: "false"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			admin := &stubConfigAdmin{ok: true}
			ws := newAdminTestServer(admin, testOwnerID, "", now)
			session := adminSession(testOwnerID, time.Minute, now)

			form := url.Values{
				"action": {"set"},
				"key":    {settings.KeyGippityReplyInThread},
				"value":  tc.value,
				"csrf":   {session.AdminCSRF},
			}
			w := httptest.NewRecorder()
			ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPost, "/admin", form))

			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
			}
			if len(admin.setCalls) != 1 {
				t.Fatalf("ApplySet calls = %v, want one", admin.setCalls)
			}
			if got := admin.setCalls[0].Value; got != tc.want {
				t.Errorf("ApplySet value = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleAdmin_PostAppliesAddAndRemove(t *testing.T) {
	now := time.Now()
	admin := &stubConfigAdmin{message: "Added.", ok: true}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)

	for _, action := range []string{"add", "remove"} {
		form := url.Values{"action": {action}, "key": {settings.KeyGippityAllowedGuilds}, "value": {"g2"}, "csrf": {session.AdminCSRF}}
		w := httptest.NewRecorder()
		ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPost, "/admin", form))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s status = %d, want %d", action, w.Code, http.StatusSeeOther)
		}
	}
	want := adminCall{Key: settings.KeyGippityAllowedGuilds, Value: "g2", Actor: testOwnerID}
	if len(admin.addCalls) != 1 || admin.addCalls[0] != want {
		t.Errorf("ApplyAdd calls = %v, want [%v]", admin.addCalls, want)
	}
	if len(admin.removeCalls) != 1 || admin.removeCalls[0] != want {
		t.Errorf("ApplyRemove calls = %v, want [%v]", admin.removeCalls, want)
	}
}

func TestHandleAdmin_AddRemoveKeepTheGuards(t *testing.T) {
	now := time.Now()
	admin := &stubConfigAdmin{ok: true}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	owner := adminSession(testOwnerID, time.Minute, now)
	other := adminSession(testOtherUser, time.Minute, now)

	for _, action := range []string{"add", "remove"} {
		bad := url.Values{"action": {action}, "key": {settings.KeyGippityAllowedGuilds}, "value": {"g2"}, "csrf": {"wrong"}}
		w := httptest.NewRecorder()
		ws.handleAdmin(w, formRequest(t, ws, owner, http.MethodPost, "/admin", bad))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s with a bad token status = %d, want %d", action, w.Code, http.StatusForbidden)
		}
		good := url.Values{"action": {action}, "key": {settings.KeyGippityAllowedGuilds}, "value": {"g2"}, "csrf": {other.AdminCSRF}}
		w = httptest.NewRecorder()
		ws.handleAdmin(w, formRequest(t, ws, other, http.MethodPost, "/admin", good))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s by a non-owner status = %d, want %d", action, w.Code, http.StatusNotFound)
		}
		foreign := formRequest(t, ws, owner, http.MethodPost, "/admin",
			url.Values{"action": {action}, "key": {settings.KeyGippityAllowedGuilds}, "value": {"g2"}, "csrf": {owner.AdminCSRF}})
		foreign.Header.Set("Origin", "https://evil.example")
		w = httptest.NewRecorder()
		ws.handleAdmin(w, foreign)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s from a foreign origin status = %d, want %d", action, w.Code, http.StatusForbidden)
		}
	}
	if len(admin.addCalls)+len(admin.removeCalls) != 0 {
		t.Errorf("a refused submission reached the store: add=%v remove=%v", admin.addCalls, admin.removeCalls)
	}
}

func TestHandleAdmin_RendersColdRowsReadOnlyAndListItems(t *testing.T) {
	t.Chdir("../..")
	now := time.Now()
	admin := &stubConfigAdmin{
		entries: []configcmd.Entry{
			{Name: "web.port", Kind: settings.KindInt, Value: "9090", Source: settings.SourceYAML, Cold: true, Note: settings.RestartRequiredNote},
			{Name: settings.KeyGippityAllowedGuilds, Kind: settings.KindStringList, Value: "g1,g2", Source: settings.SourceYAML, Items: []string{"g1", "g2"}},
		},
	}
	ws := newAdminTestServer(admin, testOwnerID, "", now)
	ws.loadTemplates()
	session := adminSession(testOwnerID, time.Minute, now)

	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodGet, "/admin", nil))
	body := w.Body.String()

	if strings.Contains(body, `name="key" value="web.port"`) {
		t.Error("a cold key has an edit form")
	}
	if !strings.Contains(body, "9090") || !strings.Contains(body, settings.RestartRequiredNote) {
		t.Error("the cold row does not show its value and the restart note")
	}
	if got := strings.Count(body, `name="action" value="remove"`); got != 2 {
		t.Errorf("remove forms = %d, want one per item (2)", got)
	}
	if got := strings.Count(body, `name="action" value="add"`); got != 1 {
		t.Errorf("add forms = %d, want 1", got)
	}
}

// TestHandleAdmin_ColdSetIsRefusedByTheRealModule runs a set on a cold key
// through the real configcmd module, so the web panel shows the same
// restart-required refusal as the slash command.
func TestHandleAdmin_ColdSetIsRefusedByTheRealModule(t *testing.T) {
	db, err := shareddb.Open(filepath.Join(t.TempDir(), shareddb.Filename))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	base, err := cfg.Decode(strings.NewReader("discord:\n  token: t\ngippity:\n  allowed_guilds: [g]\n"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	mod := configcmd.New()
	if err := mod.Init(bot.Deps{Settings: store}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	now := time.Now()
	ws := newAdminTestServer(mod, testOwnerID, "", now)
	session := adminSession(testOwnerID, time.Minute, now)
	form := url.Values{"action": {"set"}, "key": {"web.port"}, "value": {"1"}, "csrf": {session.AdminCSRF}}
	w := httptest.NewRecorder()
	ws.handleAdmin(w, formRequest(t, ws, session, http.MethodPost, "/admin", form))

	after := sessionFromResponse(t, ws, w)
	if after.FlashOK || !strings.Contains(after.Flash, "read-only at runtime") {
		t.Errorf("flash = %q (ok=%v), want a restart-required refusal", after.Flash, after.FlashOK)
	}
	if _, ok := store.Override("web.port"); ok {
		t.Error("a cold key was written")
	}
}
