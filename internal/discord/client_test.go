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
	var _ ConfigureCommand
}

func TestBuildNotificationEmbed(t *testing.T) {
	embed := buildNotificationEmbed(
		"myproject", "abc1", "hello world", "",
	)
	if embed.Title != "Claude waiting (myproject)" {
		t.Errorf("unexpected title: %s",
			embed.Title)
	}
	if embed.Footer == nil ||
		embed.Footer.Text !=
			"Session: myproject #abc1" {
		t.Error("unexpected footer")
	}
}

func TestBuildNotificationEmbed_Summary(t *testing.T) {
	embed := buildNotificationEmbed(
		"proj", "x", "raw preview", "summary here",
	)
	if !contains(embed.Description, "summary here") {
		t.Error("summary not in description")
	}
	if contains(embed.Description, "raw preview") {
		t.Error("raw preview should be replaced")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) &&
		containsAt(s, sub)
}

func containsAt(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
