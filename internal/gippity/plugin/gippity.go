package gbpgippity

import (
	"encoding/json"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go"
)

// PluginName is the name of the plugin
var PluginName = "gippity"

// OpenAI client
var openaiClient *openai.Client

var discordSession *discordgo.Session

var lastMessage []string

const maxHistoryMessages = 30

var messageCount int = 0
var messageGoal int = 0
var messageGoalRange [2]int = [2]int{10, 20}

var behaviorPool = []string{
	//	"sarkastisch",
	//	"pessimistisch",
	"zynisch",
	"spöttisch",
	//	"ironisch",
	"launisch",
	//	"böse",
	//	"herablassend",
	"nett",
	"freundlich",
	"hilfsbereit",
	"lieb",
	"optimistisch",
	"entspannt",
	"energisch",
	"respektvoll",
	"mürrisch",
	"senil",
	"paranoid",
}

var allowedGuildIDs [2]string = [2]string{"225303764108705793", "125231125961506816"} // TODO: make this a map

var userMessageCount map[string]int

const userMessageLimit = 30

const messageHistoryFileName = "message_history.json"

var userMessageCountLastReset map[string]time.Time

// Start the plugin
func Start(discord *discordgo.Session) {
	slog.Info("Starting plugin.", "plugin", PluginName)

	userMessageCount = make(map[string]int, 0)
	userMessageCountLastReset = make(map[string]time.Time, 0)

	loadLastMessages()

	openaiClient = openai.NewClient() // option.WithAPIKey defaults to os.LookupEnv("OPENAI_API_KEY")

	discordSession = discord

	discord.AddHandler(onMessageCreate)
}

func loadLastMessages() {
	file, err := os.Open(messageHistoryFileName)
	if err != nil {
		lastMessage = make([]string, 0)
		slog.Warn("Error while loading last messages", "error", err)
		return
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&lastMessage)
	if err != nil {
		slog.Warn("Error while loading last messages", "error", err)
	}
}

