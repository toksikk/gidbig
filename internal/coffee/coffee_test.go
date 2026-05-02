package coffee

import "testing"

func TestBeverageEmojiFor(t *testing.T) {
	tests := []struct {
		userID   string
		expected string
	}{
		{"263959699764805642", "☕"},
		{"217697101818232832", "☕"},
		{"000000000000000000", "☕"},
		{"", "☕"},
	}
	for _, tt := range tests {
		got := beverageEmojiFor(tt.userID)
		if got != tt.expected {
			t.Errorf("beverageEmojiFor(%q) = %q; want %q", tt.userID, got, tt.expected)
		}
	}
}
