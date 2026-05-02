package coffee

import "testing"

func TestIsValidBeverageEmoji(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		// valid Unicode emoji
		{"🫖", true},
		{"☕", true},
		{"🍺", true},
		{"🧃", true},
		// valid Discord custom emoji
		{"<:customemoji:123456789>", true},
		{"<a:animatedemoji:987654321>", true},
		// invalid: plain text
		{"hello", false},
		{"hello world", false},
		// invalid: empty
		{"", false},
		// invalid: number
		{"42", false},
		// invalid: mixed text
		{"coffee", false},
	}
	for _, tt := range tests {
		got := isValidBeverageEmoji(tt.input)
		if got != tt.valid {
			t.Errorf("isValidBeverageEmoji(%q) = %v; want %v", tt.input, got, tt.valid)
		}
	}
}

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
