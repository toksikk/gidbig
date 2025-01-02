package util

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
)

type reactionItem struct {
	session      *discordgo.Session
	channelid    string
	messageid    string
	emoji        string
	reactionType string
}

var reactionItemChannel chan (reactionItem)

// GetChannelName returns the name of a channel
func GetChannelName(discordSession *discordgo.Session, channelID string) string {
	channel, err := discordSession.Channel(channelID)
	if err != nil {
		slog.Warn("Error while getting channel", "error", err)
		return channelID
	}
	return channel.Name
}

// GetGuildName returns the name of a guild
func GetGuildName(discordSession *discordgo.Session, guildID string) string {
	guild, err := discordSession.Guild(guildID)
	if err != nil {
		slog.Warn("Error while getting guild", "error", err)
		return guildID
	}
	return guild.Name
}

func idToTimestamp(id string) (int64, error) {
	convertedID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return -1, err
	}
	convertedIDString := strconv.FormatInt(convertedID, 2)
	m := 64 - len(convertedIDString)
	unixbin := convertedIDString[0 : 42-m]
	unix, err := strconv.ParseInt(unixbin, 2, 64)
	if err != nil {
		return -1, err
	}
	return unix + 1420070400000, nil
}

// GetTimestampOfMessage returns the timestamp of a message
func GetTimestampOfMessage(messageID string) time.Time {
	timestamp, err := idToTimestamp(messageID)
	if err != nil {
		slog.Error("Error while converting messageID to timestamp", "Error", err)
		return time.Time{}
	}
	return time.UnixMilli(timestamp)
}

// GetBotDisplayName returns the display name of the bot
func GetBotDisplayName(m *discordgo.MessageCreate, discordSession *discordgo.Session) string {
	botDisplayNames := getBotDisplayNames(discordSession)
	if botDisplayNames[m.GuildID] == "" {
		return "Gidbig"
	}
	return botDisplayNames[m.GuildID]
}

func getBotDisplayNames(discordSession *discordgo.Session) map[string]string {
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

// GetUsernameInGuild returns the username of a user in a guild
func GetUsernameInGuild(discordSession *discordgo.Session, m *discordgo.MessageCreate) string {
	member, err := discordSession.GuildMember(m.GuildID, m.Author.ID)
	if err != nil {
		slog.Warn("Error while getting member", "error", err)
		return m.Author.Username
	}
	return member.Nick
}

// GetUsernameForUserIDInGuild returns the username of a user in a guild
func GetUsernameForUserIDInGuild(discordSession *discordgo.Session, userid string, guildid string) string {
	member, err := discordSession.GuildMember(guildid, userid)
	if err == nil {
		if member.Nick != "" {
			return member.Nick
		}
		return member.User.Username
	}
	slog.Warn("Error while getting member", "error", err)
	user, err := discordSession.User(userid)
	if err == nil {
		return user.Username
	}
	slog.Warn("Error while getting user", "error", err)
	return "Unbekannter Benutzer"
}

// ReactOnMessage reacts to a message with an emoji concurrently
func ReactOnMessage(session *discordgo.Session, channelid string, messageid string, emoji string, reactionType string) {
	if reactionItemChannel == nil {
		reactionItemChannel = make(chan reactionItem)
		go messageReactionWorkerLoop()
	}
	switch reactionType {
	case "add":
		reactionItemChannel <- reactionItem{session: session, channelid: channelid, messageid: messageid, emoji: emoji, reactionType: reactionType}
	case "remove":
		reactionItemChannel <- reactionItem{session: session, channelid: channelid, messageid: messageid, emoji: emoji, reactionType: reactionType}
	}
}

func messageReactionWorkerLoop() {
	for {
		reaction := <-reactionItemChannel
		switch reaction.reactionType {
		case "add":
			go reaction.session.MessageReactionAdd(reaction.channelid, reaction.messageid, reaction.emoji) // nolint:errcheck
		case "remove":
			go reaction.session.MessageReactionRemove(reaction.channelid, reaction.messageid, reaction.emoji, reaction.session.State.User.ID) // nolint:errcheck
		}
	}
}
