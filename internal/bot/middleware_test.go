package bot

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// fakeInteraction builds a minimal InteractionCreate for a slash command.
func fakeInteraction(userID, commandName string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User: &discordgo.User{ID: userID},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: commandName,
			},
		},
	}
}

// captureRespond replaces interactionRespondFn for a test and returns the captured responses.
func captureRespond(t *testing.T) *[]*discordgo.InteractionResponse {
	t.Helper()
	orig := interactionRespondFn
	t.Cleanup(func() { interactionRespondFn = orig })

	var captured []*discordgo.InteractionResponse
	interactionRespondFn = func(_ *discordgo.Session, _ *discordgo.Interaction, r *discordgo.InteractionResponse) error {
		captured = append(captured, r)
		return nil
	}
	return &captured
}

func applyChain(h HandlerFunc, mw ...Middleware) HandlerFunc {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// --- OwnerOnly ---

func TestOwnerOnly_OwnerPasses(t *testing.T) {
	captured := captureRespond(t)
	var called atomic.Bool
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	}, OwnerOnly("owner123"))

	h(nil, fakeInteraction("owner123", "cmd"))
	if !called.Load() {
		t.Fatal("handler not called for owner")
	}
	if len(*captured) != 0 {
		t.Fatal("unexpected deny response for owner")
	}
}

func TestOwnerOnly_NonOwnerDenied(t *testing.T) {
	captured := captureRespond(t)
	var called atomic.Bool
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	}, OwnerOnly("owner123"))

	h(nil, fakeInteraction("other456", "cmd"))
	if called.Load() {
		t.Fatal("handler called for non-owner")
	}
	if len(*captured) != 1 {
		t.Fatalf("want 1 deny response, got %d", len(*captured))
	}
}

func TestOwnerOnly_NoMember_UserFieldUsed(t *testing.T) {
	captureRespond(t)
	var called atomic.Bool
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	}, OwnerOnly("owner123"))

	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			User: &discordgo.User{ID: "owner123"},
			Data: discordgo.ApplicationCommandInteractionData{Name: "cmd"},
		},
	}
	h(nil, i)
	if !called.Load() {
		t.Fatal("handler not called when owner set via User field")
	}
}

// --- RateLimit ---

func TestRateLimit_FirstCallPasses(t *testing.T) {
	captureRespond(t)
	var called atomic.Bool
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	}, RateLimit(time.Second))

	h(nil, fakeInteraction("u1", "cmd"))
	if !called.Load() {
		t.Fatal("first call should pass rate limit")
	}
}

func TestRateLimit_SecondCallWithinWindowDenied(t *testing.T) {
	captured := captureRespond(t)
	var count atomic.Int32
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		count.Add(1)
	}, RateLimit(time.Hour))

	h(nil, fakeInteraction("u2", "cmd"))
	h(nil, fakeInteraction("u2", "cmd"))
	if count.Load() != 1 {
		t.Fatalf("want 1 pass, got %d", count.Load())
	}
	if len(*captured) != 1 {
		t.Fatalf("want 1 deny, got %d", len(*captured))
	}
}

func TestRateLimit_DifferentUsersIndependent(t *testing.T) {
	captureRespond(t)
	var count atomic.Int32
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		count.Add(1)
	}, RateLimit(time.Hour))

	h(nil, fakeInteraction("userA", "cmd"))
	h(nil, fakeInteraction("userB", "cmd"))
	if count.Load() != 2 {
		t.Fatalf("different users should have independent buckets, got %d calls", count.Load())
	}
}

// --- Recover ---

func TestRecover_CatchesPanic(t *testing.T) {
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		panic("test panic")
	}, Recover())

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Recover middleware did not absorb panic: %v", r)
		}
	}()
	h(nil, fakeInteraction("u", "cmd"))
}

func TestRecover_NoPanicPassesThrough(t *testing.T) {
	var called atomic.Bool
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	}, Recover())

	h(nil, fakeInteraction("u", "cmd"))
	if !called.Load() {
		t.Fatal("handler not called when no panic")
	}
}

// --- WithCorrelationID ---

func TestWithCorrelationID_CallsNext(t *testing.T) {
	var called atomic.Bool
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	}, WithCorrelationID())

	h(nil, fakeInteraction("u", "cmd"))
	if !called.Load() {
		t.Fatal("handler not called with correlation ID middleware")
	}
}

func TestWithCorrelationID_MonotonicIDs(t *testing.T) {
	mw := WithCorrelationID()
	var called atomic.Int32
	h := applyChain(func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Add(1)
	}, mw)

	for range 5 {
		h(nil, fakeInteraction("u", "cmd"))
	}
	if called.Load() != 5 {
		t.Fatalf("want 5 calls, got %d", called.Load())
	}
}

// --- Middleware chain composition ---

func TestMiddlewareChain_OrderApplied(t *testing.T) {
	captureRespond(t)
	var calls []string
	record := func(name string) Middleware {
		return func(next HandlerFunc) HandlerFunc {
			return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
				calls = append(calls, name+":before")
				next(s, i)
				calls = append(calls, name+":after")
			}
		}
	}

	h := applyChain(
		func(_ *discordgo.Session, _ *discordgo.InteractionCreate) { calls = append(calls, "handler") },
		record("A"), record("B"),
	)
	h(nil, fakeInteraction("u", "cmd"))

	want := []string{"A:before", "B:before", "handler", "B:after", "A:after"}
	if len(calls) != len(want) {
		t.Fatalf("want %v, got %v", want, calls)
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("calls[%d] = %q, want %q", i, calls[i], w)
		}
	}
}
