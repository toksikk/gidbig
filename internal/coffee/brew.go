package coffee

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	brewDuration   = 3 * time.Minute
	cupSize        = 0.25
	potCapacity    = 1.0
	emptyThreshold = 0.01
)

type brewState struct {
	readyAt      time.Time
	isReady      bool
	coffeeLiters float64
	takenBy      []string
}

var (
	brewMu     sync.Mutex
	brewStates = map[string]*brewState{}

	sendBrewMessage = defaultSendBrewMessage
)

func defaultSendBrewMessage(s *discordgo.Session, channelID, content string) {
	if s == nil {
		return
	}
	_, _ = s.ChannelMessageSend(channelID, content)
}

func brewStateKey(guildID, channelID string) string {
	if guildID == "" {
		return "dm:" + channelID
	}
	return guildID + ":" + channelID
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
		sendBrewMessage(s, channelID, "☕ Coffee is ready! Everyone grab a cup with `/coffee`!")
	}
}

func handleBrewMessage(s *discordgo.Session, guildID, channelID, messageID, userID string) {
	brewMu.Lock()
	key := brewStateKey(guildID, channelID)
	st, ok := brewStates[key]
	if !ok || !st.isReady || st.coffeeLiters < emptyThreshold {
		brewMu.Unlock()
		return
	}
	for _, uid := range st.takenBy {
		if uid == userID {
			brewMu.Unlock()
			return
		}
	}
	st.takenBy = append(st.takenBy, userID)
	st.coffeeLiters -= cupSize
	summary := buildBrewSummary(st)
	isEmpty := st.coffeeLiters < emptyThreshold
	if isEmpty {
		delete(brewStates, key)
	}
	brewMu.Unlock()

	reactOnMessage(s, channelID, messageID, "☕", "add")
	sendBrewMessage(s, channelID, summary)
	if isEmpty {
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
