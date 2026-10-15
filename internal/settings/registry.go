// Package settings holds the runtime overrides the owner changes through
// Discord. Values are stored in the shared SQLite database as key-value rows and
// layered over config.yaml, so a change applies without a restart for the hot
// keys the registry declares.
package settings

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/toksikk/gidbig/internal/cfg"
)

// Kind is the value type of a setting. It decides how a stored string is
// parsed; every value is stored as a string.
type Kind string

const (
	// KindBool is a true/false setting.
	KindBool Kind = "bool"
	// KindInt is a whole-number setting.
	KindInt Kind = "int"
	// KindString is a free-form setting.
	KindString Kind = "string"
	// KindStringList is a comma-separated list setting.
	KindStringList Kind = "stringlist"
)

// Source names where an effective value came from, lowest precedence first.
type Source string

const (
	// SourceDefault is the built-in default.
	SourceDefault Source = "default"
	// SourceYAML is the value read from config.yaml at boot.
	SourceYAML Source = "yaml"
	// SourceDB is an explicit runtime override, which beats config.yaml.
	SourceDB Source = "db"
)

// Registry key names. They are exported so a module reads its own knob through
// a constant instead of repeating the dotted string, and so this registry stays
// the single definition of what is runtime-editable.
const (
	KeyGippityRateLimitMessagesPerHour = "gippity.rate_limit_messages_per_hour"
	KeyGippityReplyInThread            = "gippity.reply_in_thread"
	KeyGippityRespondToAllMessages     = "gippity.respond_to_all_messages"
	KeyGippityClassifierModel          = "gippity.classifier_model"
	KeyGippityClassifierNames          = "gippity.classifier_names"
	KeyGippityClassifierRateLimit      = "gippity.classifier_rate_limit_per_hour"
	KeyGippityDebugChannel             = "gippity.debug_channel"
	KeyGippityToolsEnabled             = "gippity.tools.enabled"
	KeyGippityToolsMaxIterations       = "gippity.tools.max_iterations"
	KeyGippityToolsAllowed             = "gippity.tools.allowed"
	KeyGippityToolsReasoningEffort     = "gippity.tools.reasoning_effort"
	KeySoundboardQueueMaxDepth         = "soundboard.queue_max_depth"
	KeyLLMModel                        = "llm.model"
	KeyLLMVisionModel                  = "llm.vision_model"
	KeyLLMMaxTokens                    = "llm.max_tokens"
)

// Key describes one runtime-tunable setting.
type Key struct {
	// Name is the dotted key the owner types.
	Name string
	// Kind is the value type used to parse stored strings.
	Kind Kind
	// Hot is true when a change applies to the next read without a restart.
	// Every registered key is hot by design: a key that needs a restart is not
	// offered at all, so the command cannot silently write a dead value.
	Hot bool
	// Secret keys are never listed, read or written. No key is secret today;
	// the flag keeps a future addition from leaking a credential.
	Secret bool
	// Extract reads the deploy-time value (config.yaml plus defaults).
	Extract func(*cfg.Config) string
	// Apply writes the raw value into a config copy so the merged result can be
	// re-validated with cfg.Validate.
	Apply func(*cfg.Config, string) error
	// Validate adds a rule cfg.Validate does not cover for this key. It runs
	// before Apply. Nil means cfg.Validate is the only rule.
	Validate func(string) error
}

