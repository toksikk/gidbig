package leetoclock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
	"gorm.io/gorm"
)

const maxRecordResponse = 2000

var recordNoMentions = &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}

func recordCommands() []*discordgo.ApplicationCommand {
	period := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type: discordgo.ApplicationCommandOptionString, Name: "period", Description: "Game date range (default: current month)",
			Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "Current month", Value: "month"}, {Name: "Last 7 days", Value: "7d"},
				{Name: "Last 30 days", Value: "30d"}, {Name: "All time", Value: "all"},
			},
		}
	}
	visibility := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionBoolean, Name: "public", Description: "Share in this channel (default: private)"}
	}
	return []*discordgo.ApplicationCommand{{
		Name: "leetoclock", Description: "View Leet o'Clock records", DMPermission: new(false),
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "top", Description: "Best players in this server or globally", Options: []*discordgo.ApplicationCommandOption{period(), scopeOption(), visibility()}},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "player", Description: "One player's best records", Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Player (default: you)"}, period(),
				scopeOption(), visibility(),
			}},
		},
	}}
}

func scopeOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: "scope", Description: "Record scope (default: this server)", Choices: []*discordgo.ApplicationCommandOptionChoice{
		{Name: "This server", Value: "server"}, {Name: "All servers", Value: "global"},
	}}
}

type recordRequest struct {
	subcommand string
	period     datastore.Period
	periodName string
	scope      string
	userID     string
	public     bool
}

func parseRecordRequest(i *discordgo.InteractionCreate) (recordRequest, error) {
	r := recordRequest{period: datastore.PeriodMonth, periodName: "month", scope: "server"}
	data := i.ApplicationCommandData()
	if len(data.Options) != 1 || data.Options[0] == nil {
		return r, fmt.Errorf("select top or player")
	}
	sub := data.Options[0]
	r.subcommand = sub.Name
	if sub.Type != discordgo.ApplicationCommandOptionSubCommand || (sub.Name != "top" && sub.Name != "player") {
		return r, fmt.Errorf("select top or player")
	}
	for _, opt := range sub.Options {
		if opt == nil {
			return r, fmt.Errorf("invalid option")
		}
		switch opt.Name {
		case "period":
			value, ok := opt.Value.(string)
			if !ok {
				return r, fmt.Errorf("invalid period")
			}
			r.periodName = value
			switch value {
			case "month":
				r.period = datastore.PeriodMonth
			case "7d":
				r.period = datastore.PeriodLast7Days
			case "30d":
				r.period = datastore.PeriodLast30Days
			case "all":
				r.period = datastore.PeriodAllTime
			default:
				return r, fmt.Errorf("invalid period")
			}
		case "public":
			value, ok := opt.Value.(bool)
			if !ok {
				return r, fmt.Errorf("invalid visibility")
			}
			r.public = value
		case "scope":
			value, ok := opt.Value.(string)
			if !ok || (value != "server" && value != "global") {
				return r, fmt.Errorf("invalid scope")
			}
			r.scope = value
		case "user":
			value, ok := opt.Value.(string)
			if !ok || value == "" || r.subcommand != "player" {
				return r, fmt.Errorf("invalid user")
			}
			r.userID = value
		default:
			return r, fmt.Errorf("invalid option")
		}
	}
	if r.subcommand == "player" && r.userID == "" {
		if i.Member != nil && i.Member.User != nil {
			r.userID = i.Member.User.ID
		} else if i.User != nil {
			r.userID = i.User.ID
		}
		if r.userID == "" {
			return r, fmt.Errorf("missing user")
		}
	}
	return r, nil
}

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil || i.Type != discordgo.InteractionApplicationCommand || i.ApplicationCommandData().Name != "leetoclock" {
		return
	}
	if !m.beginHandler() {
		return
	}
	defer m.handlerWG.Done()
	if s == nil {
		return
	}
	if i.GuildID == "" {
		respondRecordsError(s, i, "Use this command in a server.")
		return
	}
	r, err := parseRecordRequest(i)
	if err != nil {
		respondRecordsError(s, i, "Invalid record options. Choose `/leetoclock top` or `/leetoclock player`.")
		return
	}
	flags := discordgo.MessageFlags(0)
	if !r.public {
		flags = discordgo.MessageFlagsEphemeral
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: flags, AllowedMentions: recordNoMentions},
	}); err != nil {
		slog.Error("leetoclock: defer records", "error", err)
		return
	}
	body, err := m.recordResponse(i.GuildID, r)
	if err != nil {
		slog.Error("leetoclock: query records", "error", err)
		body = "Could not load records right now. Try again later."
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &body, AllowedMentions: recordNoMentions}); err != nil {
		slog.Error("leetoclock: edit records", "error", err)
	}
}

func respondRecordsError(s *discordgo.Session, i *discordgo.InteractionCreate, text string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: text, Flags: discordgo.MessageFlagsEphemeral, AllowedMentions: recordNoMentions},
	}); err != nil {
		slog.Error("leetoclock: respond with error", "error", err)
	}
}

