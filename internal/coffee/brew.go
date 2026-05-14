package coffee

import (
	"fmt"
	"math/rand"
	"strings"
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
	liters float64
	milk   bool
	sugar  bool
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
	noCup        bool
	isEmpty      bool
	cupLiters    float64
	updatedMsg   string
	buttonLabels [3]string
}

func defaultRandCupSize() float64 {
	ml := 150 + rand.Intn(21)*10
	return float64(ml) / 1000.0
}

func (m *Module) defaultSendBrewReadyMessage(s *discordgo.Session, guildID, channelID string) {
	if s == nil {
		return
	}
	announcement := m.generateInteractionMessage(s, channelID,
		"The coffee is ready. Announce it to the channel in one short, inviting sentence.",
		"☕ Coffee is ready! Grab your cup!")
	labels := m.generateBrewButtonLabels(s, channelID)

	m.brewMu.Lock()
	key := brewStateKey(guildID, channelID)
	if st, ok := m.brewStates[key]; ok {
		st.readyAnnouncement = announcement
		st.buttonLabels = labels
	}
	m.brewMu.Unlock()

	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: announcement,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    labels[0],
						Style:    discordgo.PrimaryButton,
						CustomID: "coffee:grab_coffee",
					},
					discordgo.Button{
						Label:    labels[1],
						Style:    discordgo.SecondaryButton,
						CustomID: "coffee:grab_milk",
					},
					discordgo.Button{
						Label:    labels[2],
						Style:    discordgo.SecondaryButton,
						CustomID: "coffee:grab_sugar",
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

func (m *Module) hasActiveBrew(guildID, channelID string) bool {
	m.brewMu.RLock()
	defer m.brewMu.RUnlock()
	_, exists := m.brewStates[brewStateKey(guildID, channelID)]
	return exists
}

// startBrew attempts to start a new brew in the given channel.
// Returns (true, readyAt) if a brew is already in progress, (false, readyAt) if newly started.
func (m *Module) startBrew(s *discordgo.Session, guildID, channelID string) (alreadyBrewing bool, readyAt time.Time) {
	m.brewMu.Lock()
	key := brewStateKey(guildID, channelID)
	if st, exists := m.brewStates[key]; exists && !st.isReady {
		m.brewMu.Unlock()
		return true, st.readyAt
	}
	readyAt = m.nowFunc().Add(brewDuration)
	m.brewStates[key] = &brewState{
		readyAt:      readyAt,
		isReady:      false,
		coffeeLiters: potCapacity,
	}
	m.brewMu.Unlock()

	go func() {
		time.Sleep(brewDuration)
		m.markBrewReady(s, guildID, channelID)
	}()

	return false, readyAt
}

func (m *Module) markBrewReady(s *discordgo.Session, guildID, channelID string) {
	m.brewMu.Lock()
	key := brewStateKey(guildID, channelID)
	st, ok := m.brewStates[key]
	if ok && !st.isReady {
		st.isReady = true
	} else {
		ok = false
	}
	m.brewMu.Unlock()

	if ok {
		m.sendBrewReadyMessage(s, guildID, channelID)
	}
}

func (m *Module) grabCoffee(guildID, channelID, userID string) grabResult {
	m.brewMu.Lock()
	defer m.brewMu.Unlock()

	key := brewStateKey(guildID, channelID)
	st, ok := m.brewStates[key]
	if !ok || !st.isReady || st.coffeeLiters < emptyThreshold {
		return grabResult{notReady: true}
	}
	cup := CupTaken{liters: m.randCupSize()}
	if cup.liters > st.coffeeLiters {
		cup.liters = st.coffeeLiters
	}
	st.grabs = append(st.grabs, grabRecord{userID: userID, cup: cup})
	st.coffeeLiters -= cup.liters
	labels := st.buttonLabels
	isEmpty := st.coffeeLiters < emptyThreshold
	updatedMsg := buildBrewMessage(st)
	if isEmpty {
		delete(m.brewStates, key)
	}
	return grabResult{
		cupLiters:    cup.liters,
		isEmpty:      isEmpty,
		updatedMsg:   updatedMsg,
		buttonLabels: labels,
	}
}

func (m *Module) addToLastCup(guildID, channelID, userID string, milk, sugar bool) grabResult {
	m.brewMu.Lock()
	defer m.brewMu.Unlock()

	key := brewStateKey(guildID, channelID)
	st, ok := m.brewStates[key]
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
		var totalLiters float64
		cupDescs := make([]string, 0, len(cups))
		for _, c := range cups {
			totalLiters += c.liters
			emoji := "☕"
			if c.milk {
				emoji += "🥛"
			}
			if c.sugar {
				emoji += "🍬"
			}
			cupDescs = append(cupDescs, fmt.Sprintf("%s ~%.0fml", emoji, c.liters*1000))
		}
		cupsWord := "cup"
		if len(cups) > 1 {
			cupsWord = "cups"
		}
		fmt.Fprintf(&sb, "\n<@%s>: %d %s (~%.0fml) — %s",
			uid, len(cups), cupsWord, totalLiters*1000, strings.Join(cupDescs, ", "))
	}

	if st.coffeeLiters < emptyThreshold {
		sb.WriteString("\n\n_(pot is empty)_")
	} else {
		fmt.Fprintf(&sb, "\n\n%.2fL remaining", st.coffeeLiters)
	}
	return sb.String()
}
