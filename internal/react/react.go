// Package react implements /react: the bot reacts to a message with a custom
// emoji picked from an ephemeral select menu, and afterwards removes the
// invoker's own reaction with that emoji on that message.
package react

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

const (
	commandName   = "react"
	optUser       = "user"
	optMessageID  = "message_id"
	optFilter     = "filter"
	sweepInterval = time.Minute
)

var noMentions = &discordgo.MessageAllowedMentions{}

// Module implements bot.Module for /react.
type Module struct {
	router   *bot.Router
	sessions *sessionStore
	watches  *watchStore
	removed  atomic.Int64
}

// New returns a new react Module.
func New() *Module {
	return &Module{sessions: newSessionStore(), watches: newWatchStore(maxWatches)}
}

func (m *Module) Name() string { return "react" }

func (m *Module) Init(d bot.Deps) error {
	m.router = d.Router
	d.Router.AddCommand(command(), m.handleCommand)
	d.Router.AddComponent("react:", m.handleComponent)
	d.Router.OnMessageReactionAdd(m.onReactionAdd)
	slog.Info("react: initialized")
	return nil
}

func command() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        commandName,
		Description: "React to a message with a custom emoji; your own matching reaction gets removed",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionUser, Name: optUser, Description: "Target this user's latest message in this channel"},
			{Type: discordgo.ApplicationCommandOptionString, Name: optMessageID, Description: "Target this message ID in this channel"},
			{Type: discordgo.ApplicationCommandOptionString, Name: optFilter, Description: "Only show emojis whose name contains this", MaxLength: 32},
		},
	}
}

func (m *Module) Background() []bot.BackgroundTask {
	return []bot.BackgroundTask{{Name: "react-session-sweeper", Run: m.sweepLoop}}
}

func (m *Module) Shutdown() error { return nil }

// Stats reports the watch count and removed reactions for /status.
func (m *Module) Stats(_ context.Context) (bot.ModuleStats, error) {
	return bot.ModuleStats{Summary: []bot.Stat{
		{Name: "react_watches", Value: strconv.Itoa(m.watches.len())},
		{Name: "react_removed", Value: strconv.FormatInt(m.removed.Load(), 10)},
	}}, nil
}

func (m *Module) sweepLoop(ctx context.Context) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.sessions.sweep(now)
		}
	}
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, text string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: text, Flags: discordgo.MessageFlagsEphemeral, AllowedMentions: noMentions},
	}); err != nil {
		slog.Error("react: failed to respond to interaction", "error", err)
	}
}

func (m *Module) handleCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand || i.ApplicationCommandData().Name != commandName {
		return
	}
	var userID, messageID, filter string
	for _, o := range i.ApplicationCommandData().Options {
		switch o.Name {
		case optUser:
			if u := o.UserValue(nil); u != nil {
				userID = u.ID
			}
		case optMessageID:
			messageID = strings.TrimSpace(o.StringValue())
		case optFilter:
			filter = strings.TrimSpace(o.StringValue())
		}
	}
	if err := validateOptions(userID, messageID); err != nil {
		respondEphemeral(s, i, err.Error())
		return
	}
	invoker := interactionUserID(i)
	if err := bot.DeferReply(s, i, true); err != nil {
		slog.Error("react: failed to respond to interaction", "error", err)
		return
	}
	if ok := m.router.Go(func(_ context.Context) {
		edit := m.buildPicker(s, i, invoker, userID, messageID, filter)
		bot.EditReply(s, i, edit)
	}); !ok {
		msg := bot.MsgShuttingDown
		bot.EditReply(s, i, &discordgo.WebhookEdit{Content: &msg, AllowedMentions: noMentions})
	}
}

func textEdit(text string) *discordgo.WebhookEdit {
	return &discordgo.WebhookEdit{Content: &text, AllowedMentions: noMentions}
}

func (m *Module) buildPicker(s *discordgo.Session, i *discordgo.InteractionCreate, invoker, userID, messageID, filter string) *discordgo.WebhookEdit {
	target, err := resolveTarget(s, i.ChannelID, userID, messageID)
	if err != nil {
		if errors.Is(err, errNoUserMessage) {
			return textEdit(err.Error())
		}
		slog.Warn("react: resolve target failed", "channel", i.ChannelID, "error", err)
		return textEdit("Could not find that message in this channel.")
	}
	s.State.RLock()
	emojis := collectEmojis(s.State.Guilds, filter)
	s.State.RUnlock()
	if len(emojis) == 0 {
		if filter != "" {
			return textEdit(fmt.Sprintf("No emoji matches `%s`.", filter))
		}
		return textEdit("No custom emojis available.")
	}
	tok, err := m.sessions.put(&pickerSession{
		invokerID: invoker, channelID: i.ChannelID, messageID: target, guildID: i.GuildID,
		filter: filter, emojis: emojis,
	})
	if err != nil {
		slog.Error("react: create picker session failed", "error", err)
		return textEdit("Something went wrong, try again.")
	}
	content := "Pick an emoji:"
	comps := buildPage(tok, emojis, 0)
	return &discordgo.WebhookEdit{Content: &content, Components: &comps, AllowedMentions: noMentions}
}

