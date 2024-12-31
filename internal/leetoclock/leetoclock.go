package leetoclock

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
	"github.com/toksikk/gidbig/internal/util"
)

var tt time.Time

var tHourInt, tMinuteInt = 13, 37

var session *discordgo.Session

var preparationAnnounceLock = false
var winnerAnnounceLock = false
var renewReactionsLock = false

var playersWithClockReactions []string = []string{}

const firstPlace string = "🥇"
const secondPlace string = "🥈"
const thirdPlace string = "🥉"
const otherPlace string = "🏅"
const zonk string = ":zonk:750630908372975636"
const lol string = ":louisdefunes_lol:357611625102180373"
const notamused string = ":louisdefunes_notamused:357611625521479680"
const wat string = ":gustaff:721122751145967679"

var announcementChannels = []string{}

var store *datastore.Store

// Start the plugin
func Start(discord *discordgo.Session) {
	session = discord
	store = datastore.NewStore(datastore.InitDB())
	session.AddHandler(onMessageCreate)

	if os.Getenv("LEETOCLOCK_DEBUG") != "" {
		t := time.Now()
		target := t.Add(time.Minute * 1)
		tHourInt, tMinuteInt = target.Hour(), target.Minute()
		slog.Debug("Updated target time", "Hour", tHourInt, "Minute", tMinuteInt)
	}
	if os.Getenv("LEETOCLOCK_DEBUG_CHANNEL") != "" {
		announcementChannels = append(announcementChannels, os.Getenv("LEETOCLOCK_DEBUG_CHANNEL"))
	}
	go gameTick()
	slog.Info("leetoclock function registered")
}

func calculateScore(messageTimestamp time.Time) int {
	return int(messageTimestamp.Sub(tt).Milliseconds())
}

func isOnTargetTimeRange(messageTimestamp time.Time, onlyOnTarget bool) bool {
	oneMinuteBefore := tt.Add(-time.Minute * 1)
	if messageTimestamp.Hour() == tt.Hour() && messageTimestamp.Minute() == tt.Minute() {
		return true
	}
	if !onlyOnTarget {
		if messageTimestamp.Hour() == oneMinuteBefore.Hour() && messageTimestamp.Minute() == oneMinuteBefore.Minute() {
			return true
		}
	}
	return false
}

func announcePreparation() {
	if isOnTargetTimeRange(time.Now(), false) {
		preparationAnnounceLock = true
		for _, v := range announcementChannels {
			_, err := session.ChannelMessageSend(v, fmt.Sprintf("## Leet o'Clock scheduled:\n<t:%d:R>", tt.Unix()))
			if err != nil {
				slog.Error("Error while sending preparation announcement", "Error", err)
			}
		}
		time.Sleep(2 * time.Minute)
		preparationAnnounceLock = false
	}
}

func isScoreInScoreArray(s datastore.Score, a []datastore.Score) bool {
	for _, v := range a {
		if v.PlayerID == s.PlayerID {
			return true
		}
	}
	return false
}

func sortScoreArrayByScore(a []datastore.Score) []datastore.Score {
	for i := 0; i < len(a); i++ {
		for j := i + 1; j < len(a); j++ {
			if a[j].Score < a[i].Score {
				a[i], a[j] = a[j], a[i]
			}
		}
	}
	return a
}

