package discord

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Reverie-Development-Inc/claude-notify/internal/sanitize"
	"github.com/bwmarrin/discordgo"
)

// embedDescLimit is Discord's maximum character count
// for an embed description field.
const embedDescLimit = 4096

// ReplyEvent is sent when a user replies to a
// notification in the DM channel.
type ReplyEvent struct {
	Content      string
	MessageID    string
	RefMessageID string
	ChannelID    string
	UserID       string
}

// ReactionEvent is sent when a user reacts to a
// notification message.
type ReactionEvent struct {
	MessageID string
	ChannelID string
	Emoji     string
	UserID    string
}

// ClearCommand is sent when a user invokes the
// /clear slash command.
type ClearCommand struct {
	SessionID   string
	Interaction interface{} // *discordgo.Interaction
}

// ConfigureCommand is sent when a user invokes the
// /configure slash command.
type ConfigureCommand struct {
	// Subcommand: "user" or "channel"
	Subcommand string
	// Action: "add", "remove", "list", "set",
	// "clear", "show"
	Action string
	// Value: user ID or channel ID
	Value       string
	Interaction interface{} // *discordgo.Interaction
}

// Client wraps a discordgo session for sending DM
// notifications and polling for user replies.
type Client struct {
	session    *discordgo.Session
	userID     string
	dmChannel  string
	validator  *Validator
	retryAfter time.Time
	mu         sync.Mutex

	// IsAllowed checks if a user ID is permitted to
	// react/reply. Set by daemon after loading config.
	// If nil, only the owner is allowed.
	IsAllowed func(userID string) bool

	// Gateway event channels — daemon selects on these.
	Replies    chan ReplyEvent
	Reactions  chan ReactionEvent
	Clears     chan ClearCommand
	Configures chan ConfigureCommand

	// appID is the bot's application ID, needed for
	// slash command registration.
	appID     string
	botUserID string
}

// NewClient creates a Discord client with a persistent
// gateway connection. The token should be a bot token;
// userID is the Discord user to DM.
func NewClient(
	token, userID string,
) (*Client, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Intents: DM + guild messages/reactions for channel
	// mode notifications.
	s.Identify.Intents =
		discordgo.IntentsDirectMessages |
			discordgo.IntentsDirectMessageReactions |
			discordgo.IntentsGuildMessages |
			discordgo.IntentsGuildMessageReactions

	c := &Client{
		session:    s,
		userID:     userID,
		validator:  NewValidator(userID),
		Replies:    make(chan ReplyEvent, 16),
		Reactions:  make(chan ReactionEvent, 16),
		Clears:     make(chan ClearCommand, 4),
		Configures: make(chan ConfigureCommand, 4),
	}

	// Register gateway event handlers.
	s.AddHandler(c.onReady)
	s.AddHandler(c.onMessageCreate)
	s.AddHandler(c.onMessageReactionAdd)
	s.AddHandler(c.onInteractionCreate)

	// Open the gateway connection.
	if err := s.Open(); err != nil {
		return nil, fmt.Errorf(
			"open gateway: %w", err)
	}

	return c, nil
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

// DMChannelID returns the cached DM channel ID.
func (c *Client) DMChannelID() string {
	return c.dmChannel
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
// the backoff timer. Reads the Retry-After header when
// available, otherwise falls back to 5 seconds.
func (c *Client) handleRateLimit(err error) {
	if err == nil {
		return
	}
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) ||
		restErr.Response == nil ||
		restErr.Response.StatusCode != 429 {
		return
	}

	backoff := 5 * time.Second
	if ra := restErr.Response.Header.Get(
		"Retry-After",
	); ra != "" {
		if secs, err := strconv.ParseFloat(
			ra, 64,
		); err == nil && secs > 0 {
			backoff = time.Duration(
				secs * float64(time.Second),
			)
		}
	}
	// Floor: at least 1 second.
	if backoff < time.Second {
		backoff = time.Second
	}

	c.mu.Lock()
	c.retryAfter = time.Now().Add(backoff)
	c.mu.Unlock()
	log.Printf(
		"Discord rate limited, backing off %v",
		backoff,
	)
}

// buildNotificationEmbed constructs the embed for a
// notification message.
func buildNotificationEmbed(
	projectName, shortID, preview, summary string,
	sessionNum int,
) *discordgo.MessageEmbed {
	body := preview
	if summary != "" {
		body = summary
	}

	suffix := "\n\n" +
		"React below, or **reply** to this " +
		"message to type something custom."

	maxBody := embedDescLimit - len(suffix)
	body = sanitize.Truncate(body, maxBody)

	return &discordgo.MessageEmbed{
		Title: fmt.Sprintf(
			"Session %d: Claude is waiting...",
			sessionNum,
		),
		Description: body + suffix,
		Color:       ColorWaiting,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf(
				"Session: %s #%s",
				projectName, shortID,
			),
		},
		Timestamp: time.Now().Format(
			time.RFC3339),
	}
}

