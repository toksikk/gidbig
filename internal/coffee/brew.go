package coffee

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	brewDuration   = 3 * time.Minute
	potCapacity    = 1.0
	emptyThreshold = 0.01
)

type brewState struct {
	readyAt      time.Time
	isReady      bool
	coffeeLiters float64
	takenBy      []string
}

type grabResult struct {
	cupML        float64
	summary      string
	isEmpty      bool
	alreadyTaken bool
	notReady     bool
}

var (
	brewMu     sync.RWMutex
	brewStates = map[string]*brewState{}

	sendBrewMessage      = defaultSendBrewMessage
	sendBrewReadyMessage = defaultSendBrewReadyMessage
	randCupSize          = defaultRandCupSize
)

// defaultRandCupSize returns a random cup size between 150ml and 350ml in 10ml steps.
func defaultRandCupSize() float64 {
	ml := 150 + rand.Intn(21)*10
	return float64(ml) / 1000.0
}

func defaultSendBrewMessage(s *discordgo.Session, channelID, content string) {
	if s == nil {
		return
	}
	_, _ = s.ChannelMessageSend(channelID, content)
}

func defaultSendBrewReadyMessage(s *discordgo.Session, guildID, channelID string) {
	if s == nil {
		return
	}
	announcement := generateInteractionMessage(s, channelID,
		"The coffee is ready. Announce it to the channel in one short, inviting sentence.",
		"☕ Coffee is ready! Grab your cup!")
	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: announcement,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "☕ Grab a cup",
						Style:    discordgo.PrimaryButton,
						CustomID: "grab_coffee",
					},
				},
			},
		},
	})
}

func brewStateKey(guildID, channelID string) string {
	if guildID == "" {
		return "dm:" + channelID
	}
	return guildID + ":" + channelID
}

func hasActiveBrew(guildID, channelID string) bool {
	brewMu.RLock()
	defer brewMu.RUnlock()
	_, exists := brewStates[brewStateKey(guildID, channelID)]
	return exists
}

// startBrew attempts to start a new brew in the given channel.
// Returns (true, readyAt) if a brew is already in progress, (false, readyAt) if newly started.
func startBrew(s *discordgo.Session, guildID, channelID string) (alreadyBrewing bool, readyAt time.Time) {
	brewMu.Lock()
	key := brewStateKey(guildID, channelID)
	if st, exists := brewStates[key]; exists && !st.isReady {
		brewMu.Unlock()
		return true, st.readyAt
	}
	readyAt = nowFunc().Add(brewDuration)
	brewStates[key] = &brewState{
		readyAt:      readyAt,
		isReady:      false,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	go func() {
		time.Sleep(time.Until(readyAt))
		markBrewReady(s, guildID, channelID)
	}()

	return false, readyAt
}

func markBrewReady(s *discordgo.Session, guildID, channelID string) {
	brewMu.Lock()
	key := brewStateKey(guildID, channelID)
	st, ok := brewStates[key]
	if ok && !st.isReady {
		st.isReady = true
	} else {
		ok = false
	}
	brewMu.Unlock()

	if ok {
		sendBrewReadyMessage(s, guildID, channelID)
	}
}

// grabCoffee handles the state mutation for a user grabbing a cup of coffee.
func grabCoffee(guildID, channelID, userID string) grabResult {
	brewMu.Lock()
	defer brewMu.Unlock()

	key := brewStateKey(guildID, channelID)
	st, ok := brewStates[key]
	if !ok || !st.isReady || st.coffeeLiters < emptyThreshold {
		return grabResult{notReady: true}
	}
	if slices.Contains(st.takenBy, userID) {
		return grabResult{alreadyTaken: true}
	}
	cup := randCupSize()
	st.takenBy = append(st.takenBy, userID)
	st.coffeeLiters -= cup
	if st.coffeeLiters < 0 {
		st.coffeeLiters = 0
	}
	summary := buildBrewSummary(st)
	isEmpty := st.coffeeLiters < emptyThreshold
	if isEmpty {
		delete(brewStates, key)
	}
	return grabResult{
		cupML:   cup,
		summary: summary,
		isEmpty: isEmpty,
	}
}

func handleBrewMessage(s *discordgo.Session, guildID, channelID, messageID, userID string) {
	result := grabCoffee(guildID, channelID, userID)
	if result.notReady || result.alreadyTaken {
		return
	}
	reactOnMessage(s, channelID, messageID, "☕", "add")
	sendBrewMessage(s, channelID, result.summary)
	if result.isEmpty {
		sendBrewMessage(s, channelID, "The coffee pot is empty!")
	}
}

func buildBrewSummary(st *brewState) string {
	mentions := make([]string, len(st.takenBy))
	for i, uid := range st.takenBy {
		mentions[i] = "<@" + uid + ">"
	}
	return fmt.Sprintf("%s took coffee. %.2fL remaining", strings.Join(mentions, ", "), st.coffeeLiters)
}