func buildScoreboardForGame(game datastore.Game) (string, []datastore.Score, []datastore.Score, []datastore.Score, error) {
	scores, err := store.GetScoresForGameID(game.ID)
	if err != nil {
		slog.Error("Error while getting scores for game", "Game", game, "Error", err)
		return "", []datastore.Score{}, []datastore.Score{}, []datastore.Score{}, err
	}

	scores = sortScoreArrayByScore(scores)
	channel, _ := session.Channel(game.ChannelID)

	scoreboard := fmt.Sprintf("## 1337erboard for <t:%d>\n", tt.Unix())

	earlyBirds := make([]datastore.Score, 0)
	winners := make([]datastore.Score, 0)
	zonks := make([]datastore.Score, 0)

	printHeader := true
	for _, score := range scores {
		if isScoreInScoreArray(score, winners) || len(winners) >= 3 {
			continue
		} else {
			if score.Score >= 0 {
				if printHeader {
					scoreboard += "### Top scorers\n"
					printHeader = false
				}
				winners = append(winners, score)

			}
		}
	}

	for i, winner := range winners {
		var award string
		if i == 0 {
			award = firstPlace
		} else if i == 1 {
			award = secondPlace
		} else if i == 2 {
			award = thirdPlace
		} else {
			award = otherPlace
		}

		player, err := store.GetPlayerByID(winner.PlayerID)
		if err != nil {
			return "", []datastore.Score{}, []datastore.Score{}, []datastore.Score{}, err
		}

		scoreboard += fmt.Sprintf("%s <@%s> with %d ms (https://discord.com/channels/%s/%s/%s)\n", award, player.UserID, winner.Score, channel.GuildID, game.ChannelID, winner.MessageID)
	}

	printHeader = true
	for _, score := range scores {
		if isScoreInScoreArray(score, zonks) || isScoreInScoreArray(score, winners) {
			continue
		} else {
			if score.Score > 0 {
				if printHeader {
					scoreboard += "### Zonks\n"
					printHeader = false
				}
				zonks = append(zonks, score)
			}
		}
	}

	for _, z := range zonks {
		player, err := store.GetPlayerByID(z.PlayerID)
		if err != nil {
			return "", []datastore.Score{}, []datastore.Score{}, []datastore.Score{}, err
		}
		scoreboard += fmt.Sprintf("%s <@%s> with %d ms (https://discord.com/channels/%s/%s/%s)\n", "😭", player.UserID, z.Score, channel.GuildID, game.ChannelID, z.MessageID)
	}

	printHeader = true
	for _, score := range scores {
		if isScoreInScoreArray(score, earlyBirds) {
			continue
		} else {
			if score.Score >= -5000 && score.Score < 0 {
				if printHeader {
					scoreboard += "### Honorlolable mentions\n"
					printHeader = false
				}
				earlyBirds = append(earlyBirds, score)
			}
		}
	}

	for _, earlyBird := range earlyBirds {
		player, err := store.GetPlayerByID(earlyBird.PlayerID)
		if err != nil {
			return "", []datastore.Score{}, []datastore.Score{}, []datastore.Score{}, err
		}
		var award string
		if isScoreInScoreArray(earlyBird, zonks) {
			award = "🫠"
		} else if isScoreInScoreArray(earlyBird, winners) {
			award = "😐"
		} else {
			award = "🤨"
		}

		scoreboard += fmt.Sprintf("%s <@%s> with %d ms (https://discord.com/channels/%s/%s/%s)\n", award, player.UserID, earlyBird.Score, channel.GuildID, game.ChannelID, earlyBird.MessageID)
	}

	// TODO: this "find highest score for current season" should be a function in datastore
	// var memScore datastore.Score = datastore.Score{Score: 999999999999999999}
	// season, err := store.GetSeasonByDate(time.Now())
	// if err != nil {
	// 	logrus.Errorln(err)
	// }
	// games, err := store.GetGames()
	// if err != nil {
	// 	logrus.Errorln(err)
	// }
	// for _, g := range games {
	// 	if g.SeasonID == season.ID {
	// 		scores, err := store.GetScoresForGameID(g.ID)
	// 		if err != nil {
	// 			logrus.Errorln(err)
	// 		}
	// 		for _, s := range scores {
	// 			if s.Score >= 0 && s.Score < memScore.Score {
	// 				memScore = s
	// 			}
	// 		}
	// 	}
	// }
	// player, err := store.GetPlayerByID(memScore.PlayerID)
	// if err != nil {
	// 	logrus.Errorln(err)
	// }

	// memScoreChannel, err := session.ChannelMessage(memScore.ChannelID, memScore.MessageID)
	// if err != nil {
	// 	logrus.Errorln(err)
	// }

	// scoreboard += fmt.Sprintf("### Current season highscore\n<@%s> with %d ms on <t:%d> (https://discord.com/channels/%s/%s/%s)\n", player.UserID, memScore.Score, memScore.CreatedAt.Unix(), channel.GuildID, ChannelID, memScore.MessageID)
	// scoreboard += fmt.Sprintf("\nCurrent season ends on <t:%d> (<t:%d:R>)\n", season.EndDate.Unix(), season.EndDate.Unix())

	return scoreboard, earlyBirds, winners, zonks, nil
}