// SendNotification sends an idle notification DM with
// reaction-based quick replies. If summary is non-empty,
// it replaces the raw preview in the embed body.
func (c *Client) SendNotification(
	projectName string,
	shortID string,
	preview string,
	summary string,
	sessionNum int,
) (string, error) {
	if err := c.ensureDMChannel(); err != nil {
		return "", err
	}
	if err := c.checkRateLimit(); err != nil {
		return "", err
	}

	embed := buildNotificationEmbed(
		projectName, shortID, preview, summary,
		sessionNum,
	)
	msg, err := c.session.ChannelMessageSendEmbed(
		c.dmChannel, embed,
	)
	if err != nil {
		c.handleRateLimit(err)
		return "", fmt.Errorf("send DM: %w", err)
	}

	if reactErr := c.AddReactionsTo(
		c.dmChannel, msg.ID,
	); reactErr != nil {
		log.Printf(
			"failed to add reactions: %v", reactErr,
		)
	}

	c.validator.SetNotificationTime(time.Now())
	return msg.ID, nil
}

// SendChannelNotification sends a notification to a
// specific channel instead of the DM channel.
func (c *Client) SendChannelNotification(
	channelID string,
	projectName string,
	shortID string,
	preview string,
	summary string,
	sessionNum int,
) (string, error) {
	if err := c.checkRateLimit(); err != nil {
		return "", err
	}

	embed := buildNotificationEmbed(
		projectName, shortID, preview, summary,
		sessionNum,
	)
	msg, err := c.session.ChannelMessageSendEmbed(
		channelID, embed,
	)
	if err != nil {
		c.handleRateLimit(err)
		return "", fmt.Errorf(
			"send channel notification: %w", err)
	}

	if reactErr := c.AddReactionsTo(
		channelID, msg.ID,
	); reactErr != nil {
		log.Printf(
			"failed to add reactions: %v", reactErr,
		)
	}

	c.validator.SetNotificationTime(time.Now())
	return msg.ID, nil
}

// DeleteChannelMessage deletes a message from any
// channel by ID.
func (c *Client) DeleteChannelMessage(
	channelID, msgID string,
) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	err := c.session.ChannelMessageDelete(
		channelID, msgID,
	)
	c.handleRateLimit(err)
	return err
}

// SendHint sends a plain text message (not an embed) to
// the DM channel, e.g. to tell the user to use Discord's
// Reply feature.
func (c *Client) SendHint(text string) error {
	if err := c.ensureDMChannel(); err != nil {
		return err
	}
	return c.SendHintTo(c.dmChannel, text)
}

