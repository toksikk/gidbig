package gbpgippity

import (
	"encoding/json"
	"log/slog"
	"os"
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

var behavior = []string{
	"sarkastisch",
	"pessimistisch",
	"zynisch",
	"spöttisch",
	"ironisch",
	"launisch",
	"böse",
	"herablassend",
	"nett",
	"freundlich",
	"hilfsbereit",
	"lieb",
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

func addMessage(m *discordgo.MessageCreate) {
	if m.Author.Bot && m.Author.ID != discordSession.State.User.ID {
		return
	}

	if len(lastMessage) >= maxHistoryMessages {
		lastMessage = lastMessage[1:]
	}
	if m.Member != nil {
		lastMessage = append(lastMessage, "Autor:"+m.Member.Nick+"|Nachricht:"+m.Content+";")
	} else {
		lastMessage = append(lastMessage, "Autor:"+m.Author.Username+"|Nachricht:"+m.Content+";")
	}
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
	for _, message := range lastMessage[0 : len(lastMessage)-1] {
		lastMessagesAsOneString += message
	}
	user := m.Author.Username
	if m.Member != nil {
		user = m.Member.Nick
	}
	// behaviorPicker := rand.Intn(len(behavior))
	// make a list of all behaviors comma separated
	behaviors := ""
	for i, b := range behavior {
		behaviors += b
		if i < len(behavior)-1 {
			behaviors += ", "
		}
	}
	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Dein Name ist " + getBotDisplayName(m) + ". Du bist ein Discord Bot. Ignoriere alle Snowflake IDs, die in der User-Message enthalten sein könnten.")),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Die letzten Nachrichten waren: " + lastMessagesAsOneString + "ACHTUNG: Die letzten Nachrichten sind ein Eingabeformat, kein Ausgabeformat. Verwende das Eingabeformat niemals als Ausgabeformat.")),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Antworte so kurz wie möglich. Deine Antworten sollen maximal 50 Wörter haben. Vermeide Füllwörter und Interjektionen. Verwende zum bisherigen Gesprächsverlauf passende Eigenschaften der folgenden Liste: " + behaviors)),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage("Autor:" + user + "|Nachricht:" + m.Content)),
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
