package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
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
	r := ReplyEvent{
		UserID:    "u1",
		ChannelID: "ch1",
	}
	if r.UserID != "u1" {
		t.Error("ReplyEvent.UserID")
	}
	if r.ChannelID != "ch1" {
		t.Error("ReplyEvent.ChannelID")
	}
	re := ReactionEvent{
		UserID:    "u2",
		ChannelID: "ch2",
	}
	if re.UserID != "u2" {
		t.Error("ReactionEvent.UserID")
	}
	if re.ChannelID != "ch2" {
		t.Error("ReactionEvent.ChannelID")
	}
	var _ ClearCommand
	var _ ConfigureCommand
}

func TestBuildNotificationEmbed(t *testing.T) {
	embed := buildNotificationEmbed(
		"myproject", "abc1", "hello world", "",
		1,
	)
	want := "Session 1: Claude is waiting..."
	if embed.Title != want {
		t.Errorf("title = %q, want %q",
			embed.Title, want)
	}
	if embed.Color != ColorWaiting {
		t.Errorf("color = %d, want %d",
			embed.Color, ColorWaiting)
	}
	if embed.Footer == nil ||
		embed.Footer.Text !=
			"Session: myproject #abc1" {
		t.Error("unexpected footer")
	}
}

func TestBuildNotificationEmbed_Summary(
	t *testing.T,
) {
	embed := buildNotificationEmbed(
		"proj", "x", "raw preview",
		"summary here", 3,
	)
	if !contains(
		embed.Description, "summary here",
	) {
		t.Error("summary not in description")
	}
	if contains(
		embed.Description, "raw preview",
	) {
		t.Error(
			"raw preview should be replaced")
	}
}

func TestIsNotificationEmbed(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		footer string
		filter string
		want   bool
	}{
		{
			"waiting embed matches",
			"Session 1: Claude is waiting...",
			"Session: proj #abc1", "", true,
		},
		{
			"working embed matches",
			"Session 2: Claude is working...",
			"Session: proj #def2", "", true,
		},
		{
			"disconnected embed matches",
			"Session 3: Disconnected",
			"Session: proj #ghi3", "", true,
		},
		{
			"filter matches",
			"Session 1: Claude is waiting...",
			"Session: proj #abc1", "abc1",
			true,
		},
		{
			"filter mismatch",
			"Session 1: Claude is waiting...",
			"Session: proj #abc1", "xyz9",
			false,
		},
		{
			"non-notification embed",
			"Some other title",
			"whatever", "", false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &discordgo.Message{
				Embeds: []*discordgo.MessageEmbed{
					{
						Title: tt.title,
						Footer: &discordgo.
							MessageEmbedFooter{
							Text: tt.footer,
						},
					},
				},
			}
			got := isNotificationEmbed(
				msg, tt.filter,
			)
			if got != tt.want {
				t.Errorf("got %v, want %v",
					got, tt.want)
			}
		})
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
