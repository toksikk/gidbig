package gippity

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/tool"
	"gorm.io/gorm"
)

// newRuntimeSettingsStore wires a real settings store over the module's test
// database, so the accessors can be exercised against stored values.
// newRuntimeSettingsStoreFor reloads the store over an existing database, the way
// a restart would, so a row written outside the command becomes visible.
func newRuntimeSettingsStoreFor(t *testing.T, db *gorm.DB) *settings.Store {
	t.Helper()
	return newRuntimeSettingsStore(t, db)
}

func newRuntimeSettingsStore(t *testing.T, db *gorm.DB) *settings.Store {
	t.Helper()
	base, err := cfg.Decode(strings.NewReader("discord:\n  token: t\ngippity:\n  allowed_guilds: [guild]\n"))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	return store
}

func mustSet(t *testing.T, store *settings.Store, key, value string) {
	t.Helper()
	if err := store.Set(key, value, "owner"); err != nil {
		t.Fatalf("Set(%s=%q): %v", key, value, err)
	}
}

// TestRuntimeSettings_AccessorsFollowTheStore proves every runtime-tunable knob
// reads the store rather than the Init-time snapshot.
func TestRuntimeSettings_AccessorsFollowTheStore(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store

	// The snapshot fields must not be consulted once a store is wired.
	m.userMessageLimit = 1
	m.classifierRateLimit = 1
	m.classifierModel = "snapshot-model"
	m.replyInThread = false
	m.respondToAllMessages = false
	m.debugChannel = "snapshot-channel"
	m.classifierNames = []string{"snapshot"}
	m.toolsEnabled = false
	m.toolMaxIterations = 1
	m.toolAllowed = map[string]bool{"snapshot": true}
	m.toolReasoningEffort = "snapshot"

	mustSet(t, store, settings.KeyGippityRateLimitMessagesPerHour, "7")
	if got := m.userMessageLimitNow(); got != 7 {
		t.Errorf("userMessageLimitNow() = %d, want 7", got)
	}
	mustSet(t, store, settings.KeyGippityClassifierRateLimit, "9")
	if got := m.classifierRateLimitNow(); got != 9 {
		t.Errorf("classifierRateLimitNow() = %d, want 9", got)
	}
	mustSet(t, store, settings.KeyGippityClassifierModel, "stored-model")
	if got := m.classifierModelNow(); got != "stored-model" {
		t.Errorf("classifierModelNow() = %q, want %q", got, "stored-model")
	}
	mustSet(t, store, settings.KeyGippityClassifierNames, "gustaff, gusti")
	names := m.classifierNamesNow()
	if len(names) != 2 || names[0] != "gustaff" || names[1] != "gusti" {
		t.Errorf("classifierNamesNow() = %v, want [gustaff gusti]", names)
	}
	mustSet(t, store, settings.KeyGippityReplyInThread, "true")
	if !m.replyInThreadNow() {
		t.Error("replyInThreadNow() = false, want true")
	}
	mustSet(t, store, settings.KeyGippityRespondToAllMessages, "true")
	if !m.respondToAllMessagesNow() {
		t.Error("respondToAllMessagesNow() = false, want true")
	}
	mustSet(t, store, settings.KeyGippityDebugChannel, "stored-channel")
	if got := m.debugChannelNow(); got != "stored-channel" {
		t.Errorf("debugChannelNow() = %q, want %q", got, "stored-channel")
	}
	mustSet(t, store, settings.KeyGippityToolsEnabled, "true")
	if !m.toolsEnabledNow() {
		t.Error("toolsEnabledNow() = false, want true")
	}
	mustSet(t, store, settings.KeyGippityToolsMaxIterations, "5")
	if got := m.toolMaxIterationsNow(); got != 5 {
		t.Errorf("toolMaxIterationsNow() = %d, want 5", got)
	}
	mustSet(t, store, settings.KeyGippityToolsAllowed, "bot_status")
	allowed := m.toolAllowedNow()
	if len(allowed) != 1 || !allowed["bot_status"] {
		t.Errorf("toolAllowedNow() = %v, want {bot_status}", allowed)
	}
	mustSet(t, store, settings.KeyGippityToolsAllowed, "")
	if got := m.toolAllowedNow(); got != nil {
		t.Errorf("toolAllowedNow() with an empty list = %v, want nil", got)
	}
	mustSet(t, store, settings.KeyGippityToolsReasoningEffort, "low")
	if got := m.toolReasoningEffortNow(); got != "low" {
		t.Errorf("toolReasoningEffortNow() = %q, want %q", got, "low")
	}
}

