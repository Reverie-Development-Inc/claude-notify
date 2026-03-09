package discord

import (
	"testing"
)

func TestExpandReaction(t *testing.T) {
	tests := []struct {
		emoji string
		want  string
	}{
		{"✅", "Yes, continue"},
		{"❌", "No, stop here"},
		{"👀", "Show me what you have so far"},
		{"🤷", ""},
	}
	for _, tt := range tests {
		got := ExpandReaction(tt.emoji)
		if got != tt.want {
			t.Errorf(
				"ExpandReaction(%q) = %q, want %q",
				tt.emoji, got, tt.want,
			)
		}
	}
}
