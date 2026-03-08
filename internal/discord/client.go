package discord

import (
	"fmt"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Client wraps a discordgo session for sending DM
// notifications and polling for user replies.
type Client struct {
	session   *discordgo.Session
	userID    string
	dmChannel string
	validator *Validator
}

// NewClient creates a Discord REST client. The token
// should be a bot token; userID is the Discord user to DM.
func NewClient(
	token, userID string,
) (*Client, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &Client{
		session:   s,
		userID:    userID,
		validator: NewValidator(userID),
	}, nil
}

// ensureDMChannel opens (or reuses) a DM channel with
// the configured user.
func (c *Client) ensureDMChannel() error {
	if c.dmChannel != "" {
		return nil
	}
	ch, err := c.session.UserChannelCreate(c.userID)
	if err != nil {
		return fmt.Errorf("create DM channel: %w", err)
	}
	c.dmChannel = ch.ID
	return nil
}

// SendNotification sends a rich embed DM with the
// question preview and numbered reply suggestions.
// Returns the sent message ID.
func (c *Client) SendNotification(
	projectName, shortID, preview string,
	suggestions []string,
) (string, error) {
	if err := c.ensureDMChannel(); err != nil {
		return "", err
	}

	desc := preview
	if len(suggestions) > 0 {
		desc += "\n"
		for i, s := range suggestions {
			desc += fmt.Sprintf(
				"\n**%d.** %s", i+1, s,
			)
		}
		desc += "\n\nOr type a custom reply."
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf(
			"Claude waiting (%s)", projectName,
		),
		Description: desc,
		Color:       0xD4A574, // warm amber
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf(
				"Session: %s #%s",
				projectName, shortID,
			),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	msg, err := c.session.ChannelMessageSendEmbed(
		c.dmChannel, embed,
	)
	if err != nil {
		return "", fmt.Errorf("send DM: %w", err)
	}

	c.validator.SetNotificationTime(time.Now())
	return msg.ID, nil
}

// PollForReply fetches recent messages in the DM channel
// after afterMsgID and returns the first valid reply from
// the expected user. Returns empty string if no valid
// reply found.
func (c *Client) PollForReply(
	afterMsgID string,
) (string, error) {
	if err := c.ensureDMChannel(); err != nil {
		return "", err
	}

	msgs, err := c.session.ChannelMessages(
		c.dmChannel, 10, "", afterMsgID, "",
	)
	if err != nil {
		return "", fmt.Errorf("fetch messages: %w", err)
	}

	for _, msg := range msgs {
		if msg.Author == nil {
			continue
		}
		if err := c.validator.Validate(
			msg.Author.ID, msg.Timestamp,
		); err != nil {
			log.Printf(
				"skip message %s: %v", msg.ID, err,
			)
			continue
		}
		return msg.Content, nil
	}
	return "", nil
}

// Close shuts down the discordgo session.
func (c *Client) Close() {
	if c.session != nil {
		c.session.Close()
	}
}
