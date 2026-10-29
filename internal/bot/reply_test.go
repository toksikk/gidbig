package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type capturedRequest struct {
	method string
	path   string
	body   []byte
}

// replySession routes discordgo REST calls to a local server. It swaps the
// process-global endpoint variables, so tests using it must not run in parallel.
func replySession(t *testing.T, status int) (*discordgo.Session, <-chan capturedRequest) {
	t.Helper()
	requests := make(chan capturedRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- capturedRequest{method: r.Method, path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	origAPI, origWebhooks := discordgo.EndpointAPI, discordgo.EndpointWebhooks
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointWebhooks = server.URL + "/webhooks/"
	t.Cleanup(func() {
		discordgo.EndpointAPI = origAPI
		discordgo.EndpointWebhooks = origWebhooks
	})
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	s.MaxRestRetries = 0
	return s, requests
}

func testInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{ID: "1", AppID: "2", Token: "tok"}}
}

func decodeResponse(t *testing.T, body []byte) discordgo.InteractionResponse {
	t.Helper()
	var resp discordgo.InteractionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestDeferReplyEphemeralFlag(t *testing.T) {
	for _, ephemeral := range []bool{false, true} {
		s, reqs := replySession(t, http.StatusOK)
		if err := DeferReply(s, testInteraction(), ephemeral); err != nil {
			t.Fatalf("DeferReply: %v", err)
		}
		resp := decodeResponse(t, (<-reqs).body)
		if resp.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
			t.Fatalf("type = %v", resp.Type)
		}
		gotEphemeral := resp.Data != nil && resp.Data.Flags&discordgo.MessageFlagsEphemeral != 0
		if gotEphemeral != ephemeral {
			t.Fatalf("ephemeral = %v, want %v", gotEphemeral, ephemeral)
		}
	}
}

func TestDeferUpdateType(t *testing.T) {
	s, reqs := replySession(t, http.StatusOK)
	if err := DeferUpdate(s, testInteraction()); err != nil {
		t.Fatalf("DeferUpdate: %v", err)
	}
	if resp := decodeResponse(t, (<-reqs).body); resp.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("type = %v", resp.Type)
	}
}

func TestDeferReplyWrapsError(t *testing.T) {
	s, _ := replySession(t, http.StatusBadRequest)
	err := DeferReply(s, testInteraction(), false)
	if err == nil || !strings.Contains(err.Error(), "bot: defer reply") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditReplySendsEdit(t *testing.T) {
	s, reqs := replySession(t, http.StatusOK)
	content := "hi"
	EditReply(s, testInteraction(), &discordgo.WebhookEdit{Content: &content})
	req := <-reqs
	if req.method != http.MethodPatch || !strings.Contains(string(req.body), `"hi"`) {
		t.Fatalf("unexpected request %s %s", req.method, req.body)
	}
}

func TestEditReplyErrorDoesNotPanic(t *testing.T) {
	s, reqs := replySession(t, http.StatusBadRequest)
	content := "hi"
	EditReply(s, testInteraction(), &discordgo.WebhookEdit{Content: &content})
	<-reqs
}

func TestRouterGoRunsWithLiveCtx(t *testing.T) {
	r := newRouter(Deps{})
	done := make(chan error, 1)
	if !r.Go(func(ctx context.Context) { done <- ctx.Err() }) {
		t.Fatal("Go returned false")
	}
	if err := <-done; err != nil {
		t.Fatalf("ctx already done: %v", err)
	}
	r.stopAsync()
}

func TestRouterGoAfterStopReturnsFalse(t *testing.T) {
	r := newRouter(Deps{})
	r.stopAsync()
	ran := false
	if r.Go(func(context.Context) { ran = true }) {
		t.Fatal("Go returned true after stop")
	}
	if ran {
		t.Fatal("fn ran after stop")
	}
}

func TestRouterStopAsyncCancelsAndWaits(t *testing.T) {
	r := newRouter(Deps{})
	started := make(chan struct{})
	var mu sync.Mutex
	exited := false
	r.Go(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		exited = true
		mu.Unlock()
	})
	<-started
	r.stopAsync()
	mu.Lock()
	defer mu.Unlock()
	if !exited {
		t.Fatal("stopAsync returned before work exited")
	}
}

func TestRouterGoRecoversPanic(t *testing.T) {
	r := newRouter(Deps{})
	r.Go(func(context.Context) { panic("boom") })
	r.stopAsync()
}

func TestRouterGoZeroValueRouter(t *testing.T) {
	r := &Router{}
	done := make(chan error, 1)
	if !r.Go(func(ctx context.Context) { done <- ctx.Err() }) {
		t.Fatal("Go returned false")
	}
	if err := <-done; err != nil {
		t.Fatalf("ctx = %v", err)
	}
	r.stopAsync()
}

func TestRouterGoNilReceiverRunsInline(t *testing.T) {
	var r *Router
	ran := false
	if !r.Go(func(ctx context.Context) { ran = ctx.Err() == nil }) {
		t.Fatal("nil router Go returned false")
	}
	if !ran {
		t.Fatal("fn did not run inline")
	}
}

type orderModule struct {
	mu    *sync.Mutex
	order *[]string
}

func (orderModule) Name() string                 { return "order" }
func (orderModule) Init(Deps) error              { return nil }
func (orderModule) Background() []BackgroundTask { return nil }
func (m orderModule) Shutdown() error {
	m.mu.Lock()
	*m.order = append(*m.order, "module")
	m.mu.Unlock()
	return nil
}

func TestBotShutdownDrainsAsyncBeforeModuleShutdown(t *testing.T) {
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	b := New(Deps{Session: s})
	var mu sync.Mutex
	var order []string
	if err := b.RegisterModule(orderModule{mu: &mu, order: &order}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	b.Router.Go(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		order = append(order, "async")
		mu.Unlock()
	})
	<-started
	b.Shutdown()
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "async,module" {
		t.Fatalf("order = %v", order)
	}
}
