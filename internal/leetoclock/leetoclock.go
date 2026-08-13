package leetoclock

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
	"github.com/toksikk/gidbig/internal/util"
)

const (
	firstPlace    = "🥇"
	secondPlace   = "🥈"
	thirdPlace    = "🥉"
	otherPlace    = "🏅"
	zonk          = ":zonk:750630908372975636"
	lol           = ":louisdefunes_lol:357611625102180373"
	notamused     = ":louisdefunes_notamused:357611625521479680"
	wat           = ":gustaff:721122751145967679"
	defaultHour   = 13
	defaultMinute = 37
)

// Module implements bot.Module for the daily Leet o'Clock game.
type Module struct {
	session *discordgo.Session
	store   *datastore.Store

	stateMu                   sync.RWMutex
	target                    time.Time
	targetHour                int
	targetMinute              int
	playersWithClockReactions map[string]struct{}
	announcementChannels      []string
	renewReactionsMu          sync.Mutex
	lifecycleMu               sync.Mutex
	accepting                 bool
	handlerWG                 sync.WaitGroup
	workWG                    sync.WaitGroup

	now              func() time.Time
	messageTimestamp func(string) time.Time
	reactOnMessage   func(*discordgo.Session, string, string, string, string)
	renewGame        func(datastore.Game)
	tickInterval     time.Duration
}

// New returns a Module with production defaults.
func New() *Module {
	m := &Module{
		targetHour:                defaultHour,
		targetMinute:              defaultMinute,
		playersWithClockReactions: make(map[string]struct{}),
		now:                       time.Now,
		messageTimestamp:          util.GetTimestampOfMessage,
		reactOnMessage:            util.ReactOnMessage,
		tickInterval:              time.Minute,
	}
	m.renewGame = m.renewReactions
	return m
}

// Name returns the module identifier.
func (m *Module) Name() string { return "leetoclock" }

// Init opens the shared database and captures runtime dependencies.
func (m *Module) Init(d bot.Deps) error {
	dbPath := "gidbig.db"
	if d.Config != nil && d.Config.Database.Path != "" {
		dbPath = d.Config.Database.Path
	}

	store, err := datastore.Open(dbPath)
	if err != nil {
		return fmt.Errorf("leetoclock: open store: %w", err)
	}
	m.store = store
	m.session = d.Session
	m.accepting = true

	if os.Getenv("LEETOCLOCK_DEBUG") != "" {
		target := m.now().Add(time.Minute)
		m.targetHour, m.targetMinute = target.Hour(), target.Minute()
		m.tickInterval = time.Second
	}
	if channel := os.Getenv("LEETOCLOCK_DEBUG_CHANNEL"); channel != "" {
		m.announcementChannels = append(m.announcementChannels, channel)
	}
	m.updateTarget()
	slog.Info("leetoclock: initialized")
	return nil
}

func (m *Module) Commands() []*discordgo.ApplicationCommand { return nil }

// Listeners returns the Discord listeners owned by this module.
func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{m.onMessageCreate}
}

func (m *Module) Components() []bot.ComponentHandler { return nil }

// Background returns independently supervised announcement loops.
func (m *Module) Background() []bot.BackgroundTask {
	return []bot.BackgroundTask{
		{Name: "leetoclock/preparation", Run: m.runPreparationLoop},
		{Name: "leetoclock/winners", Run: m.runWinnerLoop},
	}
}

// Shutdown closes the database after supervised tasks have stopped.
func (m *Module) Shutdown() error {
	m.lifecycleMu.Lock()
	if !m.accepting {
		m.lifecycleMu.Unlock()
		return nil
	}
	m.accepting = false
	m.lifecycleMu.Unlock()

	m.handlerWG.Wait()
	m.workWG.Wait()
	return m.store.Close()
}

func (m *Module) runPreparationLoop(ctx context.Context) {
	for {
		m.updateTarget()
		if m.isOnTargetTimeRange(m.now(), false) {
			m.announcePreparation()
			if !wait(ctx, 2*time.Minute) {
				return
			}
		} else if !wait(ctx, m.tickInterval) {
			return
		}
	}
}