// SendHintTo sends a plain text message to any channel.
func (c *Client) SendHintTo(
	channelID, text string,
) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	_, err := c.session.ChannelMessageSend(
		channelID, text,
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

// ClearNotificationMessages scans the DM channel for
// notification embeds and deletes them. Paginates
// through messages up to 14 days old. Uses bulk delete
// when possible (messages < 14 days old, 2-100 at a
// time). Returns the number of messages deleted.
func (c *Client) ClearNotificationMessages(
	sessionFilter string,
) (int, error) {
	if err := c.checkRateLimit(); err != nil {
		return 0, err
	}
	if err := c.ensureDMChannel(); err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-14 * 24 * time.Hour)
	var toDelete []string
	beforeID := ""

	// Paginate through DM history.
	for {
		msgs, err := c.session.ChannelMessages(
			c.dmChannel, 100, beforeID, "", "",
		)
		c.handleRateLimit(err)
		if err != nil {
			return 0, fmt.Errorf(
				"fetch DM messages: %w", err)
		}
		if len(msgs) == 0 {
			break
		}

		pastCutoff := false
		for _, msg := range msgs {
			// Stop if we've gone past the 14-day
			// bulk-delete window.
			if msg.Timestamp.Before(cutoff) {
				pastCutoff = true
				break
			}

			if msg.Author == nil {
				continue
			}
			if msg.Author.ID == c.userID {
				continue
			}
			if !isNotificationEmbed(
				msg, sessionFilter,
			) {
				continue
			}
			toDelete = append(toDelete, msg.ID)
		}

		if pastCutoff {
			break
		}
		beforeID = msgs[len(msgs)-1].ID
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	// Bulk delete requires 2+ messages and only works
	// for messages < 14 days old. Single messages use
	// regular delete.
	deleted := 0
	if len(toDelete) == 1 {
		err := c.session.ChannelMessageDelete(
			c.dmChannel, toDelete[0],
		)
		c.handleRateLimit(err)
		if err == nil {
			deleted = 1
		}
	} else {
		// Bulk delete in chunks of 100.
		for i := 0; i < len(toDelete); i += 100 {
			end := i + 100
			if end > len(toDelete) {
				end = len(toDelete)
			}
			chunk := toDelete[i:end]
			if len(chunk) < 2 {
				err := c.session.ChannelMessageDelete(
					c.dmChannel, chunk[0],
				)
				c.handleRateLimit(err)
				if err == nil {
					deleted++
				}
				continue
			}
			err := c.session.ChannelMessagesBulkDelete(
				c.dmChannel, chunk,
			)
			c.handleRateLimit(err)
			if err != nil {
				log.Printf(
					"bulk delete failed: %v", err)
				// Fall back to individual deletes.
				for _, id := range chunk {
					err := c.session.ChannelMessageDelete(
						c.dmChannel, id,
					)
					c.handleRateLimit(err)
					if err == nil {
						deleted++
					}
				}
			} else {
				deleted += len(chunk)
			}
		}
	}
	return deleted, nil
}

// isNotificationEmbed checks if a message contains a
// claude-notify notification embed. If sessionFilter is
// non-empty, the embed's footer must contain that session
// ID.
func isNotificationEmbed(
	msg *discordgo.Message,
	sessionFilter string,
) bool {
	for _, embed := range msg.Embeds {
		if embed.Title == "" ||
			!strings.HasPrefix(
				embed.Title, "Session ") {
			continue
		}
		if sessionFilter == "" {
			return true
		}
		if embed.Footer == nil {
			continue
		}
		parts := strings.SplitN(
			embed.Footer.Text, "#", 2)
		if len(parts) == 2 &&
			strings.EqualFold(
				strings.TrimSpace(parts[1]),
				sessionFilter,
			) {
			return true
		}
	}
	return false
}

// Reaction emojis used for quick replies.
const (
	ReactionYes  = "✅"
	ReactionNo   = "❌"
	ReactionLook = "👀"
)

// Embed colors for session status.
const (
	ColorWorking      = 0x2ECC71 // green
	ColorWaiting      = 0xF1C40F // yellow
	ColorDisconnected = 0xE74C3C // red
)

// reactionMap maps reaction emojis to reply text.
var reactionMap = map[string]string{
	ReactionYes: "Yes or Continue, decide which " +
		"answer makes more sense based on context.",
	ReactionNo: "No",
	ReactionLook: "Show me additional context on this",
}

// ExpandReaction returns the reply text for a reaction
// emoji, or empty string if not recognized.
func ExpandReaction(emoji string) string {
	return reactionMap[emoji]
}

// AddReactionsTo adds the quick-reply reaction emojis
// to a message in any channel.
func (c *Client) AddReactionsTo(
	channelID, msgID string,
) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	for _, emoji := range []string{
		ReactionYes, ReactionNo, ReactionLook,
	} {
		err := c.session.MessageReactionAdd(
			channelID, msgID, emoji,
		)
		if err != nil {
			c.handleRateLimit(err)
			return fmt.Errorf(
				"add reaction %s: %w",
				emoji, err,
			)
		}
	}
	return nil
}

// RemoveBotReactions removes only the bot's own
// reactions per-emoji, preserving user reactions.
func (c *Client) RemoveBotReactions(
	channelID, msgID string,
) error {
	if c.botUserID == "" {
		return nil
	}
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	for _, emoji := range []string{
		ReactionYes, ReactionNo, ReactionLook,
	} {
		err := c.session.MessageReactionRemove(
			channelID, msgID,
			emoji, c.botUserID,
		)
		if err != nil {
			c.handleRateLimit(err)
		}
	}
	return nil
}

