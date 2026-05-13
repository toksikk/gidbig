package bot

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
)

// HandlerFunc is the type for slash-command interaction handlers.
type HandlerFunc func(*discordgo.Session, *discordgo.InteractionCreate)

// Middleware wraps a HandlerFunc to add cross-cutting behaviour.
type Middleware func(HandlerFunc) HandlerFunc

// interactionRespondFn is swappable in tests.
var interactionRespondFn = func(s *discordgo.Session, i *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	return s.InteractionRespond(i, r)
}

func denyEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = interactionRespondFn(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// OwnerOnly rejects interactions from non-owner users with an ephemeral denial.
func OwnerOnly(ownerID string) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			if interactionUserID(i) != ownerID {
				denyEphemeral(s, i, "Access denied.")
				return
			}
			next(s, i)
		}
	}
}

// RateLimit rejects interactions that arrive faster than d per user per command.
func RateLimit(d time.Duration) Middleware {
	type bucket struct {
		mu   sync.Mutex
		last time.Time
	}
	var mu sync.Mutex
	buckets := make(map[string]*bucket)

	return func(next HandlerFunc) HandlerFunc {
		return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			if i.Type != discordgo.InteractionApplicationCommand {
				next(s, i)
				return
			}
			key := interactionUserID(i) + ":" + i.ApplicationCommandData().Name

			mu.Lock()
			b, ok := buckets[key]
			if !ok {
				b = &bucket{}
				buckets[key] = b
			}
			mu.Unlock()

			b.mu.Lock()
			defer b.mu.Unlock()
			if time.Since(b.last) < d {
				denyEphemeral(s, i, "Slow down.")
				return
			}
			b.last = time.Now()
			next(s, i)
		}
	}
}

// Recover catches panics in a handler and logs them without crashing the process.
func Recover() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("bot/middleware: recovered from panic", "panic", r)
				}
			}()
			next(s, i)
		}
	}
}

// WithCorrelationID tags each interaction with a monotonic ID and logs it.
func WithCorrelationID() Middleware {
	var counter atomic.Uint64
	return func(next HandlerFunc) HandlerFunc {
		return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			id := counter.Add(1)
			if i.Type == discordgo.InteractionApplicationCommand {
				slog.Debug("bot/middleware: interaction", "correlation_id", id, "command", i.ApplicationCommandData().Name)
			} else {
				slog.Debug("bot/middleware: interaction", "correlation_id", id)
			}
			next(s, i)
		}
	}
}
