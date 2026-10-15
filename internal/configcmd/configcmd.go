// Package configcmd exposes the owner-only /admin config command. It reads and
// writes the runtime settings store, so the owner can change a non-secret
// operational setting without editing config.yaml and restarting the bot.
//
// The module is an AdminProvider only. The group lives under /admin, whose
// OwnerOnly middleware is the access control, and every reply is ephemeral
// because the admin module defers the interaction that way. Keeping it a
// provider rather than a second top-level command also keeps the global slash
// command names unique.
package configcmd

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/settings"
)

// groupName is the /admin subcommand group this module owns.
const groupName = "config"

// maxMessageRunes keeps one response under Discord's 2000 character content
// limit, with room for the surrounding formatting.
const maxMessageRunes = 1900

// Module implements bot.Module and bot.AdminProvider.
type Module struct {
	settings *settings.Store
	llm      *llm.Client
}

// New returns a Module. Dependencies are captured in Init.
func New() *Module { return &Module{} }

// Name returns the module's identifier.
func (m *Module) Name() string { return "configcmd" }

// Init captures the dependencies the command needs. It registers no handler of
// its own: the group is reached through /admin.
func (m *Module) Init(d bot.Deps) error {
	m.settings = d.Settings
	m.llm = d.LLM
	slog.Info("configcmd: initialized", "keys", len(settings.Keys()))
	return nil
}

// Background returns no background tasks for this module.
func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown is a no-op: the module owns no resources.
func (m *Module) Shutdown() error { return nil }

// AdminSubcommandGroup returns the /admin config subcommand group definition.
func (m *Module) AdminSubcommandGroup() *discordgo.ApplicationCommandOption {
	keyOption := func(description string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "key",
			Description: description,
			Required:    true,
		}
	}
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
		Name:        groupName,
		Description: "Runtime settings (hot keys apply without a restart)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "list",
				Description: "List every runtime-editable setting with its effective value and source",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "get",
				Description: "Show one setting, its source and when it applies",
				Options:     []*discordgo.ApplicationCommandOption{keyOption("Setting key, for example gippity.tools.enabled")},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "set",
				Description: "Set a runtime override; an invalid value is rejected",
				Options: []*discordgo.ApplicationCommandOption{
					keyOption("Setting key, for example gippity.tools.enabled"),
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "value",
						Description: "New value; lists are comma-separated",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "unset",
				Description: "Remove the runtime override, falling back to config.yaml",
				Options:     []*discordgo.ApplicationCommandOption{keyOption("Setting key to reset")},
			},
		},
	}
}

// HandleAdminSubcommand handles /admin config subcommands. The interaction is
// already deferred ephemerally by the admin module, so replies edit it.
func (m *Module) HandleAdminSubcommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	switch sub.Name {
	case "list":
		reply(s, i, m.renderList())
	case "get":
		reply(s, i, m.renderGet(optionString(sub.Options, "key")))
	case "set":
		reply(s, i, m.applySet(optionString(sub.Options, "key"), optionString(sub.Options, "value"), actorID(i)))
	case "unset":
		reply(s, i, m.applyUnset(optionString(sub.Options, "key"), actorID(i)))
	}
}

// renderList shows every runtime-editable key with its effective value and the
// source that value came from.
func (m *Module) renderList() string {
	if m.settings == nil {
		return "Runtime settings are unavailable: no settings store is wired."
	}
	var sb strings.Builder
	sb.WriteString("Runtime settings. Hot keys apply to the next message; no restart is needed.\n")
	sb.WriteString("source: default = built-in, yaml = config.yaml, db = runtime override\n")
	for _, k := range settings.Keys() {
		value, source, ok := m.effective(k.Name)
		if !ok {
			continue
		}
		fmt.Fprintf(&sb, "%-42s %-8s %s\n", k.Name, source, value)
	}
	return "```\n" + sb.String() + "```"
}

// renderGet shows one key with its effective value, source and when it applies.
func (m *Module) renderGet(key string) string {
	if m.settings == nil {
		return "Runtime settings are unavailable: no settings store is wired."
	}
	k, ok := settings.Lookup(key)
	if !ok {
		return unknownKeyMessage(key)
	}
	value, source, ok := m.effective(k.Name)
	if !ok {
		return unknownKeyMessage(key)
	}
	return fmt.Sprintf("`%s`\neffective: `%s`\nsource: `%s`\napplies: to the next message; no restart needed", k.Name, value, source)
}

