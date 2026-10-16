package leetoclock

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/shareddb"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"
)

const (
	firstPlace        = "🥇"
	secondPlace       = "🥈"
	thirdPlace        = "🥉"
	otherPlace        = "🏅"
	fallbackZonk      = "😴"
	fallbackLol       = "😂"
	fallbackNotAmused = "😒"
	fallbackWat       = "🧐"
	defaultHour       = 13
	defaultMinute     = 37
)

// Module implements bot.Module for the daily Leet o'Clock game.
type Module struct {
	session *discordgo.Session
	store   *datastore.Store
	ownsDB  bool
	tools   *tool.Registry

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
	season           func() util.Season
	messageTimestamp func(string) time.Time
	reactOnMessage   func(*discordgo.Session, string, string, string, string)
	renewGame        func(datastore.Game)
	tickInterval     time.Duration // guarded by stateMu

	settings     *settings.Store
	cfgDebug     bool   // config.yaml value, used when settings is nil
	debugChannel string // cold
	baseChannels []string
	debug        bool      // guarded by stateMu; last applied debug flag
	debugTarget  time.Time // guarded by stateMu; full debug round time

	// resolveMember turns a free-text player name from a tool call into a
	// Discord user ID. Defaults to util.ResolveMemberQuery; overridable in tests.
	resolveMember func(*discordgo.Session, string, string) (util.MemberMatch, bool, string)

	emojiZonk      string
	emojiLol       string
	emojiNotAmused string
	emojiWat       string
}

// New returns a Module with production defaults.
func New() *Module {
	m := &Module{
		targetHour:                defaultHour,
		targetMinute:              defaultMinute,
		playersWithClockReactions: make(map[string]struct{}),
		now:                       time.Now,
		season:                    util.CurrentSeason,
		messageTimestamp:          util.GetTimestampOfMessage,
		reactOnMessage:            util.ReactOnMessage,
		resolveMember:             util.ResolveMemberQuery,
		tickInterval:              time.Minute,
		emojiZonk:                 fallbackZonk,
		emojiLol:                  fallbackLol,
		emojiNotAmused:            fallbackNotAmused,
		emojiWat:                  fallbackWat,
	}
	m.renewGame = m.renewReactions
	return m
}

// Name returns the module identifier.
func (m *Module) Name() string { return "leetoclock" }

// resolveEmoji returns the emoji to react with. A configured value is treated
// as a guild emoji ID when numeric (formatted as "name:id") and otherwise used
// verbatim, so Unicode overrides also work. Empty config uses the fallback.
func resolveEmoji(name, configured, fallback string) string {
	if configured == "" {
		return fallback
	}
	if _, err := strconv.ParseUint(configured, 10, 64); err == nil {
		return name + ":" + configured
	}
	return configured
}

// Init opens the shared database and captures runtime dependencies.
func (m *Module) Init(d bot.Deps) error {
	var (
		store *datastore.Store
		err   error
	)
	if d.DB != nil {
		store, err = datastore.Use(d.DB)
		m.ownsDB = false
	} else {
		store, err = datastore.Open(shareddb.Filename)
		m.ownsDB = true
	}
	if err != nil {
		return fmt.Errorf("leetoclock: open store: %w", err)
	}
	m.store = store
	m.session = d.Session
	m.accepting = true

	var lec cfg.LeetoclockConfig
	if d.Config != nil {
		lec = d.Config.Leetoclock
	}
	m.settings = d.Settings
	m.cfgDebug = lec.Debug
	m.debugChannel = lec.DebugChannel
	m.baseChannels = append([]string(nil), lec.AnnouncementChannels...)
	debug := m.debugEnabled()
	m.applyDebug(debug)
	m.emojiZonk = resolveEmoji("zonk", lec.Emojis.Zonk, fallbackZonk)
	m.emojiLol = resolveEmoji("lol", lec.Emojis.Lol, fallbackLol)
	m.emojiNotAmused = resolveEmoji("notamused", lec.Emojis.NotAmused, fallbackNotAmused)
	m.emojiWat = resolveEmoji("wat", lec.Emojis.Wat, fallbackWat)
	m.updateTarget()
	m.tools = d.Tools
	m.registerTools()
	d.Router.OnMessage(m.onMessageCreate)
	for _, cmd := range m.Commands() {
		d.Router.AddCommand(cmd, m.onInteractionCreate)
	}
	slog.Info("leetoclock: initialized", "debug", debug, "announcement_channels", m.channels(), "target", m.currentTarget().String())
	return nil
}

func (m *Module) Commands() []*discordgo.ApplicationCommand { return recordCommands() }

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
	// A shared database is owned by the composition root, not by this module.
	if !m.ownsDB {
		return nil
	}
	return m.store.Close()
}

// debugEnabled reads the effective leetoclock.debug flag: the settings store
// when wired, otherwise the config.yaml value.
func (m *Module) debugEnabled() bool {
	if m.settings != nil {
		return m.settings.Bool(settings.KeyLeetoclockDebug)
	}
	return m.cfgDebug
}

// applyDebug switches target, tick and announcement channels between debug and
// normal mode. Debug: target now+1min, 1s tick, debug channel only. Normal:
// 13:37, 1min tick, configured announcement channels.
func (m *Module) applyDebug(debug bool) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.applyDebugLocked(debug)
}

