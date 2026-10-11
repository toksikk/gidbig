package gippity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/toksikk/gidbig/internal/tool"
)

// toolCall builds a function tool call as it would arrive from the provider.
func toolCall(id, name, arguments string) openai.ChatCompletionMessageToolCallUnion {
	return openai.ChatCompletionMessageToolCallUnion{
		ID:   id,
		Type: "function",
		Function: openai.ChatCompletionMessageFunctionToolCallFunction{
			Name:      name,
			Arguments: arguments,
		},
	}
}

func completionWithToolCalls(calls ...openai.ChatCompletionMessageToolCallUnion) *openai.ChatCompletion {
	return &openai.ChatCompletion{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{ToolCalls: calls}}},
	}
}

func completionWithContent(content string) *openai.ChatCompletion {
	return &openai.ChatCompletion{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: content}}},
	}
}

func TestCompleteWithTools_DisabledSingleCall(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.toolsEnabled = false

	calls := 0
	var seenTools int
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		calls++
		seenTools = len(params.Tools)
		return completionWithContent("plain answer"), nil
	}

	got, err := m.completeWithTools(context.Background(), tool.Env{}, []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")})
	if err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if got != "plain answer" {
		t.Errorf("answer = %q, want %q", got, "plain answer")
	}
	if calls != 1 {
		t.Errorf("completion calls = %d, want 1", calls)
	}
	if seenTools != 0 {
		t.Errorf("tools sent = %d, want 0", seenTools)
	}
}

func TestCompleteWithTools_RunsToolThenAnswers(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.toolsEnabled = true
	m.tools = tool.NewRegistry()

	var handledArgs string
	m.tools.Register(tool.Def{
		Name:        "lookup",
		Description: "test lookup",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		Handler: func(_ context.Context, _ tool.Env, args json.RawMessage) (string, error) {
			handledArgs = string(args)
			return "tool result", nil
		},
	})

	var captured []openai.ChatCompletionNewParams
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		captured = append(captured, params)
		if len(captured) == 1 {
			return completionWithToolCalls(toolCall("call-1", "lookup", `{"q":"x"}`)), nil
		}
		return completionWithContent("final answer"), nil
	}

	got, err := m.completeWithTools(context.Background(), tool.Env{UserID: "u1"}, []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")})
	if err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if got != "final answer" {
		t.Errorf("answer = %q, want %q", got, "final answer")
	}
	if len(captured) != 2 {
		t.Fatalf("completion calls = %d, want 2", len(captured))
	}
	if handledArgs != `{"q":"x"}` {
		t.Errorf("handler args = %q", handledArgs)
	}
	if len(captured[0].Tools) != 1 {
		t.Errorf("first call tools = %d, want 1", len(captured[0].Tools))
	}
	// The second call must contain the assistant tool-call turn and the tool result.
	var sawToolMessage bool
	for _, msg := range captured[1].Messages {
		if msg.OfTool != nil {
			sawToolMessage = true
			if msg.OfTool.Content.OfString.Value != "tool result" {
				t.Errorf("tool message content = %q", msg.OfTool.Content.OfString.Value)
			}
			if msg.OfTool.ToolCallID != "call-1" {
				t.Errorf("tool message call id = %q", msg.OfTool.ToolCallID)
			}
		}
	}
	if !sawToolMessage {
		t.Error("second call did not include a tool message")
	}
}

