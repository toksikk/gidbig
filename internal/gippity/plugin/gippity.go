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

// StoredFunction is a struct to store a function with a description and parameters for OpenAI completion
type StoredFunction struct {
	Description string
	Parameters  string
	Function    func()
}

var functionRegistry = make(map[string]StoredFunction)

// RegisterFunction registers a function to be called by a completion
func RegisterFunction(name string, description string, parameters string, fn StoredFunction) {
	functionRegistry[name] = StoredFunction{
		Description: description,
		Parameters:  parameters,
		Function:    fn.Function,
	}
}

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

type message struct {
	Username    string `json:"username"`
	UserID      string `json:"user_id"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Timestamp   int64  `json:"timestamp"`
	Message     string `json:"message"`
}

type messageHistory struct {
	Messages []message `json:"messages"`
}

var msgHistory messageHistory

func loadLastMessages() {
	file, err := os.Open(messageHistoryFileName)
	if err != nil {
		msgHistory = messageHistory{Messages: make([]message, 0)}
		slog.Warn("Error while loading last messages", "error", err)
		return
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&msgHistory)
	if err != nil {
		slog.Warn("Error while loading last messages", "error", err)
	}
}

func saveLastMessages() {
	file, err := os.Create(messageHistoryFileName)
	if err != nil {
		slog.Warn("Error while saving last messages", "error", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(msgHistory)
	if err != nil {
		slog.Warn("Error while saving last messages", "error", err)
	}
}

func hoursSince(t time.Time) int {
	return int(time.Since(t).Hours())
}

func formatMessage(msg *discordgo.MessageCreate) (string, error) {
	channel, err := discordSession.Channel(msg.ChannelID)
	channelName := ""
	if err != nil {
		slog.Info("Error while getting channel", "error", err)
		channelName = msg.ChannelID
	} else {
		channelName = channel.Name
	}

	messageStruct := message{
		Username:    msg.Author.Username,
		UserID:      msg.Author.ID,
		ChannelID:   msg.ChannelID,
		ChannelName: channelName,
		Timestamp:   msg.Timestamp.Unix(),
		Message:     msg.Content,
	}

	jsonData, err := json.Marshal(messageStruct)
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}

func addMessage(m *discordgo.MessageCreate) {
	if m.Author.Bot && m.Author.ID != discordSession.State.User.ID {
		return
	}

	if len(msgHistory.Messages) >= maxHistoryMessages {
		msgHistory.Messages = msgHistory.Messages[1:]
	}
	author := m.Author.Username
	if m.Member != nil {
		author = m.Member.Nick
	}
	channel, err := discordSession.Channel(m.ChannelID)
	channelName := ""
	if err != nil {
		slog.Info("Error while getting channel", "error", err)
		channelName = m.ChannelID
	} else {
		channelName = channel.Name
	}

	msgHistory.Messages = append(msgHistory.Messages, message{
		Username:    author,
		UserID:      m.Author.ID,
		ChannelID:   m.ChannelID,
		ChannelName: channelName,
		Timestamp:   m.Timestamp.Unix(),
		Message:     m.Content,
	})
	saveLastMessages()
}

func getMessageHistoryAsJSON(history messageHistory) (string, error) {
	jsonData, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}

// nolint: unused
func getBotDisplayName(m *discordgo.MessageCreate) string {
	botDisplayNames := getBotDisplayNames()
	if botDisplayNames[m.GuildID] == "" {
		return "Gidbig"
	}
	return botDisplayNames[m.GuildID]
}

func getBotDisplayNames() map[string]string {
	guilds := discordSession.State.Guilds
	botUserID := discordSession.State.User.ID
	allBotDisplayNames := make(map[string]string)
	for _, guild := range guilds {
		botGuildMember, err := discordSession.GuildMember(guild.ID, botUserID)
		if err != nil {
			slog.Info("Error while getting bot member", "error", err)
			continue
		}
		allBotDisplayNames[guild.ID] = botGuildMember.Nick
	}
	return allBotDisplayNames
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

// nolint: unused
func generateHistorySummary(history messageHistory) string {
	messageHistoryJSON, err := getMessageHistoryAsJSON(history)
	if err != nil {
		slog.Info("Error while getting message history as JSON", "error", err)
		return ""
	}

	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Du erhältst eine Nachrichten Historie aus einem Discord Textchat im JSON Format. Schreibe eine Zusammenfassung der Nachrichten Historie, aber lasse keine Details dabei aus.")),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage(messageHistoryJSON)),
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
		N:     openai.Int(1),
	})

	if err != nil {
		slog.Info("Error while getting completion", "error", err)
		return ""
	}

	slog.Info("Chat completion", "completion", chatCompletion)

	return chatCompletion.Choices[0].Message.Content
}

// nolint: unused
func generateMessageSummary(message string) string {
	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Du erhältst eine Nachricht aus einem Discord Textchat im JSON Format. Schreibe eine Zusammenfassung der Nachricht, aber lasse keine Details dabei aus.")),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage(message)),
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
		N:     openai.Int(1),
	})

	if err != nil {
		slog.Info("Error while getting completion", "error", err)
		return ""
	}

	slog.Info("Chat completion", "completion", chatCompletion)

	return chatCompletion.Choices[0].Message.Content
}

func generateAnswer(m *discordgo.MessageCreate) (string, error) {
	allBotNames := getBotDisplayNames()
	// write a string for chatCompletion in human language that describes all bot names and their respective guilds
	botNames := ""
	for guildID, botName := range allBotNames {
		botNames += botName + " in " + guildID + ", "
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

	grammarBehavior := "Korrigiere niemals Rechtschreib- oder Grammatikfehler."
	// if rand.Intn(99) < 5 {
	// 	grammarBehavior = "Mache auf Grammatik und Rechtschreibfehler aufmerksam. Sei sehr kritisch und wenig hilfreich."
	// }

	// chatHistorySummary := generateHistorySummary()
	// if chatHistorySummary == "" {
	// 	chatHistorySummary = "Es gab keine vorherigen Nachrichten."
	// }

	// create a copy of msgHistory but without the last message
	msgHistoryCopy := msgHistory
	if len(msgHistoryCopy.Messages) > 0 {
		msgHistoryCopy.Messages = msgHistoryCopy.Messages[:len(msgHistoryCopy.Messages)-1]
	}

	chatHistorySummary, err := getMessageHistoryAsJSON(msgHistoryCopy)
	if err != nil {
		slog.Info("Error while getting message history as JSON", "error", err)
		chatHistorySummary = "Es gab keine vorherigen Nachrichten."
	}

	messageAsJSON, err := formatMessage(m)
	if err != nil {
		slog.Info("Error while formatting message", "error", err)
		return "", err
	}

	// if err != nil {
	// 	slog.Info("Error while formatting message", "error", err)
	// 	return "", err
	// }

	// messageSummary := generateMessageSummary(messageAsJSON)

	// if messageSummary == "" {
	// 	return "", errors.New("Message summary is empty")
	// }

	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Du bist ein Discord Chatbot in einem Channel mit vielen verschiedenen Nutzern, auf mehreren Servern (auch Gilden genannt) und jeweils mit mehreren Textkanälen. Du kannst auf Servern unterschiedliche Namen haben. Deine Namen auf den jeweiligen Servern sind: " + botNames + ".")),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Antworte so kurz wie möglich. Stelle keine Fragen, außer du wirst dazu aufgefordert. Deine Antworten sollen maximal 100 Wörter haben. Vermeide Füllwörter und Interjektionen. Verwende zum bisherigen Gesprächsverlauf passende Eigenschaften der folgenden Liste: " + behaviors)),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage(grammarBehavior)),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage(responseMentioned)),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Dies ist der bisherige Chatverlauf: " + chatHistorySummary)),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage(messageAsJSON)),
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
