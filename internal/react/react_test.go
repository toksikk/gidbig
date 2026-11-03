package react

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func reaction(user, msgID, emojiID string) *discordgo.MessageReaction {
	return &discordgo.MessageReaction{UserID: user, MessageID: msgID, Emoji: discordgo.Emoji{ID: emojiID, Name: "x"}}
}

func TestClaimWatch(t *testing.T) {
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
	for _, c := range cases {
		m := New()
		m.watches.add(watchKey{messageID: "m", emojiID: "e", userID: "inv"})
		m.watches.add(watchKey{messageID: "m", emojiID: "e", userID: "bot"})
		if got := m.claimWatch("bot", c.r); got != c.want {
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

func TestOnReactionAddRemovesOnlyOwnReactionOnce(t *testing.T) {
	m := New()
	m.watches.add(watchKey{messageID: "m", emojiID: "e", userID: "inv"})
	var calls []string
	m.removeOwnReaction = func(_ *discordgo.Session, ch, msg, emoji string) error {
		calls = append(calls, ch+"/"+msg+"/"+emoji)
		return nil
	}
	s := &discordgo.Session{State: discordgo.NewState()}
	s.State.User = &discordgo.User{ID: "bot"}
	ev := func(user string) *discordgo.MessageReactionAdd {
		r := reaction(user, "m", "e")
		r.ChannelID = "c"
		return &discordgo.MessageReactionAdd{MessageReaction: r}
	}

	m.onReactionAdd(s, ev("other"))
	if len(calls) != 0 {
		t.Fatalf("other user triggered removal: %v", calls)
	}
	m.onReactionAdd(s, ev("inv"))
	if len(calls) != 1 || calls[0] != "c/m/x:e" {
		t.Fatalf("calls %v, want [c/m/x:e]", calls)
	}
	if m.removed.Load() != 1 || m.watches.len() != 0 {
		t.Fatalf("removed=%d watches=%d", m.removed.Load(), m.watches.len())
	}
	m.onReactionAdd(s, ev("inv"))
	if len(calls) != 1 {
		t.Fatalf("watch fired twice: %v", calls)
	}
}