// applyDebugLocked is applyDebug for callers already holding stateMu. The debug
// target keeps its full date, so enabling at 23:59 targets 00:00 the next day.
func (m *Module) applyDebugLocked(debug bool) {
	m.debug = debug
	if debug {
		target := m.now().Add(time.Minute).Truncate(time.Minute)
		m.debugTarget = target
		m.target = target
		m.targetHour, m.targetMinute = target.Hour(), target.Minute()
		m.tickInterval = time.Second
		m.announcementChannels = nil
		if m.debugChannel != "" {
			m.announcementChannels = []string{m.debugChannel}
		}
		return
	}
	m.targetHour, m.targetMinute = defaultHour, defaultMinute
	m.tickInterval = time.Minute
	m.announcementChannels = append([]string(nil), m.baseChannels...)
}

// syncDebug applies the debug flag only when it changed since the last apply,
// so a debug target is not pushed forward every tick.
func (m *Module) syncDebug() {
	debug := m.debugEnabled()
	m.stateMu.Lock()
	changed := debug != m.debug
	if changed {
		m.applyDebugLocked(debug)
	}
	m.stateMu.Unlock()
	if changed {
		slog.Info("leetoclock: debug mode changed", "debug", debug)
	}
}

func (m *Module) currentTick() time.Duration {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.tickInterval
}

// channels returns a copy of the announcement channels, safe to use without
// holding stateMu across Discord calls.
func (m *Module) channels() []string {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return append([]string(nil), m.announcementChannels...)
}

func (m *Module) runPreparationLoop(ctx context.Context) {
	for {
		m.syncDebug()
		m.updateTarget()
		if m.isOnTargetTimeRange(m.now(), false) {
			m.announcePreparation()
			if !wait(ctx, 2*time.Minute) {
				return
			}
		} else if !wait(ctx, m.currentTick()) {
			return
		}
	}
}

func (m *Module) runWinnerLoop(ctx context.Context) {
	for {
		m.syncDebug()
		m.updateTarget()
		if m.isOnTargetTimeRange(m.now(), true) {
			// Capture the round before waiting: a debug flip during the wait
			// must not relabel the scoreboard of the round that just ended.
			target := m.currentTarget()
			if !wait(ctx, 62*time.Second) {
				return
			}
			m.announceTodaysWinners(target)
			m.resetGameVars()
			if !wait(ctx, time.Minute) {
				return
			}
		} else if !wait(ctx, m.currentTick()) {
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

func (m *Module) announcementTitle() string {
	if m.season() == util.SeasonEaster {
		return "## 🐣 Leet o'Clock Osterei-Jagd scheduled:"
	}
	return "## Leet o'Clock scheduled:"
}

func (m *Module) announcePreparation() {
	target := m.currentTarget()
	for _, channelID := range m.channels() {
		if _, err := m.session.ChannelMessageSend(channelID, fmt.Sprintf("%s\n<t:%d:R>", m.announcementTitle(), target.Unix())); err != nil {
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

func (m *Module) buildScoreboardForGame(game datastore.Game, target time.Time) (string, []datastore.Score, []datastore.Score, []datastore.Score, error) {
	scores, err := m.store.GetScoresForGameID(game.ID)
	if err != nil {
		return "", nil, nil, nil, err
	}
	scores = sortScoreArrayByScore(scores)
	channel, err := m.session.Channel(game.ChannelID)
	if err != nil {
		return "", nil, nil, nil, err
	}

	scoreboard := fmt.Sprintf("## 1337erboard for <t:%d>\n", target.Unix())
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

	_, earlyBirds, winners, zonks, err := m.buildScoreboardForGame(game, m.currentTarget())
	if err != nil {
		slog.Error("leetoclock: build scoreboard", "error", err)
		return
	}
	for _, score := range earlyBirds {
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, m.emojiLol, "remove")
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, m.emojiNotAmused, "remove")
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, m.emojiWat, "remove")
		if isScoreInScoreArray(score, zonks) {
			m.reactOnMessage(m.session, game.ChannelID, score.MessageID, m.emojiLol, "add")
		} else if isScoreInScoreArray(score, winners) {
			m.reactOnMessage(m.session, game.ChannelID, score.MessageID, m.emojiNotAmused, "add")
		} else {
			m.reactOnMessage(m.session, game.ChannelID, score.MessageID, m.emojiWat, "add")
		}
	}
	for i, score := range winners {
		for _, award := range []string{firstPlace, secondPlace, thirdPlace} {
			m.reactOnMessage(m.session, game.ChannelID, score.MessageID, award, "remove")
		}
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, []string{firstPlace, secondPlace, thirdPlace}[i], "add")
	}
	for _, score := range zonks {
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, m.emojiZonk, "remove")
		m.reactOnMessage(m.session, game.ChannelID, score.MessageID, m.emojiZonk, "add")
	}
}

func (m *Module) announceTodaysWinners(target time.Time) {
	games, err := m.store.GetGamesByDate(m.now())
	if err != nil {
		slog.Error("leetoclock: get today's games", "error", err)
		return
	}
	for _, game := range games {
		scoreboard, _, _, _, err := m.buildScoreboardForGame(game, target)
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
	m.stateMu.Lock()
	if m.debug {
		m.target = m.debugTarget
		m.stateMu.Unlock()
		return
	}
	m.target = time.Date(now.Year(), now.Month(), now.Day(), m.targetHour, m.targetMinute, 0, 0, now.Location())
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
