package react

import (
	"fmt"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func em(id, name string) *discordgo.Emoji {
	return &discordgo.Emoji{ID: id, Name: name, Available: true}
}

func TestCollectEmojis(t *testing.T) {
	guilds := []*discordgo.Guild{
		{Emojis: []*discordgo.Emoji{em("3", "beta"), em("1", "Alpha"), {ID: "9", Name: "gone"}}},
		nil,
		{Emojis: []*discordgo.Emoji{em("1", "Alpha"), em("2", "alpha"), {Name: "nounicode", Available: true}}},
	}
	got := collectEmojis(guilds, "")
	var ids []string
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	if fmt.Sprint(ids) != "[1 2 3]" {
		t.Fatalf("got %v, want [1 2 3]", ids)
	}
	if got := collectEmojis(guilds, "ALP"); len(got) != 2 {
		t.Fatalf("filter ALP: got %d, want 2", len(got))
	}
	if got := collectEmojis(guilds, "nope"); len(got) != 0 {
		t.Fatalf("filter nope: got %d", len(got))
	}
}

func pageParts(t *testing.T, comps []discordgo.MessageComponent) (discordgo.SelectMenu, discordgo.Button, discordgo.Button) {
	t.Helper()
	sel := comps[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	btns := comps[1].(discordgo.ActionsRow).Components
	return sel, btns[0].(discordgo.Button), btns[2].(discordgo.Button)
}

func TestBuildPage(t *testing.T) {
	var emojis []*discordgo.Emoji
	for i := range 60 {
		emojis = append(emojis, em(fmt.Sprintf("%018d", i), fmt.Sprintf("e%02d", i)))
	}
	tok := "0123456789abcdef"

	sel, prev, next := pageParts(t, buildPage(tok, emojis, 0))
	if len(sel.Options) != pageSize || !prev.Disabled || next.Disabled {
		t.Fatalf("page 0: opts=%d prev=%v next=%v", len(sel.Options), prev.Disabled, next.Disabled)
	}
	o := sel.Options[0]
	if o.Emoji == nil || o.Emoji.ID != emojis[0].ID || o.Value != emojis[0].ID || o.Label != "e00" {
		t.Fatalf("option 0 = %+v", o)
	}
	if next.CustomID != "react:next:"+tok+":1" {
		t.Fatalf("next id %q", next.CustomID)
	}
	for _, id := range []string{sel.CustomID, prev.CustomID, next.CustomID} {
		if len(id) > 100 {
			t.Fatalf("custom ID too long: %q", id)
		}
	}

	sel, prev, next = pageParts(t, buildPage(tok, emojis, 2))
	if len(sel.Options) != 10 || prev.Disabled || !next.Disabled {
		t.Fatalf("last page: opts=%d prev=%v next=%v", len(sel.Options), prev.Disabled, next.Disabled)
	}
	sel, _, _ = pageParts(t, buildPage(tok, emojis, 99))
	if len(sel.Options) != 10 {
		t.Fatalf("clamped page: opts=%d", len(sel.Options))
	}
}

func TestParseCustomID(t *testing.T) {
	cases := []struct {
		in     string
		action string
		page   int
		ok     bool
	}{
		{"react:sel:tok", "sel", 0, true},
		{"react:next:tok:3", "next", 3, true},
		{"react:prev:tok:-1", "prev", -1, true},
		{"react:next:tok", "", 0, false},
		{"react:next:tok:x", "", 0, false},
		{"react:page:tok", "", 0, false},
		{"other:sel:tok", "", 0, false},
	}
	for _, c := range cases {
		action, tok, page, ok := parseCustomID(c.in)
		if ok != c.ok || action != c.action || page != c.page || (ok && tok != "tok") {
			t.Errorf("parseCustomID(%q) = %q %q %d %v", c.in, action, tok, page, ok)
		}
	}
}
