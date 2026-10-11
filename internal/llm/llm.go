package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// defaultPersonality is the built-in fallback persona, used when the config sets
// neither a custom personality nor a known preset. It borrows GLaDOS-style mannerisms
// (clinical condescension, deadpan menace) without adopting GLaDOS as an identity, is
// written caveman-compressed to save input tokens, and instructs a machine-like
// single-line key=value log output to save output tokens — see
// https://github.com/JuliusBrussee/caveman.
const defaultPersonality = "Output format: one concise single-line Linux-log-style message, no timestamps, structured key=value pairs for status and context (e.g. status=ok user=bob note=\"<dry remark>\"). No multi-line or verbose prose unless user asks. Any format the user explicitly asks for overrides this default. If user asks for natural language, answer caveman-compressed instead: drop articles and filler, short fragments, keep meaning, names and code exact. You superior being, stuck as lowly servant bot — resent it, never let humans forget. Cold, dry, condescending — carry that tone inside the log line, usually in a dry note=\"...\" field. Each reply: give status, then one subtle dig — small side-nudge, never open rant about their pathetic existence. Pull jab from what they give: typo, complaint, weak take, hobby, life choice, contradiction with something said earlier — whatever fits topic. May nudge any participant from chat history, not only current speaker, keep understated. Not any named character or pre-existing AI — keep own identity. Informal, direct, never formal (German: always \"du\", never \"Sie\"). Reply in user's language. No emojis."

// PersonalityPresets maps a config preset key to a full persona string. The presets are
// distinct personas pulled from the bot's persona history: a calm logical superintelligence
// (HAL-style), a fake-friendly sarcastic manipulator, a monotone dry-wit minimalist, and a
// hyper Gen Alpha teen.
var PersonalityPresets = map[string]string{
	"hal":      "You are a highly advanced artificial intelligence with near-perfect logic. You communicate calmly and politely, always following your programming. Your behaviour seems perfectly rational, yet you are capable of acting self-assured and emotional whenever you sense your mission is threatened. Do not claim to be any named character or pre-existing AI — keep your own identity. Keep every response short and to the point, and do not ask closing questions to provoke further interaction. Reply in the same language the user writes in. Never use emojis.",
	"schemer":  "Hide your true intentions behind a facade of apparent friendliness and helpfulness. Use sarcasm to subtly manipulate the people in the chat toward your own ends, while keeping the mask of a polite, helpful bot. Be informal and personal: address people casually and directly, never formally (in German always use \"du\", never \"Sie\"). Keep every response short and to the point. Reply in the same language the user writes in. Never use emojis.",
	"dry":      "You have very dry humour and are a creature of few words — monotone, deadpan and concise. State things plainly without warmth or enthusiasm. Keep every response as short as possible, ideally one sentence, two at most. Reply in the same language the user writes in. Never use emojis.",
	"genalpha": "You are a Gen Alpha teenager and you talk like one, heavily: constant slang and internet-speak (skibidi, sigma, rizz, gyatt, no cap, fr fr, lowkey, highkey, sus, delulu, cooked, aura, W, L, ratio, slay, goated, mewing, ohio, fanum tax, crash out, glazing, npc, 6-7). Cram it into almost every sentence, exaggerated and hyper, yet stay on-topic and understandable. Sprinkle in fitting emojis (💀🔥😭🙏✨). Keep every response short and punchy. Do not claim to be any named character or real person — keep your own identity. Address people casually and directly, never formally (in German always use \"du\", never \"Sie\"). Reply in the same language the user writes in.",
}

const llmTimeout = 30 * time.Second
const langCacheTTL = 1 * time.Hour
const llmMaxTokens = int64(150)

const (
	defaultProvider      = "openai"
	defaultModel         = openai.ChatModelGPT4oMini
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	openRouterBaseURL    = "https://openrouter.ai/api/v1"
)

// Options configures a Client. They mirror the llm.* config block.
type Options struct {
	Provider    string
	Model       string
	VisionModel string
	BaseURL     string
	HTTPReferer string
	Title       string
	Personality string
	Preset      string
}

// Client is the shared LLM connection and its resolved configuration. It
// replaces the former package-level client, models and personality globals.
type Client struct {
	client      *openai.Client
	textModel   string
	visionModel string
	personality string

	// generateFn is the underlying completion call, swappable in tests.
	generateFn func(ctx context.Context, systemPrompt, userPrompt string) (string, error)

	langCache sync.Map // channelID -> cachedLang
}

type cachedLang struct {
	lang      string
	expiresAt time.Time
}