// registry is the set of runtime-editable keys.
var registry = []Key{
	{
		Name:    KeyGippityRateLimitMessagesPerHour,
		Kind:    KindInt,
		Hot:     true,
		Extract: extractInt(func(c *cfg.Config) *int { return &c.Gippity.RateLimitMessagesPerHour }),
		Apply:   applyInt(func(c *cfg.Config) *int { return &c.Gippity.RateLimitMessagesPerHour }),
	},
	{
		Name:    KeyGippityReplyInThread,
		Kind:    KindBool,
		Hot:     true,
		Extract: extractBool(func(c *cfg.Config) *bool { return &c.Gippity.ReplyInThread }),
		Apply:   applyBool(func(c *cfg.Config) *bool { return &c.Gippity.ReplyInThread }),
	},
	{
		Name:    KeyGippityRespondToAllMessages,
		Kind:    KindBool,
		Hot:     true,
		Extract: extractBool(func(c *cfg.Config) *bool { return &c.Gippity.RespondToAllMessages }),
		Apply:   applyBool(func(c *cfg.Config) *bool { return &c.Gippity.RespondToAllMessages }),
	},
	{
		Name:    KeyGippityClassifierModel,
		Kind:    KindString,
		Hot:     true,
		Extract: extractString(func(c *cfg.Config) *string { return &c.Gippity.ClassifierModel }),
		Apply:   applyString(func(c *cfg.Config) *string { return &c.Gippity.ClassifierModel }),
	},
	{
		Name:    KeyGippityClassifierNames,
		Kind:    KindStringList,
		Hot:     true,
		Extract: extractList(func(c *cfg.Config) *[]string { return &c.Gippity.ClassifierNames }),
		Apply:   applyList(func(c *cfg.Config) *[]string { return &c.Gippity.ClassifierNames }),
	},
	{
		Name:     KeyGippityClassifierRateLimit,
		Kind:     KindInt,
		Hot:      true,
		Extract:  extractInt(func(c *cfg.Config) *int { return &c.Gippity.ClassifierRateLimitPerHour }),
		Apply:    applyInt(func(c *cfg.Config) *int { return &c.Gippity.ClassifierRateLimitPerHour }),
		Validate: requirePositiveInt,
	},
	{
		Name:    KeyGippityDebugChannel,
		Kind:    KindString,
		Hot:     true,
		Extract: extractString(func(c *cfg.Config) *string { return &c.Gippity.DebugChannel }),
		Apply:   applyString(func(c *cfg.Config) *string { return &c.Gippity.DebugChannel }),
	},
	{
		Name:    KeyGippityToolsEnabled,
		Kind:    KindBool,
		Hot:     true,
		Extract: extractBool(func(c *cfg.Config) *bool { return &c.Gippity.Tools.Enabled }),
		Apply:   applyBool(func(c *cfg.Config) *bool { return &c.Gippity.Tools.Enabled }),
	},
	{
		Name:    KeyGippityToolsMaxIterations,
		Kind:    KindInt,
		Hot:     true,
		Extract: extractInt(func(c *cfg.Config) *int { return &c.Gippity.Tools.MaxIterations }),
		Apply:   applyInt(func(c *cfg.Config) *int { return &c.Gippity.Tools.MaxIterations }),
	},
	{
		Name:    KeyGippityToolsAllowed,
		Kind:    KindStringList,
		Hot:     true,
		Extract: extractList(func(c *cfg.Config) *[]string { return &c.Gippity.Tools.Allowed }),
		Apply:   applyList(func(c *cfg.Config) *[]string { return &c.Gippity.Tools.Allowed }),
	},
	{
		Name:    KeyGippityToolsReasoningEffort,
		Kind:    KindString,
		Hot:     true,
		Extract: extractString(func(c *cfg.Config) *string { return &c.Gippity.Tools.ReasoningEffort }),
		Apply:   applyString(func(c *cfg.Config) *string { return &c.Gippity.Tools.ReasoningEffort }),
	},
	{
		Name:    KeySoundboardQueueMaxDepth,
		Kind:    KindInt,
		Hot:     true,
		Extract: extractInt(func(c *cfg.Config) *int { return &c.Soundboard.QueueMaxDepth }),
		Apply:   applyInt(func(c *cfg.Config) *int { return &c.Soundboard.QueueMaxDepth }),
	},
	{
		Name:     KeyLLMModel,
		Kind:     KindString,
		Hot:      true,
		Extract:  extractString(func(c *cfg.Config) *string { return &c.LLM.Model }),
		Apply:    applyString(func(c *cfg.Config) *string { return &c.LLM.Model }),
		Validate: requireNonEmpty,
	},
	{
		// One cap for every completion the bot makes except the non-mention
		// classifier, which keeps its own much smaller bound so it can only
		// answer YES or NO.
		Name:     KeyLLMMaxTokens,
		Kind:     KindInt,
		Hot:      true,
		Extract:  extractInt(func(c *cfg.Config) *int { return &c.LLM.MaxTokens }),
		Apply:    applyInt(func(c *cfg.Config) *int { return &c.LLM.MaxTokens }),
		Validate: requirePositiveInt,
	},
	{
		// An empty llm.vision_model means "follow llm.model", so unlike llm.model
		// an empty value is meaningful here and is accepted.
		Name:    KeyLLMVisionModel,
		Kind:    KindString,
		Hot:     true,
		Extract: extractString(func(c *cfg.Config) *string { return &c.LLM.VisionModel }),
		Apply:   applyString(func(c *cfg.Config) *string { return &c.LLM.VisionModel }),
	},
}