// TestAccessors_DefaultsSurviveWithoutAnOverride pins a regression trap: a wired
// store with no override must not turn a module default into 0 — a zero limit
// would reject every non-mention classifier call. cfg.Decode applies the same
// defaults, so this also guards against the store and cfg drifting apart.
func TestAccessors_DefaultsSurviveWithoutAnOverride(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.settings = newRuntimeSettingsStore(t, m.db)

	if got := m.classifierRateLimitNow(); got != defaultClassifierRateLimit {
		t.Errorf("classifierRateLimitNow() = %d, want the module default %d", got, defaultClassifierRateLimit)
	}
	if got := m.userMessageLimitNow(); got != defaultRateLimitPerHour {
		t.Errorf("userMessageLimitNow() = %d, want the default %d", got, defaultRateLimitPerHour)
	}
	if got := m.toolMaxIterationsNow(); got != defaultToolMaxIterations {
		t.Errorf("toolMaxIterationsNow() = %d, want the default %d", got, defaultToolMaxIterations)
	}
}

// TestAccessors_FallBackToSnapshotsWithoutAStore keeps the pre-settings
// behaviour for tests and callers that build Deps by hand.
func TestAccessors_FallBackToSnapshotsWithoutAStore(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.settings = nil
	m.toolsEnabled = true
	m.toolMaxIterations = 4
	m.replyInThread = true
	m.userMessageLimit = 11
	m.toolAllowed = map[string]bool{"x": true}
	m.toolReasoningEffort = "high"

	if !m.toolsEnabledNow() {
		t.Error("toolsEnabledNow() should fall back to the snapshot")
	}
	if got := m.toolMaxIterationsNow(); got != 4 {
		t.Errorf("toolMaxIterationsNow() = %d, want 4", got)
	}
	if !m.replyInThreadNow() {
		t.Error("replyInThreadNow() should fall back to the snapshot")
	}
	if got := m.userMessageLimitNow(); got != 11 {
		t.Errorf("userMessageLimitNow() = %d, want 11", got)
	}
	if got := m.toolAllowedNow(); len(got) != 1 || !got["x"] {
		t.Errorf("toolAllowedNow() = %v, want {x}", got)
	}
	if got := m.toolReasoningEffortNow(); got != "high" {
		t.Errorf("toolReasoningEffortNow() = %q, want %q", got, "high")
	}
}

// TestCompleteWithTools_ToolsEnabledIsHot is the headline acceptance path:
// enabling tools at runtime must change the answer path with no restart.
func TestCompleteWithTools_ToolsEnabledIsHot(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store
	m.toolsEnabled = false
	m.tools = tool.NewRegistry()
	m.tools.Register(tool.Def{
		Name:        "echo",
		Description: "test tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Handler:     func(context.Context, tool.Env, json.RawMessage) (string, error) { return "tool-result", nil },
	})

	var offered bool
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		if len(params.Tools) > 0 {
			offered = true
		}
		return completionWithContent("answer"), nil
	}

	if _, err := m.completeWithTools(context.Background(), tool.Env{}, nil); err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if offered {
		t.Fatal("tools were offered while gippity.tools.enabled was false")
	}

	mustSet(t, store, settings.KeyGippityToolsEnabled, "true")

	if _, err := m.completeWithTools(context.Background(), tool.Env{}, nil); err != nil {
		t.Fatalf("completeWithTools after enabling: %v", err)
	}
	if !offered {
		t.Fatal("tools were not offered after enabling the key at runtime")
	}
}

