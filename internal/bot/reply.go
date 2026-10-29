package bot

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/bwmarrin/discordgo"
)

// MsgShuttingDown is the reply a deferred interaction receives when its work
// could not start because the bot is shutting down.
const MsgShuttingDown = "Bot is shutting down, try again later."

// DeferReply acknowledges an interaction with a deferred channel message,
// optionally ephemeral. The final reply is sent later with EditReply.
func DeferReply(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool) error {
	resp := &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource}
	if ephemeral {
		resp.Data = &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}
	}
	if err := s.InteractionRespond(i.Interaction, resp); err != nil {
		return fmt.Errorf("bot: defer reply: %w", err)
	}
	return nil
}

// DeferUpdate acknowledges a component interaction with a deferred message
// update; the message is edited later with EditReply.
func DeferUpdate(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	}); err != nil {
		return fmt.Errorf("bot: defer update: %w", err)
	}
	return nil
}

// EditReply edits the deferred response of an interaction and logs a failure.
// Callers keep control of the edit payload, including AllowedMentions.
func EditReply(s *discordgo.Session, i *discordgo.InteractionCreate, edit *discordgo.WebhookEdit) {
	if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		slog.Error("bot: edit interaction response", "error", fmt.Errorf("bot: edit reply: %w", err))
	}
}

// Go runs fn on a goroutine tracked by the router. fn receives a context that
// is cancelled when the bot shuts down, and Bot.Shutdown waits for it before
// shutting down modules. Go returns false without running fn once the router
// has stopped accepting work; the caller should then reply MsgShuttingDown.
// On a nil Router fn runs inline with context.Background().
func (r *Router) Go(fn func(ctx context.Context)) bool {
	if fn == nil {
		return false
	}
	if r == nil {
		fn(context.Background())
		return true
	}
	r.mu.Lock()
	if r.asyncClosed {
		r.mu.Unlock()
		return false
	}
	r.asyncWG.Add(1)
	ctx := r.asyncCtx
	r.mu.Unlock()

	go func() {
		defer r.asyncWG.Done()
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("bot/router: async work panicked", "panic", rec, "stack", string(debug.Stack()))
			}
		}()
		fn(ctx)
	}()
	return true
}

// stopAsync stops accepting async work, cancels running work and waits for it.
func (r *Router) stopAsync() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.asyncClosed = true
	cancel := r.asyncCancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.asyncWG.Wait()
}
