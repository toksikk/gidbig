package gippity

import (
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/settings"
)

// The module's own fallbacks for the two per-user hourly limits. cfg.Decode
// applies both defaults, so these only cover a hand-built Config that skipped
// it; a zero limit would reject every message.
const (
	defaultRateLimitPerHour    = 30
	defaultClassifierRateLimit = 30
)

// Runtime-tunable knobs. Each accessor reads the runtime settings store on every
// use, so an /admin config change applies to the next message without a restart.
// When no store is wired (tests that build Deps by hand) the Init-time snapshot
// field is returned, which keeps the existing field-level test setup working
// unchanged.

// maxTokensNow returns the cap for one completion in the answer and vision
// paths. The llm client owns the value — llm.max_tokens plus any runtime
// override — so every completion in the process shares one cap. The non-mention
// classifier deliberately keeps its own much smaller bound instead.
func (m *Module) maxTokensNow() int64 {
	if m.llmClient != nil {
		return m.llmClient.MaxTokens()
	}
	return llm.DefaultMaxTokens
}

// userMessageLimitNow keeps the Init-time fallback for a non-positive value, like
// its siblings: a stored 0 would otherwise make every mention look over budget.
func (m *Module) userMessageLimitNow() int {
	if m.settings != nil {
		if n := m.settings.Int(settings.KeyGippityRateLimitMessagesPerHour); n > 0 {
			return n
		}
	}
	return m.userMessageLimit
}

// classifierRateLimitNow keeps the Init-time fallback for a non-positive value.
// cfg.Decode applies the 30/h default, but a hand-built Config may still leave
// the key at zero, and a zero limit would reject every non-mention classifier
// call.
func (m *Module) classifierRateLimitNow() int {
	if m.settings != nil {
		if n := m.settings.Int(settings.KeyGippityClassifierRateLimit); n > 0 {
			return n
		}
	}
	return m.classifierRateLimit
}

func (m *Module) classifierModelNow() string {
	if m.settings != nil {
		return m.settings.String(settings.KeyGippityClassifierModel)
	}
	return m.classifierModel
}

func (m *Module) classifierNamesNow() []string {
	if m.settings != nil {
		return m.settings.StringList(settings.KeyGippityClassifierNames)
	}
	return m.classifierNames
}

func (m *Module) replyInThreadNow() bool {
	if m.settings != nil {
		return m.settings.Bool(settings.KeyGippityReplyInThread)
	}
	return m.replyInThread
}

func (m *Module) respondToAllMessagesNow() bool {
	if m.settings != nil {
		return m.settings.Bool(settings.KeyGippityRespondToAllMessages)
	}
	return m.respondToAllMessages
}

func (m *Module) debugChannelNow() string {
	if m.settings != nil {
		return m.settings.String(settings.KeyGippityDebugChannel)
	}
	return m.debugChannel
}

func (m *Module) toolsEnabledNow() bool {
	if m.settings != nil {
		return m.settings.Bool(settings.KeyGippityToolsEnabled)
	}
	return m.toolsEnabled
}

// toolMaxIterationsNow returns the effective tool-round cap. A non-positive value
// is honoured rather than floored: cfg.Validate forbids it while tools are
// enabled, and with tools off the loop makes no rounds and forces a final answer,
// so the value reported as in force is the value used.
func (m *Module) toolMaxIterationsNow() int {
	if m.settings != nil {
		return m.settings.Int(settings.KeyGippityToolsMaxIterations)
	}
	return m.toolMaxIterations
}

// toolAllowedNow returns the allowlist as a set, or nil when it is empty, which
// means every registered tool is offered.
func (m *Module) toolAllowedNow() map[string]bool {
	if m.settings == nil {
		return m.toolAllowed
	}
	names := m.settings.StringList(settings.KeyGippityToolsAllowed)
	if len(names) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	return allowed
}

func (m *Module) toolReasoningEffortNow() string {
	if m.settings != nil {
		return m.settings.String(settings.KeyGippityToolsReasoningEffort)
	}
	return m.toolReasoningEffort
}

// guildAllowed reports whether gippity may answer in guildID. It reads
// gippity.allowed_guilds from the store on every message, so an /admin config
// add or remove applies to the next message; without a store the Init-time
// snapshot is used.
func (m *Module) guildAllowed(guildID string) bool {
	if m.settings == nil {
		return m.allowedGuildIDs[guildID]
	}
	for _, id := range m.settings.StringList(settings.KeyGippityAllowedGuilds) {
		if id == guildID {
			return true
		}
	}
	return false
}

// userIgnored reports whether gippity must ignore messages from userID. It reads
// gippity.ignored_users from the store on every message, so an /admin config
// add or remove applies to the next message; without a store the Init-time
// snapshot is used.
func (m *Module) userIgnored(userID string) bool {
	if m.settings == nil {
		return m.ignoredUserIDs[userID]
	}
	for _, id := range m.settings.StringList(settings.KeyGippityIgnoredUsers) {
		if id == userID {
			return true
		}
	}
	return false
}