// TestCompleteWithTools_MaxIterationsIsHot checks the iteration cap is read per
// answer, not frozen at Init.
func TestCompleteWithTools_MaxIterationsIsHot(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store
	m.tools = tool.NewRegistry()
	m.tools.Register(tool.Def{
		Name:        "echo",
		Description: "test tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Handler:     func(context.Context, tool.Env, json.RawMessage) (string, error) { return "tool-result", nil },
	})

	// Always ask for a tool, so the loop runs to the cap and then forces a
	// final tool-free answer: calls = iterations + 1.
	calls := 0
	m.chatCompletionFunc = func(_ context.Context, _ openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		calls++
		return completionWithToolCalls(toolCall("1", "echo", "{}")), nil
	}
	mustSet(t, store, settings.KeyGippityToolsEnabled, "true")

	countWith := func(iterations string) int {
		t.Helper()
		mustSet(t, store, settings.KeyGippityToolsMaxIterations, iterations)
		calls = 0
		if _, err := m.completeWithTools(context.Background(), tool.Env{}, nil); err != nil {
			t.Fatalf("completeWithTools: %v", err)
		}
		return calls
	}

	one := countWith("1")
	three := countWith("3")
	if one >= three {
		t.Errorf("calls with 1 iteration = %d, with 3 = %d; the cap is not read per answer", one, three)
	}
}

// TestIsLimitedUser_RateLimitIsHot covers a hot read inside the state mutex.
func TestIsLimitedUser_RateLimitIsHot(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store
	mustSet(t, store, settings.KeyGippityRateLimitMessagesPerHour, "1")

	message := &discordgo.MessageCreate{Message: &discordgo.Message{Author: &discordgo.User{ID: "user"}}}
	if m.isLimitedUser(message) {
		t.Fatal("first message was limited")
	}
	if !m.isLimitedUser(message) {
		t.Fatal("second message was not limited at a runtime limit of 1")
	}
}

// TestSendReply_ReplyInThreadIsHot covers the answer path switch.
func TestSendReply_ReplyInThreadIsHot(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store

	threaded := false
	m.messageThreadStartFunc = func(_ *discordgo.Session, _, _ string, _ *discordgo.ThreadStart) (*discordgo.Channel, error) {
		threaded = true
		return &discordgo.Channel{ID: "thread-1"}, nil
	}

	mc := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "1", ChannelID: "chan", Content: "hi"}}
	mustSet(t, store, settings.KeyGippityReplyInThread, "true")
	if err := m.sendReply(mc, "answer"); err != nil {
		t.Fatalf("sendReply: %v", err)
	}
	if !threaded {
		t.Fatal("reply_in_thread enabled at runtime did not start a thread")
	}
}

// TestOnMessageCreate_DebugChannelIsHot checks the debug-channel restriction
// reads the store, so the owner can point debug logging at a channel live.
func TestOnMessageCreate_DebugChannelIsHot(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store

	// The restriction only applies while debug logging is enabled.
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	mustSet(t, store, settings.KeyGippityDebugChannel, "debug-channel")

	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "1", ChannelID: "other-channel", GuildID: "allowed-guild",
		Content: "hi", Author: &discordgo.User{ID: "user"},
	}}
	m.onMessageCreate(m.session, message)

	var stored int64
	if err := m.db.Model(&ChatMessage{}).Count(&stored).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if stored != 0 {
		t.Errorf("stored %d messages, want 0: the runtime debug channel was ignored", stored)
	}
}

// TestMaxTokensNow_UsesTheClientCap pins that the answer and vision paths share
// one cap with the llm client, so llm.max_tokens governs every completion except
// the classifier.
func TestMaxTokensNow_UsesTheClientCap(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store

	if got := m.maxTokensNow(); got != llm.DefaultMaxTokens {
		t.Errorf("maxTokensNow() = %d, want the client default %d", got, llm.DefaultMaxTokens)
	}

	// Wire the cap exactly as the composition root does.
	client, err := llm.New(llm.Options{
		APIKey:            "test-key",
		MaxTokens:         123,
		MaxTokensOverride: func() (int, bool) { return store.OverrideInt(settings.KeyLLMMaxTokens) },
	})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	m.llmClient = client
	if got := m.maxTokensNow(); got != 123 {
		t.Errorf("maxTokensNow() = %d, want the boot 123", got)
	}

	mustSet(t, store, settings.KeyLLMMaxTokens, "777")
	if got := m.maxTokensNow(); got != 777 {
		t.Errorf("maxTokensNow() = %d, want the runtime 777", got)
	}
}