func renewReactions(game datastore.Game) {
	for renewReactionsLock {
		time.Sleep(1 * time.Second)
	}

	renewReactionsLock = true

	_, earlybirds, winners, zonks, err := buildScoreboardForGame(game)
	if err != nil {
		slog.Error("Error while building scoreboard", "Error", err)
		return
	}

	for _, v := range earlybirds {
		go session.MessageReactionRemove(game.ChannelID, v.MessageID, lol, session.State.User.ID)       // nolint:errcheck
		go session.MessageReactionRemove(game.ChannelID, v.MessageID, notamused, session.State.User.ID) // nolint:errcheck
		go session.MessageReactionRemove(game.ChannelID, v.MessageID, wat, session.State.User.ID)       // nolint:errcheck
		if isScoreInScoreArray(v, zonks) {
			go session.MessageReactionAdd(game.ChannelID, v.MessageID, lol) // nolint:errcheck
		} else if isScoreInScoreArray(v, winners) {
			go session.MessageReactionAdd(game.ChannelID, v.MessageID, notamused) // nolint:errcheck
		} else {
			go session.MessageReactionAdd(game.ChannelID, v.MessageID, wat) // nolint:errcheck
		}
	}

	for i, v := range winners {
		go session.MessageReactionRemove(game.ChannelID, v.MessageID, firstPlace, session.State.User.ID)  // nolint:errcheck
		go session.MessageReactionRemove(game.ChannelID, v.MessageID, secondPlace, session.State.User.ID) // nolint:errcheck
		go session.MessageReactionRemove(game.ChannelID, v.MessageID, thirdPlace, session.State.User.ID)  // nolint:errcheck
		if i == 0 {
			go session.MessageReactionAdd(game.ChannelID, v.MessageID, firstPlace) // nolint:errcheck
		} else if i == 1 {
			go session.MessageReactionAdd(game.ChannelID, v.MessageID, secondPlace) // nolint:errcheck
		} else if i == 2 {
			go session.MessageReactionAdd(game.ChannelID, v.MessageID, thirdPlace) // nolint:errcheck
		}
	}

	for _, v := range zonks {
		go session.MessageReactionRemove(game.ChannelID, v.MessageID, zonk, session.State.User.ID) // nolint:errcheck
		go session.MessageReactionAdd(game.ChannelID, v.MessageID, zonk)                           // nolint:errcheck
	}

	renewReactionsLock = false
}

func announceTodaysWinners() {
	if isOnTargetTimeRange(time.Now(), true) {
		slog.Info("Announcing winners")
		winnerAnnounceLock = true
		time.Sleep(62 * time.Second)
		games, err := store.GetGamesByDate(time.Now())
		if err != nil {
			slog.Error("Error while getting games by date", "Error", err)
			return
		}
		for _, game := range games {
			scoreboard, _, _, _, err := buildScoreboardForGame(game)
			if err != nil {
				slog.Error("Error while building scoreboard", "Error", err)
				return
			}
			_, err = session.ChannelMessageSend(game.ChannelID, scoreboard)
			if err != nil {
				slog.Error("Error while sending scoreboard", "Error", err)
			}
		}
	}
	winnerAnnounceLock = false
	resetGameVars()
}

func resetGameVars() {
	playersWithClockReactions = []string{}
}

func gameTick() {
	for {
		if isOnTargetTimeRange(time.Now(), false) {
			time.Sleep(1 * time.Second)
		} else {
			if os.Getenv("LEETOCLOCK_DEBUG") != "" {
				time.Sleep(1 * time.Second)
			} else {
				time.Sleep(1 * time.Minute)
			}
			updateTTHelper()
		}
		if !preparationAnnounceLock {
			go announcePreparation()
		}
		if !winnerAnnounceLock {
			go announceTodaysWinners()
		}
	}
}

func updateTTHelper() {
	t := time.Now()
	tt = time.Date(t.Year(), t.Month(), t.Day(), tHourInt, tMinuteInt, 0, 0, t.Location())
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	message := m.Message
	messageTimestamp := util.GetTimestampOfMessage(message.ID)

	if isOnTargetTimeRange(messageTimestamp, false) {
		season, err := store.EnsureSeason(time.Now())
		if err != nil {
			slog.Error("Error while ensuring season", "Error", err)
		}
		game, err := store.EnsureGame(message.ChannelID, message.GuildID, tt, season.ID)
		if err != nil {
			slog.Error("Error while ensuring game", "Error", err)
		}
		player, err := store.EnsurePlayer(message.Author.ID)
		if err != nil {
			slog.Error("Error while ensuring player", "Error", err)
		}
		err = store.CreateScore(message.ID, player.ID, calculateScore(messageTimestamp), game.ID)
		if err != nil {
			slog.Error("Error while creating score", "Error", err)
		}

		if isOnTargetTimeRange(messageTimestamp, true) {
			hasPlayerClockReaction := func() bool {
				for _, v := range playersWithClockReactions {
					if v == message.Author.ID {
						return true
					}
				}
				return false
			}
			if !hasPlayerClockReaction() {
				go s.MessageReactionAdd(m.ChannelID, m.ID, "⏰") // nolint:errcheck
				playersWithClockReactions = append(playersWithClockReactions, message.Author.ID)
			}
		}
		go renewReactions(*game)
	}
}
