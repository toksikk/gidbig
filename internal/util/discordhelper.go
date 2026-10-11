package util

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

var mentionRe = regexp.MustCompile(`<@!?(\d+)>`)

type reactionItem struct {
	session      *discordgo.Session
	channelid    string
	messageid    string
	emoji        string
	reactionType string
}

var reactionItemChannel chan (reactionItem)

// guildMembersPageSize is the largest page the List Guild Members endpoint accepts.
const guildMembersPageSize = 1000

// listGuildMembers fetches every member of a guild over REST, paging with the
// `after` cursor. Unlike GET /guilds/{id}, this endpoint returns members, but it
// requires the Server Members Intent to be enabled in the Developer Portal.
func listGuildMembers(discordSession *discordgo.Session, guildID string) ([]*discordgo.Member, error) {
	all := []*discordgo.Member{}
	after := ""
	for {
		page, err := discordSession.GuildMembers(guildID, after, guildMembersPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < guildMembersPageSize {
			break
		}
		last := page[len(page)-1]
		if last == nil || last.User == nil || last.User.ID == after {
			break
		}
		after = last.User.ID
	}
	return all, nil
}

// viewableMemberNames returns the display names of members whose permissions in
// the channel include ViewChannel, preserving member order. A permission lookup
// that fails skips that member instead of aborting the whole list.
func viewableMemberNames(members []*discordgo.Member, permissions func(userID string) (int64, error)) []string {
	names := make([]string, 0, len(members))
	for _, member := range members {
		if member == nil || member.User == nil {
			continue
		}
		perms, err := permissions(member.User.ID)
		if err != nil {
			slog.Warn("Could not get permissions of user for channel", "userID", member.User.ID, "error", err)
			continue
		}
		if perms&discordgo.PermissionViewChannel == 0 {
			continue
		}
		names = append(names, member.DisplayName())
	}
	return names
}

// GetAllMembersOfChannelAsString retrieves the members who can view a channel
// and returns their display names as a comma-separated string.
//
// It lists the guild's members over REST (List Guild Members requires the Server
// Members Intent) and keeps only those with ViewChannel. It returns an error
// when the channel or member list cannot be fetched, so callers can tell an
// empty channel apart from a failed lookup.
func GetAllMembersOfChannelAsString(discordSession *discordgo.Session, channelID string) (string, error) {
	channel, err := discordSession.Channel(channelID)
	if err != nil {
		slog.Error("Could not retrieve Channel", "channelID", channelID, "error", err)
		return "", fmt.Errorf("get channel %s: %w", channelID, err)
	}
	members, err := listGuildMembers(discordSession, channel.GuildID)
	if err != nil {
		slog.Error("Could not retrieve Guild members", "guildID", channel.GuildID, "error", err)
		return "", fmt.Errorf("list members of guild %s: %w", channel.GuildID, err)
	}
	return strings.Join(viewableMemberNames(members, func(userID string) (int64, error) {
		return discordSession.State.UserChannelPermissions(userID, channelID)
	}), ", "), nil
}

// GetChannelName returns the name of a channel
func GetChannelName(discordSession *discordgo.Session, channelID string) string {
	channel, err := discordSession.Channel(channelID)
	if err != nil {
		slog.Warn("Error while getting channel", "error", err, "channel_id", channelID)
		return channelID
	}
	return channel.Name
}

// GetGuildName returns the name of a guild
func GetGuildName(discordSession *discordgo.Session, guildID string) string {
	guild, err := discordSession.Guild(guildID)
	if err != nil {
		slog.Warn("Error while getting guild", "error", err, "guild_id", guildID)
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
		slog.Warn("Error while getting member", "error", err, "guild_id", m.GuildID, "user_id", m.Author.ID)
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
	slog.Warn("Error while getting member", "error", err, "guild_id", guildid, "user_id", userid)
	user, err := discordSession.User(userid)
	if err == nil {
		return user.Username
	}
	slog.Warn("Error while getting user", "error", err, "user_id", userid)
	return "Unbekannter Benutzer"
}

// ResolveMentions replaces Discord mention tokens (<@ID> / <@!ID>) in text with
// the display name of the referenced user. Falls back to GetUsernameForUserIDInGuild
// for unknown users. Returns the original text unchanged when there are no mentions.
func ResolveMentions(session *discordgo.Session, guildID, text string) string {
	resolved, _ := ResolveMentionsWithRestore(session, guildID, text)
	return resolved
}

// ResolveMentionsWithRestore is like ResolveMentions but also returns a restore
// function that replaces display names in a generated string back to their original
// Discord mention tokens. Use it when the resolved text is sent to an LLM and the
// output should contain real pings rather than plain names.
func ResolveMentionsWithRestore(session *discordgo.Session, guildID, text string) (string, func(string) string) {
	matches := mentionRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text, func(s string) string { return s }
	}
	tokenToName := make(map[string]string, len(matches))
	nameToToken := make(map[string]string, len(matches))
	for _, m := range matches {
		full, id := m[0], m[1]
		if _, seen := tokenToName[full]; seen {
			continue
		}
		name := GetUsernameForUserIDInGuild(session, id, guildID)
		tokenToName[full] = name
		nameToToken[name] = full
	}
	resolved := text
	for token, name := range tokenToName {
		resolved = strings.ReplaceAll(resolved, token, name)
	}
	restore := func(generated string) string {
		for name, token := range nameToToken {
			generated = strings.ReplaceAll(generated, name, token)
		}
		return generated
	}
	return resolved, restore
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
			reaction.session.MessageReactionAdd(reaction.channelid, reaction.messageid, reaction.emoji) // nolint:errcheck
		case "remove":
			reaction.session.MessageReactionRemove(reaction.channelid, reaction.messageid, reaction.emoji, reaction.session.State.User.ID) // nolint:errcheck
		}
	}
}