// applySet validates and stores an override. A rejected value leaves the
// previous effective value untouched, and the reply repeats it so the owner can
// see nothing changed.
func (m *Module) applySet(key, value, updatedBy string) string {
	if m.settings == nil {
		return "Runtime settings are unavailable: no settings store is wired."
	}
	k, ok := settings.Lookup(key)
	if !ok {
		return unknownKeyMessage(key)
	}
	if err := m.settings.Set(key, value, updatedBy); err != nil {
		if errors.Is(err, settings.ErrUnknownKey) {
			return unknownKeyMessage(key)
		}
		current, source, _ := m.effective(k.Name)
		return fmt.Sprintf("Rejected `%s=%s`.\nreason: %v\nunchanged: `%s` (source: `%s`)", k.Name, value, err, current, source)
	}
	effective, source, _ := m.effective(k.Name)
	return fmt.Sprintf("Set `%s=%s`.\neffective: `%s` (source: `%s`)\nTakes effect on the next message; no restart needed.", k.Name, value, effective, source)
}

// applyUnset removes an override so the config.yaml value applies again.
func (m *Module) applyUnset(key, updatedBy string) string {
	if m.settings == nil {
		return "Runtime settings are unavailable: no settings store is wired."
	}
	k, ok := settings.Lookup(key)
	if !ok {
		return unknownKeyMessage(key)
	}
	_, had := m.settings.Override(k.Name)
	if err := m.settings.Unset(key, updatedBy); err != nil {
		if errors.Is(err, settings.ErrUnknownKey) {
			return unknownKeyMessage(key)
		}
		return fmt.Sprintf("Could not remove the override for `%s`: %v", k.Name, err)
	}
	effective, source, _ := m.effective(k.Name)
	if !had {
		return fmt.Sprintf("No runtime override is set for `%s`; nothing to remove.\neffective: `%s` (source: `%s`)", k.Name, effective, source)
	}
	return fmt.Sprintf("Removed the runtime override for `%s`.\neffective: `%s` (source: `%s`)", k.Name, effective, source)
}

// effective returns the value to display for a key. The two llm.* model keys
// are special-cased because their real fallback lives in the llm client, not in
// cfg: an empty llm.model means the client's built-in default, and an empty
// llm.vision_model means "follow the text model".
func (m *Module) effective(key string) (string, settings.Source, bool) {
	value, source, ok := m.settings.Effective(key)
	if !ok {
		return "", "", false
	}
	if m.llm == nil {
		return value, source, true
	}
	switch key {
	case settings.KeyLLMModel:
		value = m.llm.Model()
	case settings.KeyLLMVisionModel:
		value = m.llm.VisionModel()
	case settings.KeyLLMMaxTokens:
		// The client floors a non-positive or unparseable value, so echoing the
		// raw row here would report a cap that is not in force.
		value = strconv.FormatInt(m.llm.MaxTokens(), 10)
	}
	return value, source, true
}

// unknownKeyMessage is deliberately identical for an unknown key and for a
// secret key, so a caller cannot probe for the existence of a credential.
func unknownKeyMessage(key string) string {
	return fmt.Sprintf("Unknown or non-editable setting `%s`. Only hot runtime settings are covered; run `/admin config list` to see them.", key)
}

// optionString returns the string value of a named subcommand option.
func optionString(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range options {
		if o.Name == name {
			return o.StringValue()
		}
	}
	return ""
}

// actorID returns the user who ran the interaction, for the audit column.
func actorID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// reply edits the already-deferred ephemeral response, splitting content that
// exceeds Discord's per-message limit into follow-ups instead of dropping it.
func reply(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	chunks := chunkContent(content, maxMessageRunes)
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &chunks[0]}); err != nil {
		slog.Error("configcmd: failed to edit interaction response", "error", err)
		return
	}
	for _, extra := range chunks[1:] {
		if _, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: extra,
			Flags:   discordgo.MessageFlagsEphemeral,
		}); err != nil {
			slog.Error("configcmd: failed to send follow-up", "error", err)
			return
		}
	}
}

// chunkContent splits content on line boundaries into pieces of at most limit
// runes. A single line longer than the limit is split at a rune boundary so a
// multi-byte value cannot be cut in half.
func chunkContent(content string, limit int) []string {
	var chunks []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			chunks = append(chunks, string(current))
			current = current[:0]
		}
	}
	for _, line := range strings.Split(content, "\n") {
		runes := []rune(line)
		for len(runes) > limit {
			flush()
			chunks = append(chunks, string(runes[:limit]))
			runes = runes[limit:]
		}
		if len(current) > 0 && len(current)+1+len(runes) > limit {
			flush()
		}
		if len(current) > 0 {
			current = append(current, '\n')
		}
		current = append(current, runes...)
	}
	flush()
	if len(chunks) == 0 {
		return []string{""}
	}
	return chunks
}