func (m *Module) recordResponse(guildID string, r recordRequest) (string, error) {
	if m.store == nil {
		return "", fmt.Errorf("store unavailable")
	}
	if r.subcommand == "top" {
		queryGuild := guildID
		if r.scope == "global" {
			queryGuild = ""
		}
		records, err := m.store.TopPlayers(queryGuild, r.period, m.now())
		if err != nil {
			return "", err
		}
		return renderTop(r, records, m.serverName, m.playerDisplayName), nil
	}
	guildScope := guildID
	if r.scope == "global" {
		guildScope = ""
	}
	records, count, err := m.store.PlayerRecords(r.userID, guildScope, r.period, m.now())
	if err != nil {
		return "", err
	}
	if count == 0 {
		_, err = m.store.GetPlayerByUserID(r.userID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
		return renderPlayer(r, records, count, errors.Is(err, gorm.ErrRecordNotFound), m.serverName), nil
	}
	return renderPlayer(r, records, count, false, m.serverName), nil
}

// Only use guild names already available in the session state; global lookups
// must not fetch inaccessible guild details or show internal IDs.
func (m *Module) serverName(guildID string) string {
	if m.session != nil && m.session.State != nil {
		if guild, err := m.session.State.Guild(guildID); err == nil && guild != nil {
			return safeServerName(guild.Name)
		}
	}
	return "Unknown server"
}

func safeServerName(name string) string {
	name = strings.NewReplacer("@", "＠", "`", "'", "*", "", "_", "", "\n", " ", "\r", " ").Replace(name)
	if len(name) > 60 {
		name = name[:60]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	if name == "" {
		return "Unknown server"
	}
	return name
}

// Public leaderboards use display names instead of Discord mentions. Gateway
// member caches are often empty; resolve missing users through Discord REST
// after the interaction has been deferred.
func (m *Module) playerDisplayName(guildID, userID string) string {
	if m.session == nil {
		return ""
	}
	if m.session.State != nil {
		member, err := m.session.State.Member(guildID, userID)
		if err == nil && member != nil {
			if name := safePlayerName(member.Nick); name != "" {
				return name
			}
			if member.User != nil {
				if name := safePlayerName(member.User.DisplayName()); name != "" {
					return name
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	user, err := m.session.User(userID, discordgo.WithContext(ctx), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		slog.Warn("leetoclock: resolve leaderboard player", "error", err)
		return ""
	}
	if user != nil {
		return safePlayerName(user.DisplayName())
	}
	return ""
}

func safePlayerName(name string) string {
	name = strings.NewReplacer("@", "＠", "<", "‹", ">", "›", "`", "'", "*", "", "_", "", "[", "", "]", "", "\n", " ", "\r", " ").Replace(name)
	if len(name) > 60 {
		name = name[:60]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return strings.TrimSpace(name)
}

func appendRecordLine(body string, line string) string {
	if len(body)+len(line) > maxRecordResponse {
		return body
	}
	return body + line
}

func boundedRecords(body string) string {
	if len(body) <= maxRecordResponse {
		return body
	}
	body = body[:maxRecordResponse]
	for !utf8.ValidString(body) {
		body = body[:len(body)-1]
	}
	return body
}

func renderTop(r recordRequest, records []datastore.ScoreRecord, serverName func(string) string, playerName func(string, string) string) string {
	scope := "current server"
	if r.scope == "global" {
		scope = "global"
	}
	body := fmt.Sprintf("**Leet o'Clock top · %s · %s**\n", scope, r.periodName)
	if len(records) == 0 {
		return boundedRecords(body + "No valid scores in this period.")
	}
	for idx, record := range records {
		name := fmt.Sprintf("<@%s>", record.UserID)
		if r.public {
			name = playerName(record.GuildID, record.UserID)
			if name == "" {
				name = "User ID " + safePlayerName(record.UserID)
			}
		}
		line := fmt.Sprintf("%d. %s — %d ms · %s", idx+1, name, record.Score, recordDate(record))
		if r.scope == "global" {
			line += " · " + serverName(record.GuildID)
		} else {
			line += recordLink(record)
		}
		body = appendRecordLine(body, line+"\n")
	}
	return boundedRecords(body)
}

func renderPlayer(r recordRequest, records []datastore.ScoreRecord, count int64, missing bool, serverName func(string) string) string {
	scope := "current server"
	if r.scope == "global" {
		scope = "global"
	}
	body := fmt.Sprintf("**Leet o'Clock · <@%s> · %s · %s**\nValid attempts: %d\n", r.userID, scope, r.periodName, count)
	if missing {
		return boundedRecords(body + "No stored player found.")
	}
	if count == 0 {
		return boundedRecords(body + "No valid scores in this period and scope.")
	}
	for idx, record := range records {
		line := fmt.Sprintf("%d. %d ms · %s", idx+1, record.Score, recordDate(record))
		if r.scope == "global" {
			line += " · " + serverName(record.GuildID)
		} else {
			line += recordLink(record)
		}
		body = appendRecordLine(body, line+"\n")
	}
	return boundedRecords(body)
}

// Some older stored games have an invalid date. Recover the date from the
// scored Discord message when possible instead of displaying a 1970 timestamp.
func recordDate(record datastore.ScoreRecord) string {
	date := record.GameDate
	if date.Before(time.Date(2015, time.January, 1, 0, 0, 0, 0, time.UTC)) || date.After(time.Now().Add(24*time.Hour)) {
		if id, err := strconv.ParseUint(record.MessageID, 10, 64); err == nil && id >= 1<<22 {
			if recovered, err := discordgo.SnowflakeTimestamp(record.MessageID); err == nil {
				date = recovered
			}
		}
	}
	if date.Before(time.Date(2015, time.January, 1, 0, 0, 0, 0, time.UTC)) || date.After(time.Now().Add(24*time.Hour)) {
		return "date unavailable"
	}
	return fmt.Sprintf("<t:%d:d>", date.Unix())
}

func recordLink(record datastore.ScoreRecord) string {
	if record.GuildID == "" || record.ChannelID == "" || record.MessageID == "" {
		return ""
	}
	return fmt.Sprintf(" · [message](https://discord.com/channels/%s/%s/%s)", record.GuildID, record.ChannelID, record.MessageID)
}
