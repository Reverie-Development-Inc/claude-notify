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
}

// ReactionEvent is sent when a user reacts to a
// notification message.
type ReactionEvent struct {
	MessageID string
	Emoji     string
}

// ClearCommand is sent when a user invokes the
// /clear slash command.
type ClearCommand struct {
	SessionID   string
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

	// Gateway event channels — daemon selects on these.
	Replies   chan ReplyEvent
	Reactions chan ReactionEvent
	Clears    chan ClearCommand

	// appID is the bot's application ID, needed for
	// slash command registration.
	appID string
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

	// Minimal intents: DM messages + DM reactions.
	s.Identify.Intents =
		discordgo.IntentsDirectMessages |
			discordgo.IntentsDirectMessageReactions

	c := &Client{
		session:   s,
		userID:    userID,
		validator: NewValidator(userID),
		Replies:   make(chan ReplyEvent, 16),
		Reactions: make(chan ReactionEvent, 16),
		Clears:    make(chan ClearCommand, 4),
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

// SendNotification sends an idle notification DM with
// reaction-based quick replies. If summary is non-empty,
// it replaces the raw preview in the embed body.
func (c *Client) SendNotification(
	projectName string,
	shortID string,
	preview string,
	summary string,
) (string, error) {
	if err := c.ensureDMChannel(); err != nil {
		return "", err
	}
	if err := c.checkRateLimit(); err != nil {
		return "", err
	}

	body := preview
	if summary != "" {
		body = summary
	}

	suffix := "\n\n" +
		"React below, or **reply** to this " +
		"message to type something custom."

	// Discord embed descriptions are capped at 4096
	// characters. Truncate the body to fit within
	// that limit, reserving space for the suffix.
	maxBody := embedDescLimit - len(suffix)
	body = sanitize.Truncate(body, maxBody)

	desc := body + suffix

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf(
			"Claude waiting (%s)", projectName,
		),
		Description: desc,
		Color:       0xD4A574,
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
		c.handleRateLimit(err)
		return "", fmt.Errorf("send DM: %w", err)
	}

	// Add quick-reply reactions
	if reactErr := c.AddReactions(msg.ID); reactErr != nil {
		log.Printf(
			"failed to add reactions: %v", reactErr,
		)
	}

	c.validator.SetNotificationTime(time.Now())
	return msg.ID, nil
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
		if embed.Footer == nil {
			continue
		}
		// Exact match on session ID after "#".
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

// AddReactions adds the quick-reply reaction emojis to
// a message. Reactions are added in order.
func (c *Client) AddReactions(msgID string) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	for _, emoji := range []string{
		ReactionYes, ReactionNo, ReactionLook,
	} {
		err := c.session.MessageReactionAdd(
			c.dmChannel, msgID, emoji,
		)
		if err != nil {
			c.handleRateLimit(err)
			return fmt.Errorf(
				"add reaction %s: %w", emoji, err,
			)
		}
	}
	return nil
}

// RemoveAllReactions removes all reactions from a
// message (clears the reaction bar entirely).
func (c *Client) RemoveAllReactions(
	msgID string,
) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	err := c.session.MessageReactionsRemoveAll(
		c.dmChannel, msgID,
	)
	if err != nil {
		c.handleRateLimit(err)
	}
	return err
}

// EditEmbedColor edits a message to change its embed
// color. Preserves existing embed content.
func (c *Client) EditEmbedColor(
	msgID string, color int,
) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	msg, err := c.session.ChannelMessage(
		c.dmChannel, msgID,
	)
	if err != nil {
		c.handleRateLimit(err)
		return fmt.Errorf("fetch message: %w", err)
	}
	if len(msg.Embeds) == 0 {
		return nil
	}
	embed := msg.Embeds[0]
	embed.Color = color
	_, err = c.session.ChannelMessageEditEmbed(
		c.dmChannel, msgID, embed,
	)
	if err != nil {
		c.handleRateLimit(err)
	}
	return err
}

// AckReply reacts with ✅ on a user's reply message
// to acknowledge receipt.
func (c *Client) AckReply(msgID string) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	err := c.session.MessageReactionAdd(
		c.dmChannel, msgID, ReactionYes,
	)
	if err != nil {
		c.handleRateLimit(err)
	}
	return err
}

// NackReply reacts with ❌ on a message to indicate
// delivery failure.
func (c *Client) NackReply(msgID string) error {
	if err := c.checkRateLimit(); err != nil {
		return err
	}
	err := c.session.MessageReactionAdd(
		c.dmChannel, msgID, ReactionNo,
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
	log.Printf(
		"gateway connected as %s (app: %s)",
		r.User.Username, c.appID,
	)
}

func (c *Client) onMessageCreate(
	s *discordgo.Session,
	m *discordgo.MessageCreate,
) {
	if m.Author == nil || m.Author.ID != c.userID {
		return
	}
	ch, err := s.State.Channel(m.ChannelID)
	if err != nil || ch.Type != discordgo.ChannelTypeDM {
		return
	}

	ev := ReplyEvent{
		Content:   m.Content,
		MessageID: m.ID,
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
	if r.UserID != c.userID {
		return
	}
	// Only process reactions in the DM channel.
	if c.dmChannel != "" &&
		r.ChannelID != c.dmChannel {
		return
	}
	emoji := r.Emoji.Name
	if ExpandReaction(emoji) == "" {
		return
	}

	select {
	case c.Reactions <- ReactionEvent{
		MessageID: r.MessageID,
		Emoji:     emoji,
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
	data := i.ApplicationCommandData()
	if data.Name != "clear" {
		return
	}

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

// --- Slash command registration ---

// RegisterCommands registers the /clear slash command
// with Discord. Must be called after the gateway is
// ready (appID is set).
func (c *Client) RegisterCommands() error {
	if c.appID == "" {
		return fmt.Errorf(
			"appID not set (gateway not ready)")
	}

	cmd := &discordgo.ApplicationCommand{
		Name:        "clear",
		Description: "Clear claude-notify notifications",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type: discordgo.
					ApplicationCommandOptionString,
				Name:        "session",
				Description: "Session ID (omit for all)",
				Required:    false,
			},
		},
	}

	_, err := c.session.ApplicationCommandCreate(
		c.appID, "", cmd,
	)
	if err != nil {
		return fmt.Errorf(
			"register /clear command: %w", err)
	}
	log.Print("registered /clear slash command")
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
