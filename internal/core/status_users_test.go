package gidbig

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func member(id, name string, bot bool) *discordgo.Member {
	return &discordgo.Member{User: &discordgo.User{ID: id, Username: name, Bot: bot}}
}

func TestCollectUsersPaginatesAndGroups(t *testing.T) {
	big := make([]*discordgo.Member, 0, guildMembersPerPage+2)
	for n := 0; n < guildMembersPerPage+1; n++ {
		big = append(big, member(fmt.Sprintf("%05d", n+10), fmt.Sprintf("u%05d", n), false))
	}
	big = append(big, member("00002", "bob", false), member("00009", "gidbig", true))
	data := map[string][]*discordgo.Member{
		"g1": big,
		"g2": {member("00001", "alice", false), member("00002", "bob", false)},
	}
	var calls []string
	list := func(_ context.Context, gid, after string, limit int) ([]*discordgo.Member, error) {
		calls = append(calls, gid+"/"+after)
		all := data[gid]
		start := 0
		for start < len(all) && after != "" && all[start].User.ID != after {
			start++
		}
		if after != "" {
			start++
		}
		end := min(start+limit, len(all))
		return all[start:end], nil
	}

	guilds, users := collectUsers(context.Background(), []guildStatus{{ID: "g1", Name: "Big"}, {ID: "g2", Name: "Small"}}, list)

	if len(calls) != 3 {
		t.Errorf("calls = %v, want 2 pages for g1 + 1 for g2", calls)
	}
	if guilds[0].Humans != guildMembersPerPage+2 || guilds[0].Bots != 1 || guilds[1].Members != 2 {
		t.Errorf("guild results = %+v", guilds)
	}
	if users[0].Username != "bob" || strings.Join(users[0].Guilds, ",") != "Big,Small" {
		t.Errorf("first user = %+v, want bob in both guilds", users[0])
	}
	if last := users[len(users)-1]; !last.Bot {
		t.Errorf("bots should sort last, got %+v", last)
	}
}

func TestCollectUsersReportsMissingIntent(t *testing.T) {
	intentErr := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeMissingAccess, Message: "Missing Access"}}
	list := func(_ context.Context, gid, _ string, _ int) ([]*discordgo.Member, error) {
		switch gid {
		case "g3":
			return nil, errors.New("boom")
		case "g4":
			return []*discordgo.Member{member("1", "alice", false)}, nil
		}
		return nil, intentErr
	}
	guilds, users := collectUsers(context.Background(), []guildStatus{{ID: "g1", Name: "A"}, {ID: "g2", Name: "B"}, {ID: "g3", Name: "C"}, {ID: "g4", Name: "D"}}, list)
	r := usersReport{Header: []string{"hdr"}, Guilds: guilds, Users: users}

	w := r.warnings()
	if len(w) != 2 || w[0] != membersIntentHint || !strings.Contains(w[1], "C: boom") {
		t.Fatalf("warnings = %q", w)
	}
	out := renderUsersText(r, discordMessageLimit)
	for _, want := range []string{"A                        n/a", "D                             1 users · 0 bots", "Users (1 + 0 bots)", "alice", "Server Members Intent"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestIsMissingMembersIntentForbiddenStatus(t *testing.T) {
	if !isMissingMembersIntent(&discordgo.RESTError{Response: &http.Response{StatusCode: http.StatusForbidden}}) {
		t.Error("403 should be treated as missing intent")
	}
	if isMissingMembersIntent(errors.New("x")) {
		t.Error("plain error is not missing intent")
	}
}

func sampleUsersReport(n int) usersReport {
	r := usersReport{Header: []string{"gidbig v1", "up 1m"}, Guilds: []guildUsersResult{{Name: "Big", Members: n, Humans: n}}}
	for i := 0; i < n; i++ {
		r.Users = append(r.Users, userAccess{ID: fmt.Sprint(i), Username: fmt.Sprintf("user%04d", i), DisplayName: "Display, Name", Guilds: []string{"Big", "Other"}})
	}
	return r
}

func TestUsersWebhookEditTextAndCSV(t *testing.T) {
	r := sampleUsersReport(500)
	edit := usersWebhookEdit(r, statusFormatText)
	if edit.Content == nil || len([]rune(*edit.Content)) > discordMessageLimit {
		t.Fatalf("content missing or too long")
	}
	if !strings.Contains(*edit.Content, "more") {
		t.Error("expected omission marker in trimmed list")
	}
	if len(edit.Files) != 1 || edit.Files[0].Name != statusUsersCSVName {
		t.Fatalf("files = %+v", edit.Files)
	}
	body, _ := io.ReadAll(edit.Files[0].Reader)
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 501 {
		t.Fatalf("csv rows = %d, want header + 500", len(rows))
	}
	if strings.Join(rows[1], "|") != "0|user0000|Display, Name|false|2|Big; Other" {
		t.Errorf("row = %q", rows[1])
	}
}

func TestUsersWebhookEditEmbed(t *testing.T) {
	edit := usersWebhookEdit(sampleUsersReport(300), statusFormatEmbed)
	if edit.Embeds == nil || len(*edit.Embeds) != 1 {
		t.Fatal("expected one embed")
	}
	e := (*edit.Embeds)[0]
	for _, f := range e.Fields {
		if len(f.Value) > embedFieldValueLimit {
			t.Errorf("field %q too long: %d", f.Name, len(f.Value))
		}
	}
	if !strings.Contains(e.Footer.Text, statusUsersCSVName) {
		t.Errorf("footer = %q", e.Footer.Text)
	}
	if len(edit.Files) != 1 {
		t.Error("embed variant should attach CSV too")
	}
}

func TestUsersWebhookEditNoUsersNoFile(t *testing.T) {
	edit := usersWebhookEdit(usersReport{Header: []string{"h"}}, statusFormatText)
	if len(edit.Files) != 0 {
		t.Error("no CSV expected without users")
	}
}