func (m *Module) runWinnerLoop(ctx context.Context) {
	for {
		m.updateTarget()
		if m.isOnTargetTimeRange(m.now(), true) {
			if !wait(ctx, 62*time.Second) {
				return
			}
			m.announceTodaysWinners()
			m.resetGameVars()
			if !wait(ctx, time.Minute) {
				return
			}
		} else if !wait(ctx, m.tickInterval) {
			return
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *Module) announcePreparation() {
	target := m.currentTarget()
	for _, channelID := range m.announcementChannels {
		if _, err := m.session.ChannelMessageSend(channelID, fmt.Sprintf("## Leet o'Clock scheduled:\n<t:%d:R>", target.Unix())); err != nil {
			slog.Error("leetoclock: send preparation announcement", "error", err)
		}
	}
}

func isScoreInScoreArray(score datastore.Score, scores []datastore.Score) bool {
	for _, candidate := range scores {
		if candidate.PlayerID == score.PlayerID {
			return true
		}
	}
	return false
}

func sortScoreArrayByScore(scores []datastore.Score) []datastore.Score {
	sort.Slice(scores, func(i, j int) bool { return scores[i].Score < scores[j].Score })
	return scores
}

func (m *Module) buildScoreboardForGame(game datastore.Game) (string, []datastore.Score, []datastore.Score, []datastore.Score, error) {
	scores, err := m.store.GetScoresForGameID(game.ID)
	if err != nil {
		return "", nil, nil, nil, err
	}
	scores = sortScoreArrayByScore(scores)
	channel, err := m.session.Channel(game.ChannelID)
	if err != nil {
		return "", nil, nil, nil, err
	}

	scoreboard := fmt.Sprintf("## 1337erboard for <t:%d>\n", m.currentTarget().Unix())
	earlyBirds := make([]datastore.Score, 0)
	winners := make([]datastore.Score, 0)
	zonks := make([]datastore.Score, 0)

	for _, score := range scores {
		if score.Score >= 0 && !isScoreInScoreArray(score, winners) && len(winners) < 3 {
			winners = append(winners, score)
		}
	}
	if len(winners) > 0 {
		scoreboard += "### Top scorers\n"
	}
	for i, winner := range winners {
		awards := []string{firstPlace, secondPlace, thirdPlace}
		award := otherPlace
		if i < len(awards) {
			award = awards[i]
		}
		player, err := m.store.GetPlayerByID(winner.PlayerID)
		if err != nil {
			return "", nil, nil, nil, err
		}
		scoreboard += fmt.Sprintf("%s <@%s> with %d ms (https://discord.com/channels/%s/%s/%s)\n", award, player.UserID, winner.Score, channel.GuildID, game.ChannelID, winner.MessageID)
	}

	for _, score := range scores {
		if score.Score > 0 && !isScoreInScoreArray(score, zonks) && !isScoreInScoreArray(score, winners) {
			zonks = append(zonks, score)
		}
	}
	if len(zonks) > 0 {
		scoreboard += "### Zonks\n"
	}
	for _, score := range zonks {
		player, err := m.store.GetPlayerByID(score.PlayerID)
		if err != nil {
			return "", nil, nil, nil, err
		}
		scoreboard += fmt.Sprintf("😭 <@%s> with %d ms (https://discord.com/channels/%s/%s/%s)\n", player.UserID, score.Score, channel.GuildID, game.ChannelID, score.MessageID)
	}

	for _, score := range scores {
		if score.Score >= -5000 && score.Score < 0 && !isScoreInScoreArray(score, earlyBirds) {
			earlyBirds = append(earlyBirds, score)
		}
	}
	if len(earlyBirds) > 0 {
		scoreboard += "### Honorlolable mentions\n"
	}
	for _, score := range earlyBirds {
		player, err := m.store.GetPlayerByID(score.PlayerID)
		if err != nil {
			return "", nil, nil, nil, err
		}
		award := "🤨"
		if isScoreInScoreArray(score, zonks) {
			award = "🫠"
		} else if isScoreInScoreArray(score, winners) {
			award = "😐"
		}
		scoreboard += fmt.Sprintf("%s <@%s> with %d ms (https://discord.com/channels/%s/%s/%s)\n", award, player.UserID, score.Score, channel.GuildID, game.ChannelID, score.MessageID)
	}

	return scoreboard, earlyBirds, winners, zonks, nil
}

func (m *Module) renewReactions(game datastore.Game) {
	m.renewReactionsMu.Lock()
	defer m.renewReactionsMu.Unlock()

	_, earlyBirds, winners, zonks, err := m.buildScoreboardForGame(game)
	if err != nil {
		slog.Error("leetoclock: build scoreboard", "error", err)
		return
	}
	for _, score := range earlyBirds {
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, lol, "remove")
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, notamused, "remove")
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, wat, "remove")
		if isScoreInScoreArray(score, zonks) {
			m.reactOnMessage(m.session, game.ChannelID, score.MessageID, lol, "add")
		} else if isScoreInScoreArray(score, winners) {
			m.reactOnMessage(m.session, game.ChannelID, score.MessageID, notamused, "add")
		} else {
			m.reactOnMessage(m.session, game.ChannelID, score.MessageID, wat, "add")
		}
	}
	for i, score := range winners {
		for _, award := range []string{firstPlace, secondPlace, thirdPlace} {
			m.reactOnMessage(m.session, game.ChannelID, score.MessageID, award, "remove")
		}
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, []string{firstPlace, secondPlace, thirdPlace}[i], "add")
	}
	for _, score := range zonks {
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, zonk, "remove")
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, zonk, "add")
	}
}