func saveLastMessages() {
	file, err := os.Create(messageHistoryFileName)
	if err != nil {
		slog.Warn("Error while saving last messages", "error", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(lastMessage)
	if err != nil {
		slog.Warn("Error while saving last messages", "error", err)
	}
}

func hoursSince(t time.Time) int {
	return int(time.Since(t).Hours())
}

func formatMessage(user string, message string) string {
	// return user+" schrieb \""+message+"\";"
	return "Autor: " + user + "\nNachricht: " + message
}

func addMessage(m *discordgo.MessageCreate) {
	if m.Author.Bot && m.Author.ID != discordSession.State.User.ID {
		return
	}

	if len(lastMessage) >= maxHistoryMessages {
		lastMessage = lastMessage[1:]
	}
	author := m.Author.Username
	if m.Member != nil {
		author = m.Member.Nick
	}
	formatted := formatMessage(author, m.Content)

	lastMessage = append(lastMessage, formatted)
	saveLastMessages()
}

func getBotDisplayName(m *discordgo.MessageCreate) string {
	botUserID := discordSession.State.User.ID
	// Get the bot's member information for the specific guild
	botMember, err := discordSession.GuildMember(m.GuildID, botUserID)
	if err != nil {
		slog.Info("Error while getting bot member", "error", err)
		return ""
	}

	// Determine the bot's display name
	botDisplayName := botMember.Nick
	if botDisplayName == "" {
		botDisplayName = discordSession.State.User.Username
	}

	return botDisplayName
}

func isLimitedUser(m *discordgo.MessageCreate) bool {
	if _, exists := userMessageCount[m.Author.ID]; !exists {
		userMessageCountLastReset[m.Author.ID] = time.Now()
		userMessageCount[m.Author.ID] = 0
		return false
	}

	if _, exists := userMessageCountLastReset[m.Author.ID]; !exists {
		userMessageCountLastReset[m.Author.ID] = time.Now()
	}

	if hoursSince(userMessageCountLastReset[m.Author.ID]) >= 1 {
		userMessageCountLastReset[m.Author.ID] = time.Now()
		userMessageCount[m.Author.ID] = 0
		return false
	}

	userMessageCount[m.Author.ID]++

	return userMessageCount[m.Author.ID] >= userMessageLimit
}

func limited(m *discordgo.MessageCreate) bool {
	if m.Author.ID == discordSession.State.User.ID {
		return true
	}

	if isMentioned(m) && !isLimitedUser(m) {
		return false
	}

	if isMentioned(m) && isLimitedUser(m) {
		slog.Info("not answering because of user limitation", "userMessageCount", userMessageCount[m.Author.ID], "userMessageLimit", userMessageLimit, "userMessageCountLastReset", userMessageCountLastReset[m.Author.ID])
		_, err := discordSession.ChannelMessageSend(m.ChannelID, "Du hast heute schon genug Nachrichten geschrieben. Komm wann anders wieder.")
		if err != nil {
			slog.Info("Error while sending message", "error", err)
		}
		return true
	}

	guildAllowed := false
	for _, guild := range allowedGuildIDs {
		if m.GuildID == guild {
			guildAllowed = true
		}
	}

	if !guildAllowed {
		slog.Info("not using ai generated message in this guild")
		return true
	}

	if messageCount >= messageGoal {
		if m.GuildID == "125231125961506816" {
			// they don't want the bot to answer randomly in this guild
			return true
		}
		messageCount = 0
		messageGoal = util.RandomRange(messageGoalRange[0], messageGoalRange[1])
		return false
	}

	messageCount++
	slog.Info("not answering because of message limitation", "messageCount", messageCount, "messageGoal", messageGoal)
	return true
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	addMessage(m)

	if limited(m) {
		return
	}

	generatedAnswer, err := generateAnswer(m)
	if err != nil {
		slog.Error("Could not generate answer")
		return
	}

	_, err = s.ChannelMessageSend(m.ChannelID, generatedAnswer)

	if err != nil {
		slog.Info("Error while sending message", "error", err)
	}
}

func isMentioned(m *discordgo.MessageCreate) bool {
	botUserID := discordSession.State.User.ID

	for _, user := range m.Mentions {
		if user.ID == botUserID {
			return true
		}
	}

	return false
}

func generateAnswer(m *discordgo.MessageCreate) (string, error) {
	lastMessagesAsOneString := ""
	if len(lastMessage) > 1 {
		lastMessagesAsOneString = strings.Join(lastMessage[:len(lastMessage)-1], "\n")
	}
	user := m.Author.Username
	if m.Member != nil {
		user = m.Member.Nick
	}
	// behaviorPicker := rand.Intn(len(behavior))
	// make a list of all behaviors comma separated
	shuffledBehaviors := behaviorPool
	// Shuffle the behaviors
	rand.Shuffle(len(shuffledBehaviors), func(i, j int) {
		shuffledBehaviors[i], shuffledBehaviors[j] = shuffledBehaviors[j], shuffledBehaviors[i]
	})
	// Choose a random subset of behaviors
	subsetSize := rand.Intn(len(shuffledBehaviors)) + 1
	subset := shuffledBehaviors[:subsetSize]
	behaviors := strings.Join(subset, ", ")

	responseMentioned := "Diese Nachricht ist nicht an dich direkt gerichtet, aber du antwortest bitte dennoch darauf, damit das Gespräch weitergeführt wird."
	if isMentioned(m) {
		responseMentioned = "Diese Nachricht ist an dich direkt gerichtet."
	}
	chatHistory := "Es gab keine vorherigen Nachrichten."
	if lastMessagesAsOneString != "" {
		chatHistory = "Die letzten Nachrichten waren:\n" + lastMessagesAsOneString
	}
	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Dein Name ist " + getBotDisplayName(m) + ". Du bist ein Discord Chatbot. Ignoriere alle Snowflake IDs, die in der Benutzer-Nachricht enthalten sein könnten. Der Autor der Nachricht wird dir mitgeteilt. Du erhältst Nachrichten von verschiedenen Benutzern.")),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Antworte so kurz wie möglich. Deine Antworten sollen maximal 50 Wörter haben. Vermeide Füllwörter und Interjektionen. Verwende zum bisherigen Gesprächsverlauf passende Eigenschaften der folgenden Liste: " + behaviors)),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Mache gelegentlich auf Grammatik und Rechtschreibfehler aufmerksam, welche du in den letzten Nachrichten findest, aber nicht die, die du schon moniert hast.")),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage(responseMentioned)),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage(chatHistory)),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage(formatMessage(user, m.Content))),
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
		N:     openai.Int(1),
	})

	if err != nil {
		slog.Info("Error while getting completion", "error", err)
		return "", err
	}

	slog.Info("Chat completion", "completion", chatCompletion)

	return chatCompletion.Choices[0].Message.Content, nil
}
