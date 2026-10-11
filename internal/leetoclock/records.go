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
				{Name: "Current month", Value: "month"}, {Name: "Today", Value: "today"},
				{Name: "Yesterday", Value: "yesterday"}, {Name: "This week", Value: "week"},
				{Name: "Last 7 days", Value: "7d"}, {Name: "Last 30 days", Value: "30d"},
				{Name: "This year", Value: "year"}, {Name: "All time", Value: "all"},
			},
		}
	}
	visibility := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionBoolean, Name: "public", Description: "Share in this channel (default: private)"}
	}
	date := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type: discordgo.ApplicationCommandOptionString, Name: "date",
			Description: "One game date, YYYY-MM-DD (overrides period)",
		}
	}
	return []*discordgo.ApplicationCommand{{
		Name: "leetoclock", Description: "View Leet o'Clock records", DMPermission: new(false),
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "top", Description: "Best players in this server or globally", Options: []*discordgo.ApplicationCommandOption{period(), date(), scopeOption(), visibility()}},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "player", Description: "One player's best records", Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Player (default: you)"}, period(), date(),
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
	// day is an explicit game date. When non-zero it overrides period and
	// bounds the query to that one local day.
	day time.Time
	// from and to bound an explicit inclusive local-day range. Either side may
	// be zero to leave that bound open; when set they override period.
	from time.Time
	to   time.Time
}

// periodNames maps a resolved period to the label shown in record headers. The
// label is also the value accepted from the slash command and the LLM tool.
var periodNames = map[datastore.Period]string{
	datastore.PeriodMonth:      "month",
	datastore.PeriodToday:      "today",
	datastore.PeriodYesterday:  "yesterday",
	datastore.PeriodWeek:       "week",
	datastore.PeriodLast7Days:  "7d",
	datastore.PeriodLast30Days: "30d",
	datastore.PeriodYear:       "year",
	datastore.PeriodAllTime:    "all",
}

// parsePeriod resolves a slash-command or tool period value. An empty value
// means the current calendar month.
func parsePeriod(value string) (datastore.Period, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "month":
		return datastore.PeriodMonth, nil
	case "today":
		return datastore.PeriodToday, nil
	case "yesterday":
		return datastore.PeriodYesterday, nil
	case "week":
		return datastore.PeriodWeek, nil
	case "7d":
		return datastore.PeriodLast7Days, nil
	case "30d":
		return datastore.PeriodLast30Days, nil
	case "year":
		return datastore.PeriodYear, nil
	case "all":
		return datastore.PeriodAllTime, nil
	default:
		return "", fmt.Errorf("invalid period")
	}
}

// parseRecordDay parses a YYYY-MM-DD game date in the bot's local timezone. An
// empty value yields the zero time, meaning "no explicit date".
func parseRecordDay(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	day, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q must use YYYY-MM-DD", value)
	}
	return day, nil
}

// recordRangeName labels an explicit day range for record headers. A single
// day collapses to that date; one-sided ranges read as "from ..." or
// "until ...".
func recordRangeName(from, to time.Time) string {
	if !from.IsZero() && !to.IsZero() {
		if from.Equal(to) {
			return from.Format("2006-01-02")
		}
		return from.Format("2006-01-02") + " – " + to.Format("2006-01-02")
	}
	if !from.IsZero() {
		return "from " + from.Format("2006-01-02")
	}
	return "until " + to.Format("2006-01-02")
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
			period, err := parsePeriod(value)
			if err != nil {
				return r, fmt.Errorf("invalid period")
			}
			r.period, r.periodName = period, periodNames[period]
		case "date":
			value, ok := opt.Value.(string)
			if !ok {
				return r, fmt.Errorf("invalid date")
			}
			day, err := parseRecordDay(value)
			if err != nil {
				return r, fmt.Errorf("invalid date")
			}
			r.day = day
			if !day.IsZero() {
				r.periodName = day.Format("2006-01-02")
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
	queryGuild := guildID
	if r.scope == "global" {
		queryGuild = ""
	}
	if r.subcommand == "top" {
		records, err := m.topRecords(queryGuild, r)
		if err != nil {
			return "", err
		}
		return renderTop(r, records, m.serverName, m.playerDisplayName), nil
	}
	records, count, err := m.playerRecords(r.userID, queryGuild, r)
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

// recordWindow resolves an explicit date or day range into a half-open window.
// ok is false when neither is set, so the period presets should apply instead.
// An open `to` side extends to now, because the store cannot express an open
// upper bound.
func (m *Module) recordWindow(r recordRequest) (time.Time, time.Time, bool) {
	if !r.day.IsZero() {
		start, end := datastore.DayRange(r.day, r.day)
		return start, end, true
	}
	if !r.from.IsZero() || !r.to.IsZero() {
		to := r.to
		if to.IsZero() {
			to = m.now()
		}
		start, end := datastore.DayRange(r.from, to)
		return start, end, true
	}
	return time.Time{}, time.Time{}, false
}

// topRecords resolves the requested window for a leaderboard query. An explicit
// date or range wins over the period, so a date-specific question is not
// answered with a neighbouring period's numbers.
func (m *Module) topRecords(guildID string, r recordRequest) ([]datastore.ScoreRecord, error) {
	if start, end, ok := m.recordWindow(r); ok {
		return m.store.TopPlayersBetween(guildID, start, end)
	}
	return m.store.TopPlayers(guildID, r.period, m.now())
}

// playerRecords is topRecords for one player's records plus their attempt count.
func (m *Module) playerRecords(userID, guildID string, r recordRequest) ([]datastore.ScoreRecord, int64, error) {
	if start, end, ok := m.recordWindow(r); ok {
		return m.store.PlayerRecordsBetween(userID, guildID, start, end)
	}
	return m.store.PlayerRecords(userID, guildID, r.period, m.now())
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