func TestCompleteWithTools_CapsIterationsAndForcesFinalAnswer(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.toolsEnabled = true
	m.toolMaxIterations = 2
	m.tools = tool.NewRegistry()
	m.tools.Register(tool.Def{Name: "loop", Handler: func(context.Context, tool.Env, json.RawMessage) (string, error) {
		return "again", nil
	}})

	calls := 0
	lastTools := -1
	// The forced final call is the only one that returns content; the fake
	// keeps requesting the tool until the cap forces a tool-free call.
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		calls++
		lastTools = len(params.Tools)
		if calls > m.toolMaxIterations {
			return completionWithContent("forced"), nil
		}
		return completionWithToolCalls(toolCall("call", "loop", "{}")), nil
	}

	got, err := m.completeWithTools(context.Background(), tool.Env{}, []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")})
	if err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if got != "forced" {
		t.Errorf("answer = %q, want %q", got, "forced")
	}
	if calls != m.toolMaxIterations+1 {
		t.Errorf("completion calls = %d, want max_iterations+1 = %d", calls, m.toolMaxIterations+1)
	}
	if lastTools != 0 {
		t.Errorf("forced final call tools = %d, want 0", lastTools)
	}
}

func TestCompleteWithTools_UnknownToolYieldsErrorString(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.toolsEnabled = true
	m.tools = tool.NewRegistry()
	// A registered tool keeps the loop enabled; the model then calls a name
	// that was never registered.
	m.tools.Register(tool.Def{Name: "known", Handler: func(context.Context, tool.Env, json.RawMessage) (string, error) { return "ok", nil }})

	calls := 0
	var toolContent string
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		calls++
		if calls == 1 {
			return completionWithToolCalls(toolCall("call-1", "does_not_exist", "{}")), nil
		}
		for _, msg := range params.Messages {
			if msg.OfTool != nil {
				toolContent = msg.OfTool.Content.OfString.Value
			}
		}
		return completionWithContent("recovered"), nil
	}

	got, err := m.completeWithTools(context.Background(), tool.Env{}, []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")})
	if err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if got != "recovered" {
		t.Errorf("answer = %q, want %q", got, "recovered")
	}
	if !strings.Contains(toolContent, "unknown tool") {
		t.Errorf("tool content = %q, want unknown tool error", toolContent)
	}
}

func TestCompleteWithTools_AllowlistFiltersTools(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.toolsEnabled = true
	m.tools = tool.NewRegistry()
	m.tools.Register(tool.Def{Name: "allowed", Handler: func(context.Context, tool.Env, json.RawMessage) (string, error) { return "", nil }})
	m.tools.Register(tool.Def{Name: "blocked", Handler: func(context.Context, tool.Env, json.RawMessage) (string, error) { return "", nil }})
	m.toolAllowed = map[string]bool{"allowed": true}

	var toolsSent int
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		toolsSent = len(params.Tools)
		return completionWithContent("ok"), nil
	}

	if _, err := m.completeWithTools(context.Background(), tool.Env{}, nil); err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if toolsSent != 1 {
		t.Errorf("tools sent = %d, want 1", toolsSent)
	}
}

func TestCompleteWithTools_ReasoningEffortOnToolRoundsOnly(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.toolsEnabled = true
	m.toolMaxIterations = 1
	m.tools = tool.NewRegistry()
	m.tools.Register(tool.Def{Name: "loop", Handler: func(context.Context, tool.Env, json.RawMessage) (string, error) {
		return "again", nil
	}})
	m.toolReasoningEffort = "none"

	var efforts []string
	calls := 0
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		calls++
		efforts = append(efforts, string(params.ReasoningEffort))
		if calls == 1 {
			return completionWithToolCalls(toolCall("call", "loop", "{}")), nil
		}
		return completionWithContent("done"), nil
	}

	if _, err := m.completeWithTools(context.Background(), tool.Env{}, nil); err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if len(efforts) != 2 {
		t.Fatalf("completion calls = %d, want 2", len(efforts))
	}
	if efforts[0] != "none" {
		t.Errorf("tool round reasoning_effort = %q, want %q", efforts[0], "none")
	}
	if efforts[1] != "" {
		t.Errorf("forced final reasoning_effort = %q, want empty", efforts[1])
	}
}

