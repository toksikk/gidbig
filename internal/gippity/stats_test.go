package gippity

import (
	"context"
	"testing"
)

func TestStats(t *testing.T) {
	setupGippityTest(t)
	for _, q := range []string{
		`INSERT INTO chat_history (user_id, channel_id, message_id, is_bot_mention) VALUES ('u1','c1','m1',1), ('u1','c1','m2',0), ('u2','c2','m3',0)`,
		`INSERT INTO chat_history_edits (original_message_id, edited_content, version, edited_at) VALUES ('m1','x',1,0)`,
		`INSERT INTO user_privacy (user_id, privacy_enabled) VALUES ('u1',0), ('u2',1)`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	st, err := StatsProvider.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range append(st.Summary, st.Detail...) {
		got[s.Name] = s.Value
	}
	want := map[string]string{"msgs": "3", "mentions": "1", "users": "2", "channels": "2", "edits": "1", "images": "0", "privacy off": "1"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if StatsProvider.Name() != "gippity" {
		t.Errorf("Name = %q", StatsProvider.Name())
	}
}
