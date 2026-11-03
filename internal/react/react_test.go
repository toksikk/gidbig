package react

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func reaction(user, msgID, emojiID string) *discordgo.MessageReaction {
	return &discordgo.MessageReaction{UserID: user, MessageID: msgID, Emoji: discordgo.Emoji{ID: emojiID, Name: "x"}}
}

func TestShouldRemove(t *testing.T) {
	m := New()
	m.watches.add(watchKey{messageID: "m", emojiID: "e", userID: "inv"})

	cases := []struct {
		name string
		r    *discordgo.MessageReaction
		want bool
	}{
		{"invoker same emoji", reaction("inv", "m", "e"), true},
		{"other user", reaction("other", "m", "e"), false},
		{"other emoji", reaction("inv", "m", "e2"), false},
		{"other message", reaction("inv", "m2", "e"), false},
		{"bot itself", reaction("bot", "m", "e"), false},
		{"unicode emoji", reaction("inv", "m", ""), false},
		{"nil", nil, false},
	}
	m.watches.add(watchKey{messageID: "m", emojiID: "e", userID: "bot"})
	for _, c := range cases {
		if got := m.shouldRemove("bot", c.r); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStats(t *testing.T) {
	m := New()
	m.watches.add(watchKey{messageID: "m"})
	m.removed.Add(2)
	st, err := m.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Summary[0].Value != "1" || st.Summary[1].Value != "2" {
		t.Fatalf("stats %+v", st.Summary)
	}
}

func TestCommandDefinition(t *testing.T) {
	c := command()
	if c.Name != commandName || len(c.Options) != 3 {
		t.Fatalf("command %+v", c)
	}
	for _, o := range c.Options {
		if o.Required {
			t.Errorf("option %s must be optional", o.Name)
		}
	}
}