func TestToolParams_MapsSchema(t *testing.T) {
	params, err := toolParams([]tool.Def{{
		Name:        "demo",
		Description: "demo tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		Handler:     func(context.Context, tool.Env, json.RawMessage) (string, error) { return "", nil },
	}})
	if err != nil {
		t.Fatalf("toolParams: %v", err)
	}
	if len(params) != 1 {
		t.Fatalf("params len = %d, want 1", len(params))
	}
	fn := params[0].GetFunction()
	if fn == nil || fn.Name != "demo" {
		t.Fatalf("function = %+v", fn)
	}
	if fn.Parameters["type"] != "object" {
		t.Errorf("schema type = %v, want object", fn.Parameters["type"])
	}
}

func TestToolParams_InvalidSchemaReturnsError(t *testing.T) {
	_, err := toolParams([]tool.Def{{
		Name:       "bad",
		Parameters: json.RawMessage(`not json`),
		Handler:    func(context.Context, tool.Env, json.RawMessage) (string, error) { return "", nil },
	}})
	if err == nil {
		t.Fatal("expected error for invalid schema")
	}
}

func TestTruncateToolResult(t *testing.T) {
	long := strings.Repeat("a", maxToolResultRunes+50)
	got := truncateToolResult(long)
	if runes := []rune(got); len(runes) != maxToolResultRunes+1 {
		t.Errorf("truncated len = %d, want %d", len(runes), maxToolResultRunes+1)
	}
	if short := truncateToolResult("short"); short != "short" {
		t.Errorf("short = %q", short)
	}
}

func TestSearchChatHistoryHandler_PrivacyParity(t *testing.T) {
	m, _ := setupGippityTest(t)

	// Opt this user out of privacy: their message stays visible.
	if err := m.setUserPrivacy("u-visible", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	insertHistory(t, m, "channel-1", "allowed-guild", "u-visible", "1000000000000000001", "secret plans here", false)
	// Default privacy on: this user's message must be anonymized.
	insertHistory(t, m, "channel-1", "allowed-guild", "u-hidden", "1000000000000000002", "secret plans there", false)

	out, err := m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1", GuildID: "allowed-guild", UserID: "caller"}, json.RawMessage(`{"query":"secret plans"}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if !strings.Contains(out, "secret plans here") {
		t.Errorf("visible message missing from output: %q", out)
	}
	if strings.Contains(out, "secret plans there") {
		t.Errorf("private message leaked into output: %q", out)
	}
	if !strings.Contains(out, "[Anonymisierte Nachricht]") {
		t.Errorf("anonymization marker missing: %q", out)
	}
}

func TestSearchChatHistoryHandler_ScopeClampedToEnv(t *testing.T) {
	m, _ := setupGippityTest(t)
	if err := m.setUserPrivacy("u1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	insertHistory(t, m, "channel-1", "allowed-guild", "u1", "1000000000000000003", "find me", false)
	insertHistory(t, m, "channel-2", "allowed-guild", "u1", "1000000000000000004", "find me", false)

	// Default scope is the caller's channel; the other channel must not match.
	out, err := m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"query":"find me"}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if n := strings.Count(out, "find me"); n != 1 {
		t.Errorf("channel scope matches = %d, want 1: %q", n, out)
	}

	// Guild scope without a guild in Env yields no scope.
	out, err = m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"query":"find me","scope":"guild"}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if out != "no scope available" {
		t.Errorf("guild scope without guild = %q, want %q", out, "no scope available")
	}
}

func TestSearchChatHistoryHandler_LimitAndEmpty(t *testing.T) {
	m, _ := setupGippityTest(t)
	if err := m.setUserPrivacy("u1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	for i := 0; i < 5; i++ {
		insertHistory(t, m, "channel-1", "allowed-guild", "u1", "100000000000000001"+string(rune('0'+i)), "repeat", false)
	}

	out, err := m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"query":"repeat","limit":2}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if n := strings.Count(out, "repeat"); n != 2 {
		t.Errorf("matches = %d, want 2: %q", n, out)
	}

	empty, err := m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"query":"nothing-matches"}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if empty != "no matching messages" {
		t.Errorf("empty = %q, want %q", empty, "no matching messages")
	}

	blank, err := m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"query":"  "}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if blank != "no query given" {
		t.Errorf("blank = %q, want %q", blank, "no query given")
	}
}

