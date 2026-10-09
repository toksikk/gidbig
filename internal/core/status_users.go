package gidbig

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	humanize "github.com/dustin/go-humanize"
)

const (
	statusUsersTimeout  = 30 * time.Second
	guildMembersPerPage = 1000
	statusUsersCSVName  = "gidbig-users.csv"
	membersIntentHint   = "member list unavailable: enable \"Server Members Intent\" for the bot in the Discord Developer Portal"
)

// guildMembersFunc lists one page of guild members after the given user ID.
type guildMembersFunc func(ctx context.Context, guildID, after string, limit int) ([]*discordgo.Member, error)

// guildUsersResult is the outcome of fetching one guild's member list.
type guildUsersResult struct {
	Name    string
	Members int
	Humans  int
	Bots    int
	Err     error
}

// userAccess is a user and the guilds in which they share the bot.
type userAccess struct {
	ID          string
	Username    string
	DisplayName string
	Bot         bool
	Guilds      []string
}

type usersReport struct {
	Header []string
	Guilds []guildUsersResult
	Users  []userAccess
}

func sessionGuildMembers(s *discordgo.Session) guildMembersFunc {
	return func(ctx context.Context, guildID, after string, limit int) ([]*discordgo.Member, error) {
		return s.GuildMembers(guildID, after, limit, discordgo.WithContext(ctx))
	}
}

// collectUsers fetches every member of every guild via REST. This requires the
// Server Members Intent to be enabled in the Developer Portal but does not
// change the gateway connection or keep members in memory.
func collectUsers(ctx context.Context, guilds []guildStatus, list guildMembersFunc) ([]guildUsersResult, []userAccess) {
	results := make([]guildUsersResult, 0, len(guilds))
	users := map[string]*userAccess{}

	for _, g := range guilds {
		res := guildUsersResult{Name: g.Name}
		after := ""
		for {
			prev := after
			page, err := list(ctx, g.ID, after, guildMembersPerPage)
			if err != nil {
				res.Err = err
				break
			}
			for _, m := range page {
				if m == nil || m.User == nil {
					continue
				}
				res.Members++
				if m.User.Bot {
					res.Bots++
				} else {
					res.Humans++
				}
				u, ok := users[m.User.ID]
				if !ok {
					u = &userAccess{ID: m.User.ID, Username: m.User.Username, DisplayName: m.User.GlobalName, Bot: m.User.Bot}
					users[m.User.ID] = u
				}
				u.Guilds = append(u.Guilds, g.Name)
				after = m.User.ID
			}
			if len(page) < guildMembersPerPage || after == prev {
				break
			}
		}
		results = append(results, res)
	}

	out := make([]userAccess, 0, len(users))
	for _, u := range users {
		sort.Strings(u.Guilds)
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bot != out[j].Bot {
			return !out[i].Bot
		}
		if len(out[i].Guilds) != len(out[j].Guilds) {
			return len(out[i].Guilds) > len(out[j].Guilds)
		}
		return strings.ToLower(out[i].Username) < strings.ToLower(out[j].Username)
	})
	return results, out
}

func isMissingMembersIntent(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) {
		return false
	}
	if restErr.Message != nil && restErr.Message.Code == discordgo.ErrCodeMissingAccess {
		return true
	}
	return restErr.Response != nil && restErr.Response.StatusCode == http.StatusForbidden
}

func (u userAccess) label() string {
	name := u.Username
	if u.DisplayName != "" && u.DisplayName != u.Username {
		name = u.DisplayName + " (" + u.Username + ")"
	}
	if u.Bot {
		name += " [bot]"
	}
	return name
}

func (r usersReport) totals() (humans, bots int) {
	for _, u := range r.Users {
		if u.Bot {
			bots++
		} else {
			humans++
		}
	}
	return humans, bots
}

func (r usersReport) warnings() []string {
	var out []string
	intentHinted := false
	for _, g := range r.Guilds {
		if g.Err == nil {
			continue
		}
		if isMissingMembersIntent(g.Err) {
			if !intentHinted {
				out = append(out, membersIntentHint)
				intentHinted = true
			}
			continue
		}
		out = append(out, truncateRunes(g.Name+": "+g.Err.Error(), statusWarningMaxRunes))
	}
	return out
}

