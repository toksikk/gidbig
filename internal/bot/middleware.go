package bot

import (
	"log/slog"

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