func TestEscapeLikePattern(t *testing.T) {
	got := escapeLikePattern(`100%_a\b`)
	want := `100\%\_a\\b`
	if got != want {
		t.Errorf("escapeLikePattern = %q, want %q", got, want)
	}
}

func TestSearchChatHistoryHandler_DateRange(t *testing.T) {
	m, _ := setupGippityTest(t)
	if err := m.setUserPrivacy("u1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	insertHistoryAt(t, m, "channel-1", "allowed-guild", "u1", "1000000000000000101", "day one", time.Date(2026, time.July, 8, 9, 0, 0, 0, time.Local))
	insertHistoryAt(t, m, "channel-1", "allowed-guild", "u1", "1000000000000000102", "day one late", time.Date(2026, time.July, 8, 23, 59, 59, 0, time.Local))
	insertHistoryAt(t, m, "channel-1", "allowed-guild", "u1", "1000000000000000103", "day two", time.Date(2026, time.July, 9, 0, 0, 1, 0, time.Local))

	// A single day returns only that day's stored messages, newest first.
	out, err := m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"from":"2026-07-08","to":"2026-07-08"}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if !strings.Contains(out, "day one late") || !strings.Contains(out, "day one") {
		t.Errorf("date range missing day one messages: %q", out)
	}
	if strings.Contains(out, "day two") {
		t.Errorf("date range leaked the next day: %q", out)
	}
	if strings.Index(out, "day one late") > strings.Index(out, "day one") {
		t.Errorf("results are not newest first: %q", out)
	}

	// A query and a date range combine.
	out, err = m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"query":"day two","from":"2026-07-08","to":"2026-07-08"}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if out != "no matching messages" {
		t.Errorf("query+range outside the day = %q, want %q", out, "no matching messages")
	}

	// Dates are inclusive on both ends, so a range reaching July 9 finds it.
	out, err = m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"from":"2026-07-08","to":"2026-07-09"}`))
	if err != nil {
		t.Fatalf("searchChatHistoryHandler: %v", err)
	}
	if !strings.Contains(out, "day two") {
		t.Errorf("range to July 9 missing day two: %q", out)
	}
}

func TestSearchChatHistoryHandler_RejectsBadDate(t *testing.T) {
	m, _ := setupGippityTest(t)

	_, err := m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"from":"08.07.2026"}`))
	if err == nil {
		t.Fatal("expected error for a non-ISO date")
	}
	if !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("error = %v", err)
	}

	_, err = m.searchChatHistoryHandler(context.Background(), tool.Env{ChannelID: "channel-1"}, json.RawMessage(`{"from":"2026-07-09","to":"2026-07-08"}`))
	if err == nil {
		t.Fatal("expected error when from is after to")
	}
}

// insertHistory stores one message row for search tests.
func insertHistory(t *testing.T, m *Module, channelID, guildID, userID, messageID, content string, mention bool) {
	t.Helper()
	msg := ChatMessage{
		UserID:       userID,
		ChannelID:    channelID,
		GuildID:      guildID,
		MessageID:    messageID,
		Message:      content,
		Timestamp:    int64(len(messageID)),
		IsBotMention: mention,
	}
	if err := m.db.Create(&msg).Error; err != nil {
		t.Fatalf("insert history: %v", err)
	}
}

// insertHistoryAt stores one message row at a fixed timestamp, for date-filter
// tests that need real dates.
func insertHistoryAt(t *testing.T, m *Module, channelID, guildID, userID, messageID, content string, at time.Time) {
	t.Helper()
	msg := ChatMessage{
		UserID:    userID,
		ChannelID: channelID,
		GuildID:   guildID,
		MessageID: messageID,
		Message:   content,
		Timestamp: at.Unix(),
	}
	if err := m.db.Create(&msg).Error; err != nil {
		t.Fatalf("insert history: %v", err)
	}
}
