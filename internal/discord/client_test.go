package discord

import (
	"testing"
)

func TestExpandReaction(t *testing.T) {
	tests := []struct {
		emoji string
		want  string
	}{
		{ReactionYes, "Yes, continue"},
		{ReactionNo, "No, stop here"},
		{ReactionLook, "Show me what you have so far"},
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
