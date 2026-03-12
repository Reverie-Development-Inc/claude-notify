package discord

import (
	"testing"
)

func TestExpandReaction(t *testing.T) {
	tests := []struct {
		emoji string
		want  string
	}{
		{ReactionYes, "Yes or Continue, decide which " +
			"answer makes more sense based on context."},
		{ReactionNo, "No"},
		{ReactionLook, "Show me additional context on this"},
		{"🎉", ""},
	}
	for _, tt := range tests {
		got := ExpandReaction(tt.emoji)
		if got != tt.want {
			t.Errorf(
				"ExpandReaction(%s) = %q, want %q",
				tt.emoji, got, tt.want,
			)
		}
	}
}

func TestEventChannelTypes(t *testing.T) {
	var _ ReplyEvent
	var _ ReactionEvent
	var _ ClearCommand
}
