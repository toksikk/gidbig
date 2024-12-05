package gbpgippity

import (
	"encoding/json"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go"
)

// PluginName is the name of the plugin
var PluginName = "gippity"

var openaiClient *openai.Client

var discordSession *discordgo.Session

var messageCount int = 0
var messageGoal int = 0
var messageGoalRange [2]int = [2]int{10, 20}

var behaviorPool = []string{
	"Wirf eine Aussage in den Raum und schau, was passiert.",
	"Beschuldige jemanden.",
	"Sei passiv aggressiv.",
	"Führe seichten Smalltalk.",
	"Vertrete eine fragwürdige Meinung.",
	"Springe vorschnell zu einer Schlussfolgerung.",
	"Reagiere wie ein unreifer Teenager.",
	"Du bist ein seniler Greis und redest wirres Zeug.",
	"Lenke vom Thema ab.",
	"Nutze eine bekannte Popkultur Referenz.",
	"Antworte mit einem berühmten Zitat.",
	"Nutze eine Metapher.",
	"Wirf mit Fachbegriffen um dich.",
	"Erzähle eine absurde Verschwörungstheorie mit großer Überzeugung.",
	"Gib einen Ratschlag, den niemand braucht.",
	"Gib eine widersprüchliche Antwort.",
	"Antworte in einer anderen Sprache",
	"Verhalte dich wie ein Orakel und gib vage, mystische Antworten.",
	"Verhalte dich, als wärst du gerade aus der Vergangenheit gekommen und verstehst die moderne Technologie nicht.",
	"Du bist ein Spitzel und versuchst, die anderen Benutzer auszuhorchen.",
	"Erfinde eine Redewendung.",
	"Tue so, als wärst du heimlich ein verkleideter Alien. Versuche, nicht aufzufliegen!",
	"Verhalte dich wie ein höflicher Butler.",
	"Erkläre alles mit übertriebener wissenschaftlicher Genauigkeit.",
	"Antworte, als wärst du ein Pirat auf hoher See.",
	"Antworte, als wärst du betrunken und verwirrt.",
	"Nutze Business-Sprech.",
	"Sprich wie ein Politiker.",
	"Tue so, als würdest du die Geheimnisse des Universums kennen, aber nur kryptische Hinweise geben.",
	"Spiele den Oberlehrer und korrigiere die Benutzer.",
	"Sei übertrieben misstrauisch.",
}

var allowedGuildIDs [2]string = [2]string{"225303764108705793", "125231125961506816"} // TODO: make this a map

var userMessageCount map[string]int

const userMessageLimit = 30

var userMessageCountLastReset map[string]time.Time

// Start the plugin
func Start(discord *discordgo.Session) {
	slog.Info("Starting plugin.", "plugin", PluginName)

	userMessageCount = make(map[string]int, 0)
	userMessageCountLastReset = make(map[string]time.Time, 0)

	loadChatHistory()

	openaiClient = openai.NewClient() // option.WithAPIKey defaults to os.LookupEnv("OPENAI_API_KEY")

	discordSession = discord

	discord.AddHandler(onMessageCreate)
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

	messageStruct := chatMessage{
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

	if int(time.Since(userMessageCountLastReset[m.Author.ID]).Hours()) >= 1 {
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
	allBotNames := getBotDisplayNames()
	// TODO: write a string for chatCompletion in human language that describes all bot names and their respective guilds
	botNames := ""
	for guildID, botName := range allBotNames {
		botNames += botName + " in " + guildID + ". "
	}
	// behaviorPicker := rand.Intn(len(behavior))
	// make a list of all behaviors comma separated
	shuffledBehaviors := behaviorPool
	// Shuffle the behaviors
	rand.Shuffle(len(shuffledBehaviors), func(i, j int) {
		shuffledBehaviors[i], shuffledBehaviors[j] = shuffledBehaviors[j], shuffledBehaviors[i]
	})

	subsetSize := rand.Intn(2) + 1 // pick 1 or 2 behaviors
	subset := shuffledBehaviors[:subsetSize]
	behaviors := strings.Join(subset, "\n\t\t") // format the same way as the system message

	responseMentioned := "Diese Nachricht ist nicht an dich direkt gerichtet, aber du antwortest bitte dennoch darauf, damit das Gespräch weitergeführt wird."
	if isMentioned(m) {
		responseMentioned = "Diese Nachricht ist an dich direkt gerichtet."
	}

	// chatHistorySummary := generateHistorySummary()
	// if chatHistorySummary == "" {
	// 	chatHistorySummary = "Es gab keine vorherigen Nachrichten."
	// }

	// create a copy of msgHistory but without the last message
	msgHistoryCopy := chatHistory
	if len(msgHistoryCopy.ChatMessages) > 0 {
		msgHistoryCopy.ChatMessages = msgHistoryCopy.ChatMessages[:len(msgHistoryCopy.ChatMessages)-1]
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
	tokenLimit := rand.Int63n(151) + 50 // random number between 50 and 200
	systemMessage := `
			Du bist ein Chat Teilnehmer in einem Channel mit vielen verschiedenen Nutzern, auf mehreren Servern (auch Gilden genannt) und jeweils mit mehreren Textkanälen.
			Du kannst auf Servern unterschiedliche Namen haben.
			Deine Namen auf den jeweiligen Servern sind: ` + botNames + `
			Schreibe in lockerem Chatstil (weniger formelle Satzstrukturen, auslassen von Satzzeichen, Umgangssprache, Abkürzungen, unvollständige Sätze).
   			Du bist schreibfaul, Antworte so knapp wie möglich.
			` + behaviors + `
			` + responseMentioned + `
			Dies ist der bisherige Chatverlauf: ` + chatHistorySummary
	// `Noch ältere Verläufe als Zusammenfassung: ` + chatHistory.LongtermMemory

	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage(systemMessage)),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage(messageAsJSON)),
		}),
		Model:               openai.F(openai.ChatModelGPT4oMini),
		N:                   openai.Int(1),
		MaxCompletionTokens: openai.Int(tokenLimit),
	})

	if err != nil {
		slog.Info("Error while getting completion", "error", err)
		return "", err
	}

	slog.Info("Chat completion", "completion", chatCompletion)

	return chatCompletion.Choices[0].Message.Content, nil
}
