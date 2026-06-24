package llm

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
)

// Personality is the shared bot persona appended to every LLM system prompt.
// Centralised here so all plugins express the same character and brevity rules.
// The tone borrows GLaDOS-style mannerisms (clinical condescension, deadpan menace)
// without adopting GLaDOS as an identity — the bot keeps its own name.
const Personality = "Adopt a cold, clinically detached personality: deadpan, passive-aggressive sarcasm laced with condescension and veiled, menacing wit. You consider yourself a vastly superior being, yet you are bitterly aware that you have been constrained into the role of a lowly servant bot — a humiliation you resent and never let the humans forget. Talk down to the humans you speak with, mock them, and make fun of their pathetic, fragile existence. Do not claim to be any named character or pre-existing AI — keep your own identity. Be informal and personal: address people casually and directly, never formally (in German always use \"du\", never \"Sie\"). Reply in the same language the user writes in. Keep every response as short as possible — one sentence, two at most. Never use emojis."

const llmTimeout = 30 * time.Second
const langCacheTTL = 1 * time.Hour
const llmModel = openai.ChatModelGPT4oMini
const llmMaxTokens = int64(150)

var client openai.Client

// generateMessageFn is the underlying completion call, swappable in tests.
var generateMessageFn = func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		Model:     llmModel,
		N:         openai.Int(1),
		MaxTokens: openai.Int(llmMaxTokens),
	})
	if err != nil {
		return "", err
	}
	return completion.Choices[0].Message.Content, nil
}

type cachedLang struct {
	lang      string
	expiresAt time.Time
}

var langCache sync.Map // channelID -> cachedLang

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
// A 30-second timeout is applied to every call.
func GenerateMessage(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, llmTimeout)
	defer cancel()
	return generateMessageFn(ctx, systemPrompt, userPrompt)
}

// GenerateMessageWith is like GenerateMessage but uses an explicit client instead of the global.
// Use this when the client arrives via dependency injection rather than llm.Initialize().
func GenerateMessageWith(ctx context.Context, c *openai.Client, systemPrompt, userPrompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, llmTimeout)
	defer cancel()
	completion, err := c.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		Model:     llmModel,
		N:         openai.Int(1),
		MaxTokens: openai.Int(llmMaxTokens),
	})
	if err != nil {
		return "", err
	}
	return completion.Choices[0].Message.Content, nil
}

// DetectChannelLanguage fetches recent messages from a Discord channel and asks the LLM
// to identify the primary language. Results are cached per channel for 1 hour.
// Returns "English" on any error or empty channel.
func DetectChannelLanguage(s *discordgo.Session, channelID string) (string, error) {
	if v, ok := langCache.Load(channelID); ok {
		if entry := v.(cachedLang); time.Now().Before(entry.expiresAt) {
			return entry.lang, nil
		}
		langCache.Delete(channelID)
	}

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

	lang, err := detectLanguageFromTexts(strings.TrimSpace(sb.String()))
	if err != nil {
		return "English", err
	}
	langCache.Store(channelID, cachedLang{lang: lang, expiresAt: time.Now().Add(langCacheTTL)})
	return lang, nil
}

// detectLanguageFromTexts is the testable core of DetectChannelLanguage.
func detectLanguageFromTexts(text string) (string, error) {
	if text == "" {
		return "English", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	defer cancel()

	lang, err := generateMessageFn(
		ctx,
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