func (m *Module) announceTodaysWinners() {
	games, err := m.store.GetGamesByDate(m.now())
	if err != nil {
		slog.Error("leetoclock: get today's games", "error", err)
		return
	}
	for _, game := range games {
		scoreboard, _, _, _, err := m.buildScoreboardForGame(game)
		if err != nil {
			slog.Error("leetoclock: build scoreboard", "error", err)
			continue
		}
		if _, err := m.session.ChannelMessageSend(game.ChannelID, scoreboard); err != nil {
			slog.Error("leetoclock: send scoreboard", "error", err)
		}
	}
}

func (m *Module) resetGameVars() {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.playersWithClockReactions = make(map[string]struct{})
}

func (m *Module) updateTarget() {
	now := m.now()
	target := time.Date(now.Year(), now.Month(), now.Day(), m.targetHour, m.targetMinute, 0, 0, now.Location())
	m.stateMu.Lock()
	m.target = target
	m.stateMu.Unlock()
}

func (m *Module) currentTarget() time.Time {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.target
}

func (m *Module) isOnTargetTimeRange(timestamp time.Time, onlyOnTarget bool) bool {
	target := m.currentTarget()
	if timestamp.Hour() == target.Hour() && timestamp.Minute() == target.Minute() {
		return true
	}
	if !onlyOnTarget {
		oneMinuteBefore := target.Add(-time.Minute)
		return timestamp.Hour() == oneMinuteBefore.Hour() && timestamp.Minute() == oneMinuteBefore.Minute()
	}
	return false
}

func (m *Module) calculateScore(timestamp time.Time) int {
	return int(timestamp.Sub(m.currentTarget()).Milliseconds())
}

func (m *Module) addClockReaction(userID string) bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if _, exists := m.playersWithClockReactions[userID]; exists {
		return false
	}
	m.playersWithClockReactions[userID] = struct{}{}
	return true
}

func (m *Module) onMessageCreate(s *discordgo.Session, event *discordgo.MessageCreate) {
	if !m.beginHandler() {
		return
	}
	defer m.handlerWG.Done()

	if event == nil || event.Message == nil || event.Author == nil || s == nil || s.State == nil || s.State.User == nil {
		return
	}
	if event.Author.ID == s.State.User.ID || event.Author.Bot || m.store == nil {
		return
	}

	messageTimestamp := m.messageTimestamp(event.ID)
	if !m.isOnTargetTimeRange(messageTimestamp, false) {
		return
	}
	season, err := m.store.EnsureSeason(m.now())
	if err != nil {
		slog.Error("leetoclock: ensure season", "error", err)
		return
	}
	game, err := m.store.EnsureGame(event.ChannelID, event.GuildID, m.currentTarget(), season.ID)
	if err != nil {
		slog.Error("leetoclock: ensure game", "error", err)
		return
	}
	player, err := m.store.EnsurePlayer(event.Author.ID)
	if err != nil {
		slog.Error("leetoclock: ensure player", "error", err)
		return
	}
	if err := m.store.CreateScore(event.ID, player.ID, m.calculateScore(messageTimestamp), game.ID); err != nil {
		slog.Error("leetoclock: create score", "error", err)
		return
	}

	if m.isOnTargetTimeRange(messageTimestamp, true) && m.addClockReaction(event.Author.ID) {
		m.reactOnMessage(s, event.ChannelID, event.ID, "⏰", "add")
	}
	m.workWG.Add(1)
	go func() {
		defer m.workWG.Done()
		m.renewGame(*game)
	}()
}

func (m *Module) beginHandler() bool {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if !m.accepting {
		return false
	}
	m.handlerWG.Add(1)
	return true
}
