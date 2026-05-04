package llm

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
)

// Personality is the shared bot persona appended to every LLM system prompt.
// Centralised here so all plugins express the same character and brevity rules.
const Personality = "You have very dry humor. Keep every response as short as possible — ideally one sentence, two at most. Never use emojis."

var client openai.Client

// generateMessageFn is the underlying completion call, swappable in tests.
var generateMessageFn = func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		Model:     openai.ChatModelGPT4oMini,
		N:         openai.Int(1),
		MaxTokens: openai.Int(150),
	})
	if err != nil {
		return "", err
	}
	return completion.Choices[0].Message.Content, nil
}

// Initialize creates the shared OpenAI client. Must be called before any plugin uses LLM features.
func Initialize() {
	client = openai.NewClient()
	slog.Info("llm: shared OpenAI client initialized")
}

// GetClient returns a pointer to the shared OpenAI client for packages that need direct access (e.g. gippity).
func GetClient() *openai.Client {
	return &client
}

// GenerateMessage sends a single-turn completion and returns the response text.
func GenerateMessage(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	return generateMessageFn(ctx, systemPrompt, userPrompt)
}

// DetectChannelLanguage fetches recent messages from a Discord channel and asks the LLM
// to identify the primary language. Returns "English" on any error or empty channel.
func DetectChannelLanguage(s *discordgo.Session, channelID string) (string, error) {
	msgs, err := s.ChannelMessages(channelID, 20, "", "", "")
	if err != nil || len(msgs) == 0 {
		return "English", err
	}

	var sb strings.Builder
	for _, m := range msgs {
		if m.Author != nil && !m.Author.Bot && m.Content != "" {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
	}

	return detectLanguageFromTexts(strings.TrimSpace(sb.String()))
}

// detectLanguageFromTexts is the testable core of DetectChannelLanguage.
func detectLanguageFromTexts(text string) (string, error) {
	if text == "" {
		return "English", nil
	}

	lang, err := generateMessageFn(
		context.Background(),
		"You detect the primary language of text snippets. Reply with only the full English name of the language (e.g. German, French, English). If unsure, reply with English.",
		text,
	)
	if err != nil {
		slog.Warn("llm: language detection failed, falling back to English", "error", err)
		return "English", nil
	}

	lang = strings.TrimSpace(lang)
	if lang == "" {
		return "English", nil
	}
	return lang, nil
}
