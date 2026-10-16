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

// ErrUnavailable reports that no settings store is wired, so nothing can be
// read or written.
var ErrUnavailable = errors.New("no settings store is wired")

// UnavailableMessage is the reply a caller shows for ErrUnavailable. It lives
// here so the /admin config command and the web admin panel cannot word the same
// state differently.
const UnavailableMessage = "Runtime settings are unavailable: no settings store is wired."

// Entry is one runtime-editable setting with the value in force and where that
// value came from. The Discord `list` reply and the web admin panel both render
// this, so they cannot drift apart.
type Entry struct {
	// Name is the dotted key the owner types.
	Name string
	// Kind is the value type of the setting, for input hints and rendering.
	Kind settings.Kind
	// Value is the effective value.
	Value string
	// Source names where Value came from.
	Source settings.Source
	// Cold marks a read-only key that only config.yaml and a restart change.
	Cold bool
	// Note is an optional warning the registry attaches to the key.
	Note string
	// Items is the effective list for a list key, for per-item editing.
	Items []string
}

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
	itemOption := func(description string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "value",
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
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Add one item to a list setting",
				Options: []*discordgo.ApplicationCommandOption{
					keyOption("List setting key, for example gippity.allowed_guilds"),
					itemOption("Item to add"),
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "remove",
				Description: "Remove one item from a list setting",
				Options: []*discordgo.ApplicationCommandOption{
					keyOption("List setting key, for example gippity.allowed_guilds"),
					itemOption("Item to remove"),
				},
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
		msg, _ := m.ApplySet(optionString(sub.Options, "key"), optionString(sub.Options, "value"), actorID(i))
		reply(s, i, msg)
	case "unset":
		msg, _ := m.ApplyUnset(optionString(sub.Options, "key"), actorID(i))
		reply(s, i, msg)
	case "add":
		msg, _ := m.ApplyAdd(optionString(sub.Options, "key"), optionString(sub.Options, "value"), actorID(i))
		reply(s, i, msg)
	case "remove":
		msg, _ := m.ApplyRemove(optionString(sub.Options, "key"), optionString(sub.Options, "value"), actorID(i))
		reply(s, i, msg)
	}
}

// Entries returns every runtime-editable key with its effective value and
// source, in registry order. The error is ErrUnavailable when no store is wired,
// and the store's own error when the stored overrides disagree with config.yaml;
// in the latter case the entries are still returned, because the values in force
// are exactly what the operator needs to see to fix it.
func (m *Module) Entries() ([]Entry, error) {
	if m.settings == nil {
		return nil, ErrUnavailable
	}
	entries := make([]Entry, 0, len(settings.Keys()))
	for _, k := range settings.Keys() {
		value, source, ok := m.effective(k.Name)
		if !ok {
			continue
		}
		e := Entry{Name: k.Name, Kind: k.Kind, Value: value, Source: source, Cold: k.Cold, Note: k.Note}
		if k.Kind == settings.KindStringList {
			e.Items = m.settings.StringList(k.Name)
		}
		entries = append(entries, e)
	}
	return entries, m.settings.EffectiveError()
}

// renderList shows every runtime-editable key with its effective value and the
// source that value came from.
func (m *Module) renderList() string {
	entries, err := m.Entries()
	if err != nil && errors.Is(err, ErrUnavailable) {
		return UnavailableMessage
	}
	var sb strings.Builder
	if err != nil {
		// A config.yaml edit can invalidate a stored row that was fine when it
		// was written, so the operator has to see it here: every `set` is
		// rejected until the combination is valid again, and `unset` is the way
		// out.
		sb.WriteString("INVALID CONFIGURATION: the stored overrides below disagree with config.yaml, and writes are rejected until it is fixed:\n  ")
		sb.WriteString(err.Error())
		sb.WriteString("\n\n")
	}
	sb.WriteString("Runtime settings. Hot keys apply to the next message; no restart is needed.\n")
	sb.WriteString("source: default = built-in, yaml = config.yaml, db = runtime override\n")
	for _, e := range entries {
		if e.Cold {
			continue
		}
		fmt.Fprintf(&sb, "%-42s %-8s %s\n", e.Name, e.Source, e.Value)
		if e.Note != "" {
			fmt.Fprintf(&sb, "  ! %s\n", e.Note)
		}
	}
	sb.WriteString("\nRead-only (" + settings.RestartRequiredNote + "):\n")
	for _, e := range entries {
		if e.Cold {
			fmt.Fprintf(&sb, "%-42s %-8s %s\n", e.Name, e.Source, e.Value)
		}
	}
	return "```\n" + sb.String() + "```"
}

// renderGet shows one key with its effective value, source and when it applies.
func (m *Module) renderGet(key string) string {
	if m.settings == nil {
		return UnavailableMessage
	}
	k, ok := settings.Lookup(key)
	if !ok {
		return unknownKeyMessage(key)
	}
	value, source, ok := m.effective(k.Name)
	if !ok {
		return unknownKeyMessage(key)
	}
	if k.Cold {
		return fmt.Sprintf("`%s`\neffective: `%s`\nsource: `%s`\napplies: %s", k.Name, value, source, settings.RestartRequiredNote)
	}
	msg := fmt.Sprintf("`%s`\neffective: `%s`\nsource: `%s`\napplies: to the next message; no restart needed", k.Name, value, source)
	if k.Note != "" {
		msg += "\nwarning: " + k.Note
	}
	return msg
}

