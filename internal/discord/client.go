package discord

import (
	"fmt"
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

// Reply represents a validated user reply with routing
// info.
type Reply struct {
	Content      string
	MessageID    string
	// RefMessageID is the ID of the message this replies
	// to (Discord reply-to). Empty if bare message.
	RefMessageID string
}

// FetchReplies fetches recent messages from the DM
// channel sent after afterMsgID by the expected user.
// Returns validated replies with routing information.
func (c *Client) FetchReplies(
	afterMsgID string,
) ([]Reply, error) {
	if err := c.ensureDMChannel(); err != nil {
		return nil, err
	}

	msgs, err := c.session.ChannelMessages(
		c.dmChannel, 10, "", afterMsgID, "",
	)
	if err != nil {
		return nil, fmt.Errorf(
			"fetch messages: %w", err,
		)
	}

	var replies []Reply
	for _, msg := range msgs {
		if msg.Author == nil {
			continue
		}
		if err := c.validator.Validate(
			msg.Author.ID, msg.Timestamp,
		); err != nil {
			continue
		}
		r := Reply{
			Content:   msg.Content,
			MessageID: msg.ID,
		}
		if msg.MessageReference != nil {
			r.RefMessageID =
				msg.MessageReference.MessageID
		}
		replies = append(replies, r)
	}
	return replies, nil
}

// SendHint sends a plain text message (not an embed) to
// the DM channel, e.g. to tell the user to use Discord's
// Reply feature.
func (c *Client) SendHint(text string) error {
	if err := c.ensureDMChannel(); err != nil {
		return err
	}
	_, err := c.session.ChannelMessageSend(
		c.dmChannel, text,
	)
	return err
}

// Close shuts down the discordgo session.
func (c *Client) Close() {
	if c.session != nil {
		c.session.Close()
	}
}