// TestCompleteWithTools_MaxTokensIsHot proves the runtime cap reaches the request
// that is actually sent, for both the answer and the vision path.
func TestCompleteWithTools_MaxTokensIsHot(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store
	m.llmClient = newMaxTokensClient(t, store, 300)

	var answerCap int64
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		answerCap = params.MaxCompletionTokens.Value
		return completionWithContent("answer"), nil
	}
	var visionCap int64
	m.visionCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		visionCap = params.MaxCompletionTokens.Value
		return completionWithContent("a cat"), nil
	}

	if _, err := m.completeWithTools(context.Background(), tool.Env{}, nil); err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if answerCap != 300 {
		t.Errorf("answer MaxCompletionTokens = %d, want the boot 300", answerCap)
	}

	mustSet(t, store, settings.KeyLLMMaxTokens, "777")

	if _, err := m.completeWithTools(context.Background(), tool.Env{}, nil); err != nil {
		t.Fatalf("completeWithTools after the change: %v", err)
	}
	if answerCap != 777 {
		t.Errorf("answer MaxCompletionTokens = %d, want the runtime 777", answerCap)
	}
	if _, err := m.describeImages([]string{"https://example.test/a.png"}); err != nil {
		t.Fatalf("describeImages: %v", err)
	}
	if visionCap != 777 {
		t.Errorf("vision MaxCompletionTokens = %d, want the runtime 777", visionCap)
	}
}

// newMaxTokensClient builds an llm client whose completion cap is backed by the
// settings store, matching internal/core/cmd.go.
func newMaxTokensClient(t *testing.T, store *settings.Store, boot int) *llm.Client {
	t.Helper()
	client, err := llm.New(llm.Options{
		APIKey:            "test-key",
		MaxTokens:         int64(boot),
		MaxTokensOverride: func() (int, bool) { return store.OverrideInt(settings.KeyLLMMaxTokens) },
	})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	return client
}

// TestUserMessageLimitNow_IgnoresNonPositiveStoredValue mirrors the other
// accessors: a stored 0 must not look like "budget exhausted" for every user.
func TestUserMessageLimitNow_IgnoresNonPositiveStoredValue(t *testing.T) {
	m, _ := setupGippityTest(t)
	store := newRuntimeSettingsStore(t, m.db)
	m.settings = store

	if err := store.Set(settings.KeyGippityRateLimitMessagesPerHour, "5", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := m.userMessageLimitNow(); got != 5 {
		t.Errorf("userMessageLimitNow() = %d, want 5", got)
	}

	// Set rejects 0, so write the row directly, as sqlite3 would.
	if err := m.db.Exec(
		"UPDATE settings SET value = ? WHERE key = ?", "0", settings.KeyGippityRateLimitMessagesPerHour,
	).Error; err != nil {
		t.Fatalf("corrupt row: %v", err)
	}
	reloaded := newRuntimeSettingsStoreFor(t, m.db)
	m.settings = reloaded
	if got := m.userMessageLimitNow(); got != m.userMessageLimit {
		t.Errorf("userMessageLimitNow() = %d, want the Init-time fallback %d", got, m.userMessageLimit)
	}
}

// TestToolMaxIterationsNow_HonoursAStoredZero pins that this key has no floor: a
// stored 0 is legal while tools are off, the loop then makes no rounds, and the
// value reported as in force must be the value used.
func TestToolMaxIterationsNow_HonoursAStoredZero(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.settings = newRuntimeSettingsStore(t, m.db)

	mustSet(t, m.settings, settings.KeyGippityToolsMaxIterations, "4")
	if got := m.toolMaxIterationsNow(); got != 4 {
		t.Errorf("toolMaxIterationsNow() = %d, want 4", got)
	}

	if err := m.db.Exec(
		"UPDATE settings SET value = ? WHERE key = ?", "0", settings.KeyGippityToolsMaxIterations,
	).Error; err != nil {
		t.Fatalf("corrupt row: %v", err)
	}
	m.settings = newRuntimeSettingsStoreFor(t, m.db)
	if got := m.toolMaxIterationsNow(); got != 0 {
		t.Errorf("toolMaxIterationsNow() = %d, want the stored 0 rather than a floored value", got)
	}
}