func (r usersReport) guildLines() []string {
	lines := make([]string, 0, len(r.Guilds))
	for _, g := range r.Guilds {
		if g.Err != nil {
			lines = append(lines, fmt.Sprintf("%-24s n/a", truncateRunes(g.Name, 24)))
			continue
		}
		lines = append(lines, fmt.Sprintf("%-24s %6s users · %s bots", truncateRunes(g.Name, 24), humanize.Comma(int64(g.Humans)), humanize.Comma(int64(g.Bots))))
	}
	return lines
}

func (r usersReport) userLines() []string {
	lines := make([]string, 0, len(r.Users))
	for _, u := range r.Users {
		lines = append(lines, truncateRunes(fmt.Sprintf("%-28s %s", truncateRunes(u.label(), 28), strings.Join(u.Guilds, ", ")), 100))
	}
	return lines
}

func (r usersReport) usersTitle() string {
	humans, bots := r.totals()
	return fmt.Sprintf("Users (%d + %d bots)", humans, bots)
}

func renderUsersText(r usersReport, limit int) string {
	sections := []textSection{
		{title: fmt.Sprintf("Guilds (%d)", len(r.Guilds)), lines: r.guildLines(), trimmable: true},
		{title: r.usersTitle(), lines: r.userLines(), trimmable: true},
	}
	if w := r.warnings(); len(w) > 0 {
		for i := range w {
			w[i] = "! " + w[i]
		}
		sections = append(sections, textSection{title: "Warnings", lines: w})
	}
	return fitSections(r.Header, sections, limit)
}

func usersCSV(users []userAccess) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"user_id", "username", "display_name", "bot", "guild_count", "guilds"})
	for _, u := range users {
		_ = w.Write([]string{u.ID, u.Username, u.DisplayName, strconv.FormatBool(u.Bot), strconv.Itoa(len(u.Guilds)), strings.Join(u.Guilds, "; ")})
	}
	w.Flush()
	return buf.Bytes()
}

func usersWebhookEdit(r usersReport, format string) *discordgo.WebhookEdit {
	edit := &discordgo.WebhookEdit{}
	if len(r.Users) > 0 {
		edit.Files = []*discordgo.File{{
			Name:        statusUsersCSVName,
			ContentType: "text/csv",
			Reader:      bytes.NewReader(usersCSV(r.Users)),
		}}
	}

	if format != statusFormatEmbed {
		const fence = "```"
		content := fence + renderUsersText(r, discordMessageLimit-2*len(fence)) + fence
		edit.Content = &content
		return edit
	}

	warnings := r.warnings()
	color := statusEmbedColorOK
	if len(warnings) > 0 {
		color = statusEmbedColorWarn
	}
	fields := []*discordgo.MessageEmbedField{
		{Name: fmt.Sprintf("Guilds (%d)", len(r.Guilds)), Value: embedValue(r.guildLines())},
		{Name: r.usersTitle(), Value: embedValue(r.userLines())},
	}
	if len(warnings) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Warnings", Value: embedValue(warnings)})
	}
	footer := "users"
	if len(r.Users) > 0 {
		footer = "full list attached as " + statusUsersCSVName
	}
	empty := ""
	edit.Content = &empty
	edit.Embeds = &[]*discordgo.MessageEmbed{{
		Title:       "gidbig users",
		Description: strings.Join(r.Header, "\n"),
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: footer},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}}
	return edit
}

func buildUsersReport(ctx context.Context, s *discordgo.Session, list guildMembersFunc) usersReport {
	snap := collectStatus(ctx, s, nil, nil)
	ctx, cancel := context.WithTimeout(ctx, statusUsersTimeout)
	defer cancel()
	guilds, users := collectUsers(ctx, snap.GuildList, list)
	return usersReport{Header: snap.headerLines(), Guilds: guilds, Users: users}
}

// respondStatusUsers defers the interaction, fetches members, then edits the
// deferred reply with the report and a CSV attachment.
func respondStatusUsers(s *discordgo.Session, i *discordgo.InteractionCreate, format string) {
	if !deferInteraction(s, i) {
		return
	}
	go func() {
		report := buildUsersReport(context.Background(), s, sessionGuildMembers(s))
		if _, err := s.InteractionResponseEdit(i.Interaction, usersWebhookEdit(report, format)); err != nil {
			slog.Error("could not edit /status users response", "error", err)
		}
	}()
}