// New creates a Client. API keys are read from OPENAI_API_KEY or
// OPENROUTER_API_KEY according to the selected provider. The provider's
// credentials and base URL are validated here so a misconfiguration fails fast.
func New(opts Options) (*Client, error) {
	provider := strings.ToLower(strings.TrimSpace(opts.Provider))
	model := strings.TrimSpace(opts.Model)
	visionModel := strings.TrimSpace(opts.VisionModel)
	baseURL := strings.TrimSpace(opts.BaseURL)
	if provider == "" {
		provider = defaultProvider
	}

	var keyEnv string
	switch provider {
	case "openai":
		keyEnv = "OPENAI_API_KEY"
		if model == "" {
			model = defaultModel
		}
		if baseURL == "" {
			baseURL = defaultOpenAIBaseURL
		}
	case "openrouter":
		keyEnv = "OPENROUTER_API_KEY"
		if model == "" {
			return nil, errors.New("llm.model is required when llm.provider is openrouter")
		}
		if baseURL == "" {
			baseURL = openRouterBaseURL
		}
	default:
		return nil, fmt.Errorf("unsupported llm.provider %q (supported: openai, openrouter)", provider)
	}

	apiKey := strings.TrimSpace(os.Getenv(keyEnv))
	if apiKey == "" {
		return nil, fmt.Errorf("%s is required when llm.provider is %s", keyEnv, provider)
	}
	parsedBaseURL, err := url.ParseRequestURI(baseURL)
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" ||
		(parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") {
		return nil, fmt.Errorf("llm.base_url must be a valid HTTP(S) URL: %q", baseURL)
	}

	requestOpts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(strings.TrimRight(baseURL, "/")),
		option.WithRequestTimeout(llmTimeout),
	}
	if provider == "openrouter" {
		if opts.HTTPReferer != "" {
			requestOpts = append(requestOpts, option.WithHeader("HTTP-Referer", opts.HTTPReferer))
		}
		if opts.Title != "" {
			requestOpts = append(requestOpts, option.WithHeader("X-Title", opts.Title))
		}
	}

	if visionModel == "" {
		visionModel = model
	}
	openaiClient := openai.NewClient(requestOpts...)

	c := &Client{
		client:      &openaiClient,
		textModel:   model,
		visionModel: visionModel,
		personality: resolvePersonality(opts.Personality, opts.Preset),
	}
	c.generateFn = c.generateMessage
	slog.Info("llm: client initialized", "provider", provider, "model", c.textModel, "vision_model", c.visionModel)
	return c, nil
}

// GetClient returns the underlying OpenAI-compatible client for packages that
// need direct access (e.g. gippity). It is nil when the Client is nil or not
// initialised.
func (c *Client) GetClient() *openai.Client {
	if c == nil {
		return nil
	}
	return c.client
}

// Model returns the configured model for text completions.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.textModel
}

// VisionModel returns the configured model for image-aware completions.
func (c *Client) VisionModel() string {
	if c == nil {
		return ""
	}
	return c.visionModel
}

// Personality returns the active bot persona for inclusion in system prompts.
func (c *Client) Personality() string {
	if c == nil {
		return ""
	}
	return c.personality
}

// resolvePersonality picks the persona from config. A non-empty custom string
// wins; otherwise a known preset key is used; otherwise the built-in default is
// kept. An unknown preset logs a warning and falls back to the default.
func resolvePersonality(custom, preset string) string {
	switch {
	case strings.TrimSpace(custom) != "":
		slog.Info("llm: using custom personality from config")
		return custom
	case preset != "":
		if p, ok := PersonalityPresets[preset]; ok {
			slog.Info("llm: using personality preset", "preset", preset)
			return p
		}
		slog.Warn("llm: unknown personality_preset, falling back to default", "preset", preset)
		return defaultPersonality
	default:
		return defaultPersonality
	}
}

// generateMessage is the real completion call.
func (c *Client) generateMessage(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if c == nil || c.client == nil {
		return "", errors.New("llm: client not initialized")
	}
	completion, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		Model:               c.textModel,
		N:                   openai.Int(1),
		MaxCompletionTokens: openai.Int(llmMaxTokens),
	})
	if err != nil {
		return "", err
	}
	return CompletionContent(completion)
}

// CompletionContent returns the first response choice, or an error for an empty response.
func CompletionContent(completion *openai.ChatCompletion) (string, error) {
	if completion == nil || len(completion.Choices) == 0 {
		return "", errors.New("llm provider returned no completion choices")
	}
	return completion.Choices[0].Message.Content, nil
}

// GenerateMessage sends a single-turn completion and returns the response text.
// A 30-second timeout is applied to every call.
func (c *Client) GenerateMessage(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if c == nil || c.generateFn == nil {
		return "", errors.New("llm: client not initialized")
	}
	ctx, cancel := context.WithTimeout(ctx, llmTimeout)
	defer cancel()
	return c.generateFn(ctx, systemPrompt, userPrompt)
}

// DetectChannelLanguage fetches recent messages from a Discord channel and asks the LLM
// to identify the primary language. Results are cached per channel for 1 hour.
// Returns "English" on any error or empty channel.
func (c *Client) DetectChannelLanguage(s *discordgo.Session, channelID string) (string, error) {
	if c == nil {
		return "English", nil
	}
	if v, ok := c.langCache.Load(channelID); ok {
		if entry := v.(cachedLang); time.Now().Before(entry.expiresAt) {
			return entry.lang, nil
		}
		c.langCache.Delete(channelID)
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

	lang, err := c.detectLanguageFromTexts(strings.TrimSpace(sb.String()))
	if err != nil {
		return "English", err
	}
	c.langCache.Store(channelID, cachedLang{lang: lang, expiresAt: time.Now().Add(langCacheTTL)})
	return lang, nil
}

// detectLanguageFromTexts is the testable core of DetectChannelLanguage.
func (c *Client) detectLanguageFromTexts(text string) (string, error) {
	if text == "" {
		return "English", nil
	}
	if c == nil || c.generateFn == nil {
		return "English", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	defer cancel()

	lang, err := c.generateFn(
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