// EditEmbed updates the title and color of a message's
// first embed. Channel-aware replacement for the old
// EditEmbedColor method.
func (c *Client) EditEmbed(
	channelID, msgID, title string, color int,
) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	msg, err := c.session.ChannelMessage(
		channelID, msgID,
	)
	if err != nil {
		c.handleRateLimit(err)
		return fmt.Errorf(
			"fetch message: %w", err)
	}
	if len(msg.Embeds) == 0 {
		return nil
	}
	embed := msg.Embeds[0]
	embed.Title = title
	embed.Color = color
	_, err = c.session.ChannelMessageEditEmbed(
		channelID, msgID, embed,
	)
	if err != nil {
		c.handleRateLimit(err)
	}
	return err
}

// AckReply reacts with ✅ on a user's reply message
// to acknowledge receipt.
func (c *Client) AckReply(
	channelID, msgID string,
) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	err := c.session.MessageReactionAdd(
		channelID, msgID, ReactionYes,
	)
	if err != nil {
		c.handleRateLimit(err)
	}
	return err
}

// NackReply reacts with ❌ on a message to indicate
// delivery failure.
func (c *Client) NackReply(
	channelID, msgID string,
) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	err := c.session.MessageReactionAdd(
		channelID, msgID, ReactionNo,
	)
	if err != nil {
		c.handleRateLimit(err)
	}
	return err
}

// --- Gateway event handlers ---

func (c *Client) onReady(
	s *discordgo.Session, r *discordgo.Ready,
) {
	c.appID = r.Application.ID
	c.botUserID = r.User.ID
	log.Printf(
		"gateway connected as %s (app: %s)",
		r.User.Username, c.appID,
	)
}

func (c *Client) isAllowedUser(userID string) bool {
	if c.IsAllowed != nil {
		return c.IsAllowed(userID)
	}
	return userID == c.userID
}

func (c *Client) onMessageCreate(
	s *discordgo.Session,
	m *discordgo.MessageCreate,
) {
	if m.Author == nil || m.Author.Bot {
		return
	}
	if !c.isAllowedUser(m.Author.ID) {
		return
	}

	ev := ReplyEvent{
		Content:   m.Content,
		MessageID: m.ID,
		ChannelID: m.ChannelID,
		UserID:    m.Author.ID,
	}
	if m.MessageReference != nil {
		ev.RefMessageID =
			m.MessageReference.MessageID
	}

	select {
	case c.Replies <- ev:
	default:
		log.Print("reply channel full, dropping")
	}
}

func (c *Client) onMessageReactionAdd(
	s *discordgo.Session,
	r *discordgo.MessageReactionAdd,
) {
	if !c.isAllowedUser(r.UserID) {
		return
	}
	emoji := r.Emoji.Name
	if ExpandReaction(emoji) == "" {
		return
	}

	select {
	case c.Reactions <- ReactionEvent{
		MessageID: r.MessageID,
		ChannelID: r.ChannelID,
		Emoji:     emoji,
		UserID:    r.UserID,
	}:
	default:
		log.Print("reaction channel full, dropping")
	}
}

func (c *Client) onInteractionCreate(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
) {
	if i.Type !=
		discordgo.InteractionApplicationCommand {
		return
	}

	// Slash commands are owner-only.
	var callerID string
	if i.Member != nil {
		callerID = i.Member.User.ID
	} else if i.User != nil {
		callerID = i.User.ID
	}
	if callerID != c.userID {
		_ = c.RespondToInteraction(
			i.Interaction,
			"You don't have permission to "+
				"use this command.",
		)
		return
	}

	data := i.ApplicationCommandData()

	switch data.Name {
	case "clear":
		c.handleClearInteraction(data, i)
	case "configure":
		c.handleConfigureInteraction(data, i)
	}
}

func (c *Client) handleClearInteraction(
	data discordgo.ApplicationCommandInteractionData,
	i *discordgo.InteractionCreate,
) {
	var sessionID string
	for _, opt := range data.Options {
		if opt.Name == "session" {
			sessionID = opt.StringValue()
		}
	}
	select {
	case c.Clears <- ClearCommand{
		SessionID:   sessionID,
		Interaction: i.Interaction,
	}:
	default:
		log.Print("clear channel full, dropping")
	}
}

