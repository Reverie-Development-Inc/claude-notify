package discord

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ClearHandler is called when a user invokes the /clear
// slash command. The handler receives the optional
// session ID (empty = clear all) and returns a response
// message.
type ClearHandler func(sessionID string) string

// Client wraps a discordgo session for sending DM
// notifications and polling for user replies.
type Client struct {
	session      *discordgo.Session
	userID       string
	dmChannel    string
	validator    *Validator
	retryAfter   time.Time
	mu           sync.Mutex
	clearHandler ClearHandler
	registeredCmd *discordgo.ApplicationCommand
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

// RegisterClearCommand registers the /clear slash command
// and opens the gateway to receive interactions. The
// handler is called when a user invokes /clear.
func (c *Client) RegisterClearCommand(
	handler ClearHandler,
) error {
	c.clearHandler = handler

	// Register the interaction handler before opening
	// the gateway so we don't miss events.
	c.session.AddHandler(c.handleInteraction)

	// Only need interactions — no message intents.
	c.session.Identify.Intents = 0

	// Open the gateway connection.
	if err := c.session.Open(); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}

	// Register the /clear command globally (works in
	// DMs and servers).
	dmPerm := true
	cmd, err := c.session.ApplicationCommandCreate(
		c.session.State.User.ID, "",
		&discordgo.ApplicationCommand{
			Name: "clear",
			Description: "Clear pending Claude " +
				"Code notifications",
			DMPermission: &dmPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type: discordgo.
						ApplicationCommandOptionString,
					Name:        "session",
					Description: "Session ID to " +
						"clear (omit for all)",
					Required: false,
				},
			},
		},
	)
	if err != nil {
		return fmt.Errorf(
			"register /clear command: %w", err)
	}
	c.registeredCmd = cmd
	log.Printf("registered /clear slash command")
	return nil
}

// handleInteraction routes Discord interactions to the
// appropriate handler.
func (c *Client) handleInteraction(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
) {
	if i.Type !=
		discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	if data.Name != "clear" {
		return
	}

	// Only allow the configured user.
	var invokerID string
	if i.User != nil {
		invokerID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		invokerID = i.Member.User.ID
	}
	if invokerID != c.userID {
		s.InteractionRespond(i.Interaction,
			&discordgo.InteractionResponse{
				Type: discordgo.
					InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "You are not authorized " +
						"to use this command.",
					Flags: discordgo.MessageFlagsEphemeral,
				},
			})
		return
	}

	// Extract optional session ID.
	var sessionID string
	for _, opt := range data.Options {
		if opt.Name == "session" {
			sessionID = opt.StringValue()
		}
	}

	// Call the daemon's clear handler.
	var result string
	if c.clearHandler != nil {
		result = c.clearHandler(sessionID)
	} else {
		result = "No handler configured."
	}

	s.InteractionRespond(i.Interaction,
		&discordgo.InteractionResponse{
			Type: discordgo.
				InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: result,
				Flags: discordgo.
					MessageFlagsEphemeral,
			},
		})
}

// Close shuts down the discordgo session and
// deregisters slash commands.
func (c *Client) Close() {
	if c.session != nil {
		if c.registeredCmd != nil &&
			c.session.State != nil &&
			c.session.State.User != nil {
			err := c.session.ApplicationCommandDelete(
				c.session.State.User.ID,
				"",
				c.registeredCmd.ID,
			)
			if err != nil {
				log.Printf(
					"deregister /clear: %v", err)
			}
		}
		c.session.Close()
	}
}
