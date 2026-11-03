package react

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestValidateOptions(t *testing.T) {
	cases := []struct {
		name, user, msg string
		wantErr         bool
	}{
		{"neither", "", "", true},
		{"both", "1", "2", true},
		{"user only", "1", "", false},
		{"message only", "", "123456789012345678", false},
		{"bad message id", "", "abc", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateOptions(c.user, c.msg); (err != nil) != c.wantErr {
				t.Fatalf("validateOptions(%q,%q) err=%v, wantErr=%v", c.user, c.msg, err, c.wantErr)
			}
		})
	}
}

func msg(id, author string) *discordgo.Message {
	return &discordgo.Message{ID: id, Author: &discordgo.User{ID: author}}
}

func TestFindLastMessage(t *testing.T) {
	msgs := []*discordgo.Message{msg("5", "a"), msg("4", "b"), nil, {ID: "3"}, msg("2", "b")}
	if got := findLastMessage(msgs, "b"); got == nil || got.ID != "4" {
		t.Fatalf("want newest b message 4, got %v", got)
	}
	if got := findLastMessage(msgs, "a"); got == nil || got.ID != "5" {
		t.Fatalf("want 5, got %v", got)
	}
	if got := findLastMessage(msgs, "zzz"); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}
