package coffee

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	brewDuration   = 3 * time.Minute
	potCapacity    = 2.0
	emptyThreshold = 0.01
)

// CupTaken records the details of a single cup of coffee taken by a user.
type CupTaken struct {
	ml    float64
	milk  bool
	sugar bool
}

type grabRecord struct {
	userID string
	cup    CupTaken
}

type brewState struct {
	readyAt           time.Time
	isReady           bool
	coffeeLiters      float64
	grabs             []grabRecord
	buttonLabels      [3]string
	readyAnnouncement string
}

type grabResult struct {
	notReady     bool
	noCup        bool // tried to add milk/sugar but user has no cup in this brew
	isEmpty      bool
	cupML        float64
	updatedMsg   string
	buttonLabels [3]string
}

var (
	brewMu     sync.RWMutex
	brewStates = map[string]*brewState{}

	sendBrewReadyMessage     = defaultSendBrewReadyMessage
	randCupSize              = defaultRandCupSize
	generateBrewButtonLabels = func(_ *discordgo.Session, _ string) [3]string {
		return [3]string{"☕ Grab a cup", "🥛 With milk", "🍬 With sugar"}
	}
)

func defaultRandCupSize() float64 {
	ml := 150 + rand.Intn(21)*10
	return float64(ml) / 1000.0
}

func defaultSendBrewReadyMessage(s *discordgo.Session, guildID, channelID string) {
	if s == nil {
		return
	}
	announcement := generateInteractionMessage(s, channelID,
		"The coffee is ready. Announce it to the channel in one short, inviting sentence.",
		"☕ Coffee is ready! Grab your cup!")
	labels := generateBrewButtonLabels(s, channelID)

	brewMu.Lock()
	key := brewStateKey(guildID, channelID)
	if st, ok := brewStates[key]; ok {
		st.readyAnnouncement = announcement
		st.buttonLabels = labels
	}
	brewMu.Unlock()

	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: announcement,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    labels[0],
						Style:    discordgo.PrimaryButton,
						CustomID: "grab_coffee",
					},
					discordgo.Button{
						Label:    labels[1],
						Style:    discordgo.SecondaryButton,
						CustomID: "grab_milk",
					},
					discordgo.Button{
						Label:    labels[2],
						Style:    discordgo.SecondaryButton,
						CustomID: "grab_sugar",
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
		time.Sleep(brewDuration)
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

// grabCoffee handles the state mutation for a user grabbing a plain cup of coffee.
// Multiple grabs by the same user are allowed. Milk/sugar can be added afterwards
// via addToLastCup.
func grabCoffee(guildID, channelID, userID string) grabResult {
	brewMu.Lock()
	defer brewMu.Unlock()

	key := brewStateKey(guildID, channelID)
	st, ok := brewStates[key]
	if !ok || !st.isReady || st.coffeeLiters < emptyThreshold {
		return grabResult{notReady: true}
	}
	cup := CupTaken{ml: randCupSize()}
	if cup.ml > st.coffeeLiters {
		cup.ml = st.coffeeLiters
	}
	st.grabs = append(st.grabs, grabRecord{userID: userID, cup: cup})
	st.coffeeLiters -= cup.ml
	labels := st.buttonLabels
	isEmpty := st.coffeeLiters < emptyThreshold
	updatedMsg := buildBrewMessage(st)
	if isEmpty {
		delete(brewStates, key)
	}
	return grabResult{
		cupML:        cup.ml,
		isEmpty:      isEmpty,
		updatedMsg:   updatedMsg,
		buttonLabels: labels,
	}
}

// addToLastCup adds milk and/or sugar to the most recent cup grabbed by userID in
// this brew. Returns noCup=true if the user has not grabbed a cup yet.
func addToLastCup(guildID, channelID, userID string, milk, sugar bool) grabResult {
	brewMu.Lock()
	defer brewMu.Unlock()

	key := brewStateKey(guildID, channelID)
	st, ok := brewStates[key]
	if !ok || !st.isReady {
		return grabResult{notReady: true}
	}
	lastIdx := -1
	for i := len(st.grabs) - 1; i >= 0; i-- {
		if st.grabs[i].userID == userID {
			lastIdx = i
			break
		}
	}
	if lastIdx == -1 {
		return grabResult{noCup: true}
	}
	if milk {
		st.grabs[lastIdx].cup.milk = true
	}
	if sugar {
		st.grabs[lastIdx].cup.sugar = true
	}
	return grabResult{
		updatedMsg:   buildBrewMessage(st),
		buttonLabels: st.buttonLabels,
	}
}

func buildBrewMessage(st *brewState) string {
	header := st.readyAnnouncement
	if header == "" {
		header = "☕ Coffee is ready! Grab your cup!"
	}

	var sb strings.Builder
	sb.WriteString(header)

	// Group by user, maintaining first-grab order for deterministic output.
	userOrder := []string{}
	userGrabs := map[string][]CupTaken{}
	for _, gr := range st.grabs {
		if _, seen := userGrabs[gr.userID]; !seen {
			userOrder = append(userOrder, gr.userID)
		}
		userGrabs[gr.userID] = append(userGrabs[gr.userID], gr.cup)
	}

	for _, uid := range userOrder {
		cups := userGrabs[uid]
		var totalML float64
		cupDescs := make([]string, 0, len(cups))
		for _, c := range cups {
			totalML += c.ml
			emoji := "☕"
			if c.milk {
				emoji += "🥛"
			}
			if c.sugar {
				emoji += "🍬"
			}
			cupDescs = append(cupDescs, fmt.Sprintf("%s ~%.0fml", emoji, c.ml*1000))
		}
		cupsWord := "cup"
		if len(cups) > 1 {
			cupsWord = "cups"
		}
		fmt.Fprintf(&sb, "\n<@%s>: %d %s (~%.0fml) — %s",
			uid, len(cups), cupsWord, totalML*1000, strings.Join(cupDescs, ", "))
	}

	if st.coffeeLiters < emptyThreshold {
		sb.WriteString("\n\n_(pot is empty)_")
	} else {
		fmt.Fprintf(&sb, "\n\n%.2fL remaining", st.coffeeLiters)
	}
	return sb.String()
}