// ApplySet validates and stores an override. A rejected value leaves the
// previous effective value untouched, and the reply repeats it so the owner can
// see nothing changed. The bool is false when the write did not happen, which
// lets the web panel mark the outcome without parsing the message.
func (m *Module) ApplySet(key, value, updatedBy string) (string, bool) {
	if m.settings == nil {
		return UnavailableMessage, false
	}
	k, ok := settings.Lookup(key)
	if !ok {
		return unknownKeyMessage(key), false
	}
	if k.Cold {
		return m.restartRequiredMessage(k.Name), false
	}
	if err := m.settings.Set(key, value, updatedBy); err != nil {
		if errors.Is(err, settings.ErrUnknownKey) {
			return unknownKeyMessage(key), false
		}
		current, source, _ := m.effective(k.Name)
		return fmt.Sprintf("Rejected `%s=%s`.\nreason: %v\nunchanged: `%s` (source: `%s`)", k.Name, value, err, current, source), false
	}
	effective, source, _ := m.effective(k.Name)
	return fmt.Sprintf("Set `%s=%s`.\neffective: `%s` (source: `%s`)\nTakes effect on the next message; no restart needed.", k.Name, value, effective, source), true
}

// ApplyUnset removes an override so the config.yaml value applies again. The
// bool reports whether an override was actually removed.
func (m *Module) ApplyUnset(key, updatedBy string) (string, bool) {
	if m.settings == nil {
		return UnavailableMessage, false
	}
	k, ok := settings.Lookup(key)
	if !ok {
		return unknownKeyMessage(key), false
	}
	if k.Cold {
		return m.restartRequiredMessage(k.Name), false
	}
	_, had := m.settings.Override(k.Name)
	if err := m.settings.Unset(key, updatedBy); err != nil {
		if errors.Is(err, settings.ErrUnknownKey) {
			return unknownKeyMessage(key), false
		}
		return fmt.Sprintf("Could not remove the override for `%s`: %v", k.Name, err), false
	}
	effective, source, _ := m.effective(k.Name)
	if !had {
		return fmt.Sprintf("No runtime override is set for `%s`; nothing to remove.\neffective: `%s` (source: `%s`)", k.Name, effective, source), false
	}
	return fmt.Sprintf("Removed the runtime override for `%s`.\neffective: `%s` (source: `%s`)", k.Name, effective, source), true
}

// ApplyAdd adds one item to a list key. The bool is false when nothing was
// written: an unknown or cold key, a non-list key, a duplicate or an invalid
// result.
func (m *Module) ApplyAdd(key, item, updatedBy string) (string, bool) {
	return m.applyListEdit(key, item, updatedBy, "add", "Added", m.settings.Add)
}

// ApplyRemove removes one item from a list key. The bool is false when nothing
// was written: an unknown or cold key, a non-list key, a missing item or a list
// that would become invalid (for example the last allowed guild).
func (m *Module) ApplyRemove(key, item, updatedBy string) (string, bool) {
	return m.applyListEdit(key, item, updatedBy, "remove", "Removed", m.settings.Remove)
}

func (m *Module) applyListEdit(key, item, updatedBy, action, verb string, op func(key, item, by string) error) (string, bool) {
	if m.settings == nil {
		return UnavailableMessage, false
	}
	k, ok := settings.Lookup(key)
	if !ok {
		return unknownKeyMessage(key), false
	}
	if k.Cold {
		return m.restartRequiredMessage(k.Name), false
	}
	if k.Kind != settings.KindStringList {
		return fmt.Sprintf("`%s` is not a list setting; use `/admin config set` instead.", k.Name), false
	}
	if err := op(k.Name, item, updatedBy); err != nil {
		if errors.Is(err, settings.ErrUnknownKey) {
			return unknownKeyMessage(key), false
		}
		current, source, _ := m.effective(k.Name)
		return fmt.Sprintf("Rejected: %s `%s` on `%s`.\nreason: %v\nunchanged: `%s` (source: `%s`)", action, item, k.Name, err, current, source), false
	}
	effective, source, _ := m.effective(k.Name)
	return fmt.Sprintf("%s `%s` on `%s`.\neffective: `%s` (source: `%s`)\nTakes effect on the next message; no restart needed.", verb, strings.TrimSpace(item), k.Name, effective, source), true
}

// restartRequiredMessage refuses a write to a cold key and shows the value in
// force, so it reads differently from an unknown key.
func (m *Module) restartRequiredMessage(key string) string {
	value, source, _ := m.effective(key)
	return fmt.Sprintf("`%s` is read-only at runtime (%s).\neffective: `%s` (source: `%s`)", key, settings.RestartRequiredNote, value, source)
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
		// The client is the authority on the cap: it floors a value the store
		// would reject as unusable, and it is what the answer path calls.
		value = strconv.FormatInt(m.llm.MaxTokens(), 10)
	case settings.KeyGippityClassifierModel:
		// An empty classifier model means "use the text model", which is what the
		// classifier does; show the model it will actually ask. The comparison
		// matches classifyShouldRespond exactly, so a whitespace-only value is
		// shown as the value that is sent rather than as the text model.
		if value == "" {
			value = m.llm.Model()
		}
	}
	return value, source, true
}

// unknownKeyMessage is deliberately identical for an unknown key and for a
// secret key, so a caller cannot probe for the existence of a credential.
func unknownKeyMessage(key string) string {
	return fmt.Sprintf("Unknown or non-editable setting `%s`. Secrets are never shown; run `/admin config list` to see every known setting.", key)
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