func (c *Client) handleConfigureInteraction(
	data discordgo.ApplicationCommandInteractionData,
	i *discordgo.InteractionCreate,
) {
	if len(data.Options) == 0 {
		_ = c.RespondToInteraction(
			i.Interaction, "Usage: /configure user|channel")
		return
	}

	sub := data.Options[0]
	var action, value string
	if len(sub.Options) > 0 {
		action = sub.Options[0].Name
		if len(sub.Options[0].Options) > 0 {
			value = sub.Options[0].Options[0].
				StringValue()
		}
	}

	select {
	case c.Configures <- ConfigureCommand{
		Subcommand:  sub.Name,
		Action:      action,
		Value:       value,
		Interaction: i.Interaction,
	}:
	default:
		log.Print(
			"configure channel full, dropping")
	}
}

// --- Slash command registration ---

// RegisterCommands registers slash commands (/clear
// and /configure) with Discord. Must be called after
// the gateway is ready (appID is set).
func (c *Client) RegisterCommands() error {
	if c.appID == "" {
		return fmt.Errorf(
			"appID not set (gateway not ready)")
	}

	commands := []*discordgo.ApplicationCommand{
		{
			Name: "clear",
			Description: "Clear claude-notify " +
				"notifications",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type: discordgo.
						ApplicationCommandOptionString,
					Name:        "session",
					Description: "Session ID (omit for all)",
				},
			},
		},
		{
			Name:        "configure",
			Description: "Configure claude-notify",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type: discordgo.
						ApplicationCommandOptionSubCommandGroup,
					Name:        "user",
					Description: "Manage allowed users",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type: discordgo.
								ApplicationCommandOptionSubCommand,
							Name:        "add",
							Description: "Allow a user to react/reply",
							Options: []*discordgo.ApplicationCommandOption{
								{
									Type: discordgo.
										ApplicationCommandOptionString,
									Name:        "id",
									Description: "Discord user ID",
									Required:    true,
								},
							},
						},
						{
							Type: discordgo.
								ApplicationCommandOptionSubCommand,
							Name:        "remove",
							Description: "Remove a user",
							Options: []*discordgo.ApplicationCommandOption{
								{
									Type: discordgo.
										ApplicationCommandOptionString,
									Name:        "id",
									Description: "Discord user ID",
									Required:    true,
								},
							},
						},
						{
							Type: discordgo.
								ApplicationCommandOptionSubCommand,
							Name:        "list",
							Description: "List allowed users",
						},
					},
				},
				{
					Type: discordgo.
						ApplicationCommandOptionSubCommandGroup,
					Name:        "channel",
					Description: "Manage notification channel",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type: discordgo.
								ApplicationCommandOptionSubCommand,
							Name:        "set",
							Description: "Set notification channel",
							Options: []*discordgo.ApplicationCommandOption{
								{
									Type: discordgo.
										ApplicationCommandOptionString,
									Name:        "id",
									Description: "Channel ID",
									Required:    true,
								},
							},
						},
						{
							Type: discordgo.
								ApplicationCommandOptionSubCommand,
							Name:        "clear",
							Description: "Remove channel (use DM)",
						},
						{
							Type: discordgo.
								ApplicationCommandOptionSubCommand,
							Name:        "show",
							Description: "Show current channel",
						},
					},
				},
			},
		},
	}

	for _, cmd := range commands {
		_, err := c.session.ApplicationCommandCreate(
			c.appID, "", cmd,
		)
		if err != nil {
			return fmt.Errorf(
				"register /%s command: %w",
				cmd.Name, err)
		}
		log.Printf("registered /%s slash command",
			cmd.Name)
	}
	return nil
}

// RespondToInteraction sends an ephemeral response to a
// slash command interaction.
func (c *Client) RespondToInteraction(
	interaction interface{}, content string,
) error {
	i, ok := interaction.(*discordgo.Interaction)
	if !ok {
		return fmt.Errorf("invalid interaction type")
	}
	return c.session.InteractionRespond(i,
		&discordgo.InteractionResponse{
			Type: discordgo.
				InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
				Flags: discordgo.
					MessageFlagsEphemeral,
			},
		},
	)
}

// Close shuts down the discordgo session and gateway.
func (c *Client) Close() {
	if c.session != nil {
		_ = c.session.Close()
	}
}
