package discord

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Client wraps a discordgo session for sending DM
// notifications and polling for user replies.
type Client struct {
	session    *discordgo.Session
	userID     string
	dmChannel  string
	validator  *Validator
	retryAfter time.Time
	mu         sync.Mutex
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

// checkRateLimit returns an error if we should wait
// before making another API call.
func (c *Client) checkRateLimit() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.retryAfter) {
		return fmt.Errorf(
			"rate limited until %v",
			c.retryAfter.Format(time.RFC3339))
	}
	return nil
}

// handleRateLimit checks if an error is a 429 and sets
// the backoff timer.
func (c *Client) handleRateLimit(err error) {
	if err == nil {
		return
	}
	// discordgo wraps HTTP errors; check for 429.
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) &&
		restErr.Response != nil &&
		restErr.Response.StatusCode == 429 {
		c.mu.Lock()
		// Default backoff: 5 seconds.
		c.retryAfter = time.Now().Add(
			5 * time.Second)
		c.mu.Unlock()
		log.Printf(
			"Discord rate limited, backing off 5s")
	}
}

// SendNotification sends a rich embed DM with the
// question preview and numbered reply suggestions.
// Returns the sent message ID.
func (c *Client) SendNotification(
	projectName, shortID, preview string,
	suggestions []string,
) (string, error) {
	if err := c.checkRateLimit(); err != nil {
		return "", err
	}
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
	c.handleRateLimit(err)
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
	if err := c.checkRateLimit(); err != nil {
		return nil, err
	}
	if err := c.ensureDMChannel(); err != nil {
		return nil, err
	}

	msgs, err := c.session.ChannelMessages(
		c.dmChannel, 10, "", afterMsgID, "",
	)
	c.handleRateLimit(err)
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
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	if err := c.ensureDMChannel(); err != nil {
		return err
	}
	_, err := c.session.ChannelMessageSend(
		c.dmChannel, text,
	)
	c.handleRateLimit(err)
	return err
}

// DeleteMessage deletes a message from the DM channel.
// Used to clean up stale notifications when the user
// returns to the session without replying via Discord.
func (c *Client) DeleteMessage(msgID string) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	if err := c.ensureDMChannel(); err != nil {
		return err
	}
	err := c.session.ChannelMessageDelete(
		c.dmChannel, msgID,
	)
	c.handleRateLimit(err)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	return nil
}

// ClearNotificationMessages scans recent DM messages for
// notification embeds sent by the bot and deletes them.
// If sessionFilter is non-empty, only deletes embeds
// whose footer matches the given session ID. Returns the
// number of messages deleted.
func (c *Client) ClearNotificationMessages(
	sessionFilter string,
) (int, error) {
	if err := c.checkRateLimit(); err != nil {
		return 0, err
	}
	if err := c.ensureDMChannel(); err != nil {
		return 0, err
	}

	// Fetch the last 50 messages in the DM channel.
	msgs, err := c.session.ChannelMessages(
		c.dmChannel, 50, "", "", "",
	)
	c.handleRateLimit(err)
	if err != nil {
		return 0, fmt.Errorf(
			"fetch DM messages: %w", err)
	}

	// Find our bot's user ID from the gateway state,
	// falling back to checking if the author isn't the
	// configured user.
	var botID string
	if c.session.State != nil &&
		c.session.State.User != nil {
		botID = c.session.State.User.ID
	}

	deleted := 0
	for _, msg := range msgs {
		if msg.Author == nil {
			continue
		}
		// Only delete messages from the bot.
		if botID != "" && msg.Author.ID != botID {
			continue
		}
		// Must not be from the user.
		if msg.Author.ID == c.userID {
			continue
		}
		// Must have an embed with "Claude waiting"
		// title (our notification format).
		if !isNotificationEmbed(
			msg, sessionFilter,
		) {
			continue
		}

		err := c.session.ChannelMessageDelete(
			c.dmChannel, msg.ID,
		)
		c.handleRateLimit(err)
		if err != nil {
			log.Printf(
				"delete notification msg %s: %v",
				msg.ID, err,
			)
			continue
		}
		deleted++
	}
	return deleted, nil
}

// isNotificationEmbed checks if a message contains a
// claude-notify notification embed. If sessionFilter is
// non-empty, the embed's footer must contain that session
// ID.
func isNotificationEmbed(
	msg *discordgo.Message, sessionFilter string,
) bool {
	for _, embed := range msg.Embeds {
		if embed.Title == "" ||
			!strings.HasPrefix(
				embed.Title, "Claude waiting") {
			continue
		}
		if sessionFilter == "" {
			return true
		}
		if embed.Footer != nil &&
			strings.Contains(
				strings.ToLower(embed.Footer.Text),
				"#"+strings.ToLower(sessionFilter),
			) {
			return true
		}
	}
	return false
}

// FetchRecentUserMessages fetches the most recent
// messages from the DM channel sent by the configured
// user. Used by the daemon to check for commands like
// /clear even when no sessions are actively notified.
func (c *Client) FetchRecentUserMessages(
	limit int,
) ([]Reply, error) {
	if err := c.checkRateLimit(); err != nil {
		return nil, err
	}
	if err := c.ensureDMChannel(); err != nil {
		return nil, err
	}

	msgs, err := c.session.ChannelMessages(
		c.dmChannel, limit, "", "", "",
	)
	c.handleRateLimit(err)
	if err != nil {
		return nil, fmt.Errorf(
			"fetch messages: %w", err)
	}

	var replies []Reply
	for _, msg := range msgs {
		if msg.Author == nil ||
			msg.Author.ID != c.userID {
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

// Close shuts down the discordgo session.
func (c *Client) Close() {
	if c.session != nil {
		_ = c.session.Close()
	}
}
