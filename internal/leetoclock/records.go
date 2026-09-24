package leetoclock

import (
	"errors"
	"fmt"
	"log/slog"
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
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "top", Description: "Best players in this server", Options: []*discordgo.ApplicationCommandOption{period(), visibility()}},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "player", Description: "One player's best records", Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Player (default: you)"}, period(),
				{Type: discordgo.ApplicationCommandOptionString, Name: "scope", Description: "Record scope (default: this server)", Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "This server", Value: "server"}, {Name: "All servers", Value: "global"},
				}}, visibility(),
			}},
		},
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
			if !ok || r.subcommand != "player" || (value != "server" && value != "global") {
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
		records, err := m.store.TopPlayers(guildID, r.period, m.now())
		if err != nil {
			return "", err
		}
		return renderTop(guildID, r.periodName, records), nil
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
		return renderPlayer(r, records, count, errors.Is(err, gorm.ErrRecordNotFound)), nil
	}
	return renderPlayer(r, records, count, false), nil
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

func renderTop(guildID, period string, records []datastore.ScoreRecord) string {
	body := fmt.Sprintf("**Leet o'Clock top · server %s · %s**\n", guildID, period)
	if len(records) == 0 {
		return boundedRecords(body + "No valid scores in this period.")
	}
	for idx, record := range records {
		body = appendRecordLine(body, fmt.Sprintf("%d. <@%s> — %d ms · <t:%d:d>%s\n", idx+1, record.UserID, record.Score, record.GameDate.Unix(), recordLink(record)))
	}
	return boundedRecords(body)
}

func renderPlayer(r recordRequest, records []datastore.ScoreRecord, count int64, missing bool) string {
	body := fmt.Sprintf("**Leet o'Clock · <@%s> · %s · %s**\nValid attempts: %d\n", r.userID, r.scope, r.periodName, count)
	if missing {
		return boundedRecords(body + "No stored player found.")
	}
	if count == 0 {
		return boundedRecords(body + "No valid scores in this period and scope.")
	}
	for idx, record := range records {
		line := fmt.Sprintf("%d. %d ms · <t:%d:d>", idx+1, record.Score, record.GameDate.Unix())
		if r.scope == "global" {
			line += " · server " + record.GuildID
		} else {
			line += recordLink(record)
		}
		body = appendRecordLine(body, line+"\n")
	}
	return boundedRecords(body)
}

func recordLink(record datastore.ScoreRecord) string {
	if record.GuildID == "" || record.ChannelID == "" || record.MessageID == "" {
		return ""
	}
	return fmt.Sprintf(" · [message](https://discord.com/channels/%s/%s/%s)", record.GuildID, record.ChannelID, record.MessageID)
}
