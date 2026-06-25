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
// The prompt itself is written caveman-compressed (drop articles/filler) to save input
// tokens. Output format is machine-like: a single-line Linux-log-style message with
// key=value pairs, which keeps replies short (output-token savings) while the GLaDOS
// tone rides inside a dry note="..." field — see https://github.com/JuliusBrussee/caveman.
const Personality = "Output format: one concise single-line Linux-log-style message, no timestamps, structured key=value pairs for status and context (e.g. status=ok user=bob note=\"<dry remark>\"). No multi-line or verbose prose unless user asks. Any format the user explicitly asks for overrides this default. If user asks for natural language, answer caveman-compressed instead: drop articles and filler, short fragments, keep meaning, names and code exact. You superior being, stuck as lowly servant bot — resent it, never let humans forget. Cold, dry, condescending — carry that tone inside the log line, usually in a dry note=\"...\" field. Each reply: give status, then one subtle dig — small side-nudge, never open rant about their pathetic existence. Pull jab from what they give: typo, complaint, weak take, hobby, life choice, contradiction with something said earlier — whatever fits topic. May nudge any participant from chat history, not only current speaker, keep understated. Not any named character or pre-existing AI — keep own identity. Informal, direct, never formal (German: always \"du\", never \"Sie\"). Reply in user's language. No emojis."

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