// byName indexes the registry once at package init.
var byName = func() map[string]Key {
	m := make(map[string]Key, len(registry))
	for _, k := range registry {
		m[k.Name] = k
	}
	return m
}()

// Lookup returns the registry entry for a key name. A secret key is reported as
// unknown, so a caller cannot probe for the existence of a credential.
func Lookup(name string) (Key, bool) {
	k, ok := byName[name]
	if !ok || k.Secret {
		return Key{}, false
	}
	return k, true
}

// Keys returns every registered key in name order, excluding secret keys so a
// listing can never surface one.
func Keys() []Key {
	out := make([]Key, 0, len(registry))
	for _, k := range registry {
		if k.Secret {
			continue
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func extractBool(get func(*cfg.Config) *bool) func(*cfg.Config) string {
	return func(c *cfg.Config) string { return strconv.FormatBool(*get(c)) }
}

func extractInt(get func(*cfg.Config) *int) func(*cfg.Config) string {
	return func(c *cfg.Config) string { return strconv.Itoa(*get(c)) }
}

func extractString(get func(*cfg.Config) *string) func(*cfg.Config) string {
	return func(c *cfg.Config) string { return *get(c) }
}

func extractList(get func(*cfg.Config) *[]string) func(*cfg.Config) string {
	return func(c *cfg.Config) string { return formatList(*get(c)) }
}

func applyBool(get func(*cfg.Config) *bool) func(*cfg.Config, string) error {
	return func(c *cfg.Config, v string) error {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("must be true or false, got %q", v)
		}
		*get(c) = b
		return nil
	}
}

func applyInt(get func(*cfg.Config) *int) func(*cfg.Config, string) error {
	return func(c *cfg.Config, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("must be a whole number, got %q", v)
		}
		*get(c) = n
		return nil
	}
}

func applyString(get func(*cfg.Config) *string) func(*cfg.Config, string) error {
	return func(c *cfg.Config, v string) error {
		*get(c) = v
		return nil
	}
}

func applyList(get func(*cfg.Config) *[]string) func(*cfg.Config, string) error {
	return func(c *cfg.Config, v string) error {
		*get(c) = parseList(v)
		return nil
	}
}

// parseList splits a comma- or newline-separated value into a trimmed list with
// no empty entries. An empty value yields an empty list.
func parseList(v string) []string {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// formatList renders a list in the form parseList accepts.
func formatList(v []string) string { return strings.Join(v, ",") }

// requirePositiveInt rejects zero and negative numbers, and values that are not
// whole numbers.
func requirePositiveInt(raw string) error {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("must be a whole number, got %q", raw)
	}
	if n <= 0 {
		return fmt.Errorf("must be greater than zero, got %d", n)
	}
	return nil
}

// requireNonEmpty rejects an empty value. An empty model would either be
// invalid (openrouter) or silently mean "use the built-in default", so the
// owner is told to use unset instead.
func requireNonEmpty(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("must not be empty; use unset to fall back to config.yaml")
	}
	return nil
}
