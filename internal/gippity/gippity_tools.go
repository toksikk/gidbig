package gippity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/tool"
)

const (
	// defaultToolMaxIterations bounds tool-call rounds before the answer path
	// forces a final answer without tools.
	defaultToolMaxIterations = 3
	// toolLoopTimeout bounds the total wall clock across every completion call
	// in one answer, including tool execution. The client applies only a
	// per-request timeout.
	toolLoopTimeout = 60 * time.Second
	// maxToolResultRunes caps each tool result before it is appended to the
	// conversation, so a tool cannot blow up the prompt.
	maxToolResultRunes = 2000
)

// enabledToolDefs returns the registered tools that may be offered to the
// model, honouring the optional allowlist.
func (m *Module) enabledToolDefs() []tool.Def {
	all := m.tools.All()
	if len(m.toolAllowed) == 0 {
		return all
	}
	out := make([]tool.Def, 0, len(all))
	for _, d := range all {
		if m.toolAllowed[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// toolParams maps the internal tool definitions to OpenAI function-tool params.
func toolParams(defs []tool.Def) ([]openai.ChatCompletionToolUnionParam, error) {
	out := make([]openai.ChatCompletionToolUnionParam, 0, len(defs))
	for _, d := range defs {
		parameters := shared.FunctionParameters{}
		if len(d.Parameters) > 0 {
			if err := json.Unmarshal(d.Parameters, &parameters); err != nil {
				return nil, fmt.Errorf("gippity: tool %q has an invalid parameter schema: %w", d.Name, err)
			}
		}
		out = append(out, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        d.Name,
			Description: param.NewOpt(d.Description),
			Parameters:  parameters,
		}))
	}
	return out, nil
}

// completeWithTools runs the bounded tool loop. Without tools enabled, or with
// no tools registered, it performs a single completion call, matching the
// pre-tool behaviour exactly.
func (m *Module) completeWithTools(ctx context.Context, env tool.Env, messages []openai.ChatCompletionMessageParamUnion) (string, error) {
	params := openai.ChatCompletionNewParams{
		Messages:            messages,
		Model:               m.llmClient.Model(),
		N:                   openai.Int(1),
		MaxCompletionTokens: openai.Int(300),
	}

	if !m.toolsEnabled || m.tools == nil {
		return m.singleCompletion(ctx, params)
	}

	defs := m.enabledToolDefs()
	if len(defs) == 0 {
		return m.singleCompletion(ctx, params)
	}

	tools, err := toolParams(defs)
	if err != nil {
		return "", err
	}
	params.Tools = tools
	params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
		OfAuto: param.NewOpt(string(openai.ChatCompletionToolChoiceOptionAutoAuto)),
	}

	ctx, cancel := context.WithTimeout(ctx, toolLoopTimeout)
	defer cancel()

	for i := 0; i < m.toolMaxIterations; i++ {
		completion, err := m.chatCompletionFunc(ctx, params)
		if err != nil {
			return "", err
		}
		if completion == nil || len(completion.Choices) == 0 {
			return "", errors.New("gippity: tool loop got no completion choices")
		}
		message := completion.Choices[0].Message
		if len(message.ToolCalls) == 0 {
			return message.Content, nil
		}

		params.Messages = append(params.Messages, assistantToolCallMessage(message.ToolCalls))
		for _, call := range message.ToolCalls {
			params.Messages = append(params.Messages, openai.ToolMessage(m.executeToolCall(ctx, env, call), call.ID))
		}
	}

	// Iteration cap reached: ask for a final answer without offering tools.
	slog.Info("gippity: tool iteration cap reached, forcing final answer", "iterations", m.toolMaxIterations)
	params.Tools = nil
	params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{}
	completion, err := m.chatCompletionFunc(ctx, params)
	if err != nil {
		return "", err
	}
	return llm.CompletionContent(completion)
}

// singleCompletion performs one completion call, the pre-tool answer path.
func (m *Module) singleCompletion(ctx context.Context, params openai.ChatCompletionNewParams) (string, error) {
	completion, err := m.chatCompletionFunc(ctx, params)
	if err != nil {
		return "", err
	}
	return llm.CompletionContent(completion)
}

// assistantToolCallMessage rebuilds the assistant turn that requested the tool
// calls, so the model sees its own calls before the tool results.
func assistantToolCallMessage(calls []openai.ChatCompletionMessageToolCallUnion) openai.ChatCompletionMessageParamUnion {
	params := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(calls))
	for _, call := range calls {
		params = append(params, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: call.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			},
		})
	}
	return openai.ChatCompletionMessageParamUnion{
		OfAssistant: &openai.ChatCompletionAssistantMessageParam{ToolCalls: params},
	}
}

// executeToolCall runs one tool call and returns the tool message content. An
// unknown tool or a failing handler yields an error string; the answer never
// crashes because of a tool.
func (m *Module) executeToolCall(ctx context.Context, env tool.Env, call openai.ChatCompletionMessageToolCallUnion) string {
	name := call.Function.Name
	def, ok := m.tools.Get(name)
	if !ok {
		slog.Warn("gippity: model called an unknown tool", "tool", name)
		return fmt.Sprintf("error: unknown tool %q", name)
	}

	args := json.RawMessage(call.Function.Arguments)
	if len(args) == 0 {
		args = json.RawMessage("{}")
	} else if !json.Valid(args) {
		return fmt.Sprintf("error: tool %q got invalid JSON arguments", name)
	}

	out, err := def.Handler(ctx, env, args)
	if err != nil {
		slog.Warn("gippity: tool failed", "tool", name, "error", err)
		return fmt.Sprintf("error: tool %q failed", name)
	}
	return truncateToolResult(out)
}

// truncateToolResult caps a tool result so it cannot dominate the prompt.
func truncateToolResult(s string) string {
	r := []rune(s)
	if len(r) <= maxToolResultRunes {
		return s
	}
	return string(r[:maxToolResultRunes]) + "…"
}