// parseCustomID splits "react:<action>:<tok>[:<page>]".
func parseCustomID(id string) (action, tok string, page int, ok bool) {
	parts := strings.Split(id, ":")
	if len(parts) < 3 || parts[0] != "react" {
		return "", "", 0, false
	}
	action, tok = parts[1], parts[2]
	switch action {
	case "sel":
		return action, tok, 0, len(parts) == 3
	case "prev", "next":
		if len(parts) != 4 {
			return "", "", 0, false
		}
		p, err := strconv.Atoi(parts[3])
		if err != nil {
			return "", "", 0, false
		}
		return action, tok, p, true
	}
	return "", "", 0, false
}

func updateMessage(s *discordgo.Session, i *discordgo.InteractionCreate, content string, comps []discordgo.MessageComponent) {
	if comps == nil {
		comps = []discordgo.MessageComponent{}
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: content, Components: comps, AllowedMentions: noMentions},
	}); err != nil {
		slog.Error("react: failed to update picker", "error", err)
	}
}

func (m *Module) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	data := i.MessageComponentData()
	action, tok, page, ok := parseCustomID(data.CustomID)
	if !ok {
		return
	}
	sess, ok := m.sessions.get(tok)
	if !ok {
		updateMessage(s, i, "Picker expired, run /react again.", nil)
		return
	}
	if interactionUserID(i) != sess.invokerID {
		respondEphemeral(s, i, "This picker belongs to someone else.")
		return
	}
	if action != "sel" {
		updateMessage(s, i, "Pick an emoji:", buildPage(tok, sess.emojis, page))
		return
	}
	if len(data.Values) != 1 {
		return
	}
	e := sess.emojiByID(data.Values[0])
	if e == nil {
		respondEphemeral(s, i, "Unknown emoji, run /react again.")
		return
	}
	// Ack first: reaction adds are rate-limited and may exceed the 3 s window.
	if err := bot.DeferUpdate(s, i); err != nil {
		slog.Error("react: failed to respond to interaction", "error", err)
		return
	}
	noComps := []discordgo.MessageComponent{}
	if err := s.MessageReactionAdd(sess.channelID, sess.messageID, e.Name+":"+e.ID); err != nil {
		slog.Warn("react: add reaction failed", "channel", sess.channelID, "message", sess.messageID, "emoji", e.ID, "error", err)
		text := "Could not react to that message."
		bot.EditReply(s, i, &discordgo.WebhookEdit{Content: &text, Components: &noComps, AllowedMentions: noMentions})
		return
	}
	m.watches.add(watchKey{messageID: sess.messageID, emojiID: e.ID, userID: sess.invokerID})
	m.sessions.remove(tok)
	text := fmt.Sprintf("Reacted with %s to %s", e.MessageFormat(), messageLink(sess))
	bot.EditReply(s, i, &discordgo.WebhookEdit{Content: &text, Components: &noComps, AllowedMentions: noMentions})
}

func messageLink(p *pickerSession) string {
	guild := p.guildID
	if guild == "" {
		guild = "@me"
	}
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guild, p.channelID, p.messageID)
}

// shouldRemove reports whether r is a watched invoker reaction.
func (m *Module) shouldRemove(botID string, r *discordgo.MessageReaction) bool {
	if r == nil || r.UserID == "" || r.UserID == botID || r.Emoji.ID == "" {
		return false
	}
	return m.watches.has(watchKey{messageID: r.MessageID, emojiID: r.Emoji.ID, userID: r.UserID})
}

func (m *Module) onReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	if r == nil || r.MessageReaction == nil {
		return
	}
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	if !m.shouldRemove(botID, r.MessageReaction) {
		return
	}
	if err := s.MessageReactionRemove(r.ChannelID, r.MessageID, r.Emoji.APIName(), r.UserID); err != nil {
		slog.Warn("react: remove reaction failed", "channel", r.ChannelID, "message", r.MessageID, "user", r.UserID, "error", err)
		return
	}
	m.removed.Add(1)
}
