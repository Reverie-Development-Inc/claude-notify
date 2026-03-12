// Package daemon watches session metadata files, sends
// Discord DMs, handles gateway events, and writes replies
// to session FIFOs.
package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Reverie-Development-Inc/claude-notify/internal/config"
	"github.com/Reverie-Development-Inc/claude-notify/internal/discord"
	"github.com/Reverie-Development-Inc/claude-notify/internal/session"
)

// Daemon is the orchestration brain of claude-notify. It
// periodically scans session metadata files, sends Discord
// DMs when sessions have been waiting too long, and handles
// gateway events to inject replies back into sessions.
type Daemon struct {
	cfg          *config.Config
	discord      *discord.Client
	stateDir     string
	pollInterval time.Duration

	// msgIDCache maps NotificationMsgID → Metadata.
	// Rebuilt every tick() to avoid repeated dir scans
	// on reaction/reply events.
	msgIDCache map[string]*session.Metadata
}

// New creates a Daemon with the given config and Discord
// client.
func New(
	cfg *config.Config, dc *discord.Client,
) *Daemon {
	return &Daemon{
		cfg:          cfg,
		discord:      dc,
		stateDir:     cfg.StateDir(),
		pollInterval: 10 * time.Second,
	}
}

// Run starts the daemon loop. It cleans stale sessions on
// startup, registers slash commands, then selects on gateway
// event channels and a tick timer until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(d.stateDir, 0700); err != nil {
		return fmt.Errorf("mkdir state: %w", err)
	}

	d.cleanStaleSessions()

	// Load discord runtime config and set up user
	// authorization callback.
	config.LoadDiscordRuntimeConfig(d.stateDir)
	d.refreshAllowedUsers()

	// Register slash commands after gateway is ready.
	go func() {
		for i := 0; i < 10; i++ {
			if err := d.discord.RegisterCommands(); err != nil {
				log.Printf(
					"register commands (attempt %d): %v",
					i+1, err,
				)
				time.Sleep(2 * time.Second)
				continue
			}
			return
		}
		log.Print(
			"WARNING: failed to register slash " +
				"commands after 10 attempts")
	}()

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	log.Printf("daemon started, watching %s", d.stateDir)

	for {
		select {
		case <-ctx.Done():
			log.Print("daemon shutting down")
			return nil

		case <-ticker.C:
			d.tick()

		case ev := <-d.discord.Replies:
			d.handleReply(ev)

		case ev := <-d.discord.Reactions:
			d.handleReaction(ev)

		case cmd := <-d.discord.Clears:
			d.handleClear(cmd)

		case cmd := <-d.discord.Configures:
			d.handleConfigure(cmd)
		}
	}
}

// tick runs one iteration of the daemon loop: list all
// sessions, clean dead ones, dismiss stale notifications,
// and send notifications for sessions waiting long enough.
func (d *Daemon) tick() {
	sessions, err := session.List(d.stateDir)
	if err != nil {
		log.Printf("list sessions: %v", err)
		return
	}

	// Rebuild message ID cache for O(1) lookups.
	cache := make(map[string]*session.Metadata)
	for _, meta := range sessions {
		if meta.NotificationMsgID != "" {
			cache[meta.NotificationMsgID] = meta
		}
	}
	d.msgIDCache = cache

	for _, meta := range sessions {
		// Clean up dead sessions — auto-clear Discord
		// messages instead of just greying them out.
		if !isProcessAlive(meta.PID) {
			d.autoCleanNotification(meta)
			path := filepath.Join(
				d.stateDir,
				fmt.Sprintf("%d.json", meta.PID),
			)
			_ = os.Remove(path)
			continue
		}

		// User returned to session — grey out the
		// stale Discord notification.
		if meta.Status == session.StatusActive &&
			meta.NotificationSent &&
			meta.NotificationMsgID != "" {
			// Was this a FIFO injection echo?
			// If LastInjectedAt is within the last
			// 30s, this is the hook firing from our
			// own FIFO write — don't dismiss or
			// exit remote mode.
			if meta.RemoteMode &&
				meta.LastInjectedAt > 0 &&
				time.Since(
					time.Unix(
						meta.LastInjectedAt, 0,
					),
				) < 30*time.Second {
				continue
			}
			d.dismissNotification(meta)
			continue
		}

		delay := time.Duration(
			d.cfg.Notify.DelayMinutes,
		) * time.Minute

		if shouldNotify(meta, delay) {
			d.sendNotification(meta)
		}
	}
}

// remoteDebounce is the short delay used in remote mode
// instead of the full notification delay.
const remoteDebounce = 15 * time.Second

// shouldNotify returns true if the session is waiting, has
// not already been notified, and has been waiting longer
// than delay. In remote mode, uses a short debounce
// instead of the full delay.
func shouldNotify(
	meta *session.Metadata, delay time.Duration,
) bool {
	if meta.Status != session.StatusWaiting {
		return false
	}
	if meta.NotificationSent {
		return false
	}
	if meta.SkipNotification {
		return false
	}
	if meta.LastStop.IsZero() {
		return false
	}

	elapsed := time.Since(meta.LastStop)

	// Remote mode: use short debounce instead of
	// full delay.
	if meta.RemoteMode {
		return elapsed >= remoteDebounce
	}

	return elapsed >= delay
}

// sendNotification sends a Discord notification for the
// given session. Posts to configured channel if set,
// otherwise sends a DM.
func (d *Daemon) sendNotification(
	meta *session.Metadata,
) {
	projectName := filepath.Base(meta.CWD)
	drc := config.GetDiscordRuntimeConfig()

	var msgID string
	var err error

	if drc.NotificationChannel != "" {
		msgID, err = d.discord.SendChannelNotification(
			drc.NotificationChannel,
			projectName,
			meta.ShortID,
			meta.LastMessagePreview,
			meta.NotifySummary,
		)
		if err == nil {
			meta.NotificationChannelID =
				drc.NotificationChannel
			meta.NotificationChannelMsgID = msgID
		}
	} else {
		msgID, err = d.discord.SendNotification(
			projectName,
			meta.ShortID,
			meta.LastMessagePreview,
			meta.NotifySummary,
		)
	}

	if err != nil {
		log.Printf(
			"failed to send notification for %d: %v",
			meta.PID, err,
		)
		return
	}

	meta.NotificationSent = true
	meta.NotificationMsgID = msgID
	meta.ResponseDelivered = false
	metaPath := filepath.Join(
		d.stateDir,
		fmt.Sprintf("%d.json", meta.PID),
	)
	if err := session.Write(
		metaPath, meta,
	); err != nil {
		log.Printf(
			"failed to update metadata: %v", err,
		)
	}
	log.Printf(
		"sent notification for session %d "+
			"(msg: %s)", meta.PID, msgID,
	)
}

// handleReply processes an inbound reply from the gateway.
// It routes by reply-to reference when available, falls
// back to single-session routing, or sends a hint.
func (d *Daemon) handleReply(ev discord.ReplyEvent) {
	lower := strings.ToLower(
		strings.TrimSpace(ev.Content))
	if strings.HasPrefix(lower, "/clear") {
		return
	}

	if ev.RefMessageID != "" {
		meta := d.findSessionByMsgID(ev.RefMessageID)
		if meta != nil {
			if meta.ResponseDelivered {
				_ = d.discord.SendHint(
					"A response was already "+
						"delivered to this session.")
				return
			}
			d.deliverReplyFrom(
				meta, ev.Content, ev.MessageID,
			)
			return
		}
		_ = d.discord.SendHint(
			"Session not found. It may have " +
				"ended or been cleaned up.")
		return
	}

	sessions := d.notifiedSessions()
	if len(sessions) == 1 {
		if sessions[0].ResponseDelivered {
			_ = d.discord.SendHint(
				"A response was already " +
					"delivered to this session.")
			return
		}
		d.deliverReplyFrom(
			sessions[0], ev.Content, ev.MessageID,
		)
		return
	}

	_ = d.discord.SendHint(
		"Use Discord's **Reply** feature " +
			"(swipe left on mobile, right-click " +
			"→ Reply on desktop) on the " +
			"notification you want to respond to.")
}

// handleReaction processes an inbound reaction from the
// gateway. It looks up the session by message ID and
// delivers the expanded emoji text.
func (d *Daemon) handleReaction(
	ev discord.ReactionEvent,
) {
	meta := d.findSessionByMsgID(ev.MessageID)
	if meta == nil {
		return
	}

	// First-wins: reject if already delivered.
	if meta.ResponseDelivered {
		log.Printf(
			"reaction %s ignored — response "+
				"already delivered for session %d",
			ev.Emoji, meta.PID,
		)
		return
	}

	text := discord.ExpandReaction(ev.Emoji)
	if text == "" {
		return
	}

	log.Printf(
		"reaction %s from user on session %d",
		ev.Emoji, meta.PID,
	)
	d.deliverReply(meta, text)
}

// handleClear processes an inbound /clear slash command
// from the gateway. It clears notifications and responds
// to the interaction.
func (d *Daemon) handleClear(cmd discord.ClearCommand) {
	result := d.clearNotifications(cmd.SessionID)
	if err := d.discord.RespondToInteraction(
		cmd.Interaction, result,
	); err != nil {
		log.Printf("respond to /clear: %v", err)
	}
	log.Printf("clear command: %s", result)
}

// findSessionByMsgID returns the session whose
// NotificationMsgID matches the given Discord message ID,
// or nil if no match is found.
func (d *Daemon) findSessionByMsgID(
	msgID string,
) *session.Metadata {
	if d.msgIDCache != nil {
		return d.msgIDCache[msgID]
	}
	// Fallback if cache not yet built (pre-first tick).
	sessions, err := session.List(d.stateDir)
	if err != nil {
		return nil
	}
	for _, meta := range sessions {
		if meta.NotificationMsgID == msgID {
			return meta
		}
	}
	return nil
}

// notifiedSessions returns all sessions that currently
// have an active notification in Discord.
func (d *Daemon) notifiedSessions() []*session.Metadata {
	sessions, err := session.List(d.stateDir)
	if err != nil {
		return nil
	}
	var notified []*session.Metadata
	for _, meta := range sessions {
		if meta.NotificationSent &&
			meta.NotificationMsgID != "" {
			notified = append(notified, meta)
		}
	}
	return notified
}

// ColorResolved is the grey color for handled
// notification embeds.
const ColorResolved = 0x95A5A6

// deliverReply injects a reply into the session via
// FIFO, acknowledges it in Discord, and enters remote
// mode.
func (d *Daemon) deliverReply(
	meta *session.Metadata,
	content string,
) {
	d.deliverReplyFrom(meta, content, "")
}

// deliverReplyFrom injects a reply and optionally
// acknowledges a specific user message. If replyMsgID
// is empty, the reply came from a reaction.
func (d *Daemon) deliverReplyFrom(
	meta *session.Metadata,
	content string,
	replyMsgID string,
) {
	// Prefix with [discord] for Claude awareness
	injected := "[discord] " + content

	err := writeToFIFO(meta.FIFO, injected)
	if err != nil {
		log.Printf(
			"FIFO write failed for %d: %v",
			meta.PID, err,
		)
		if replyMsgID != "" {
			_ = d.discord.NackReply(
				d.discord.DMChannelID(),
				replyMsgID,
			)
		}
		_ = d.discord.SendHint(
			"Session is no longer active.",
		)
		return
	}

	// Acknowledge the user's reply message
	if replyMsgID != "" {
		_ = d.discord.AckReply(
			d.discord.DMChannelID(),
			replyMsgID,
		)
	}

	// Resolve the notification message
	if meta.NotificationMsgID != "" {
		chID := d.discord.DMChannelID()
		if meta.NotificationChannelID != "" {
			chID = meta.NotificationChannelID
		}
		_ = d.discord.RemoveBotReactions(
			chID, meta.NotificationMsgID,
		)
		_ = d.discord.EditEmbed(
			chID, meta.NotificationMsgID,
			"Resolved", ColorResolved,
		)
	}

	// Update metadata: enter remote mode, mark delivered
	meta.NotificationSent = false
	meta.NotificationMsgID = ""
	meta.NotificationChannelMsgID = ""
	meta.NotificationChannelID = ""
	meta.ResponseDelivered = true
	meta.Status = session.StatusActive
	meta.RemoteMode = true
	meta.LastInjectedAt = time.Now().Unix()
	meta.SkipNotification = false
	meta.NotifySummary = ""

	metaPath := filepath.Join(
		d.stateDir,
		fmt.Sprintf("%d.json", meta.PID),
	)
	if err := session.Write(
		metaPath, meta,
	); err != nil {
		log.Printf(
			"failed to update metadata: %v", err,
		)
	}

	log.Printf(
		"delivered reply to session %d, "+
			"remote mode ON", meta.PID,
	)
}

// clearNotifications clears notifications in two ways:
// 1. Resets metadata for tracked sessions
// 2. Scans the DM channel for orphaned notification
//    embeds and deletes them directly
// Returns a human-readable result message.
func (d *Daemon) clearNotifications(
	sessionID string,
) string {
	// Phase 1: clear tracked metadata.
	sessions, _ := session.List(d.stateDir)
	for _, meta := range sessions {
		if !meta.NotificationSent ||
			meta.NotificationMsgID == "" {
			continue
		}
		if sessionID != "" &&
			!strings.EqualFold(
				meta.ShortID, sessionID) {
			continue
		}
		meta.NotificationSent = false
		meta.NotificationMsgID = ""
		path := filepath.Join(
			d.stateDir,
			fmt.Sprintf("%d.json", meta.PID),
		)
		_ = session.Write(path, meta)
	}

	// Phase 2: scan DM channel and delete notification
	// embeds (catches orphaned messages too).
	deleted, err := d.discord.ClearNotificationMessages(
		sessionID,
	)
	if err != nil {
		log.Printf("clear notifications: %v", err)
		return "Error clearing notifications."
	}

	switch deleted {
	case 0:
		return "No notifications found to clear."
	case 1:
		return "Cleared 1 notification."
	default:
		return fmt.Sprintf(
			"Cleared %d notifications.", deleted)
	}
}

// dismissNotification greys out a pending Discord
// notification and clears reactions. Called when the
// user returns to the session or the session dies
// before they reply.
func (d *Daemon) dismissNotification(
	meta *session.Metadata,
) {
	if meta.NotificationMsgID != "" {
		chID := d.discord.DMChannelID()
		if meta.NotificationChannelID != "" {
			chID = meta.NotificationChannelID
		}
		_ = d.discord.RemoveBotReactions(
			chID, meta.NotificationMsgID,
		)
		_ = d.discord.EditEmbed(
			chID, meta.NotificationMsgID,
			"Resolved", ColorResolved,
		)
	}
	meta.NotificationSent = false
	meta.NotificationMsgID = ""
	meta.RemoteMode = false

	metaPath := filepath.Join(
		d.stateDir,
		fmt.Sprintf("%d.json", meta.PID),
	)
	if err := session.Write(
		metaPath, meta,
	); err != nil {
		log.Printf(
			"failed to update metadata: %v", err,
		)
	}
	log.Printf(
		"dismissed notification for session %d",
		meta.PID,
	)
}

// cleanStaleSessions removes metadata files for processes
// that are no longer running. Called once at daemon startup.
func (d *Daemon) cleanStaleSessions() {
	sessions, err := session.List(d.stateDir)
	if err != nil {
		return
	}
	for _, meta := range sessions {
		if !isProcessAlive(meta.PID) {
			path := filepath.Join(
				d.stateDir,
				fmt.Sprintf("%d.json", meta.PID),
			)
			_ = os.Remove(path)
			log.Printf(
				"cleaned stale session PID %d",
				meta.PID,
			)
		}
	}
}

// autoCleanNotification deletes Discord notification
// messages when a session ends (process dies). This is
// fire-and-forget — failure to delete does not block
// session cleanup.
func (d *Daemon) autoCleanNotification(
	meta *session.Metadata,
) {
	if meta.NotificationMsgID != "" {
		err := d.discord.DeleteMessage(
			meta.NotificationMsgID,
		)
		if err != nil {
			log.Printf(
				"auto-clear DM msg %s: %v",
				meta.NotificationMsgID, err,
			)
		}
	}
	if meta.NotificationChannelMsgID != "" &&
		meta.NotificationChannelID != "" {
		err := d.discord.DeleteChannelMessage(
			meta.NotificationChannelID,
			meta.NotificationChannelMsgID,
		)
		if err != nil {
			log.Printf(
				"auto-clear channel msg %s: %v",
				meta.NotificationChannelMsgID, err,
			)
		}
	}
	log.Printf(
		"auto-cleared notifications for dead "+
			"session %d", meta.PID,
	)
}

// handleConfigure processes /configure slash commands.
func (d *Daemon) handleConfigure(
	cmd discord.ConfigureCommand,
) {
	drc := config.GetDiscordRuntimeConfig()
	var response string

	switch cmd.Subcommand {
	case "user":
		response = d.handleConfigureUser(
			drc, cmd.Action, cmd.Value)
	case "channel":
		response = d.handleConfigureChannel(
			drc, cmd.Action, cmd.Value)
	default:
		response = "Unknown subcommand: " +
			cmd.Subcommand
	}

	if err := d.discord.RespondToInteraction(
		cmd.Interaction, response,
	); err != nil {
		log.Printf("respond to /configure: %v", err)
	}
}

func (d *Daemon) handleConfigureUser(
	drc *config.DiscordRuntimeConfig,
	action, value string,
) string {
	switch action {
	case "add":
		if value == "" {
			return "User ID required."
		}
		if !drc.AddUser(value) {
			return "User " + value +
				" is already allowed."
		}
		if err := config.SaveDiscordRuntimeConfig(
			d.stateDir, drc,
		); err != nil {
			return "Error saving config: " +
				err.Error()
		}
		d.refreshAllowedUsers()
		return "Added user " + value +
			" to allowed list."
	case "remove":
		if value == "" {
			return "User ID required."
		}
		if !drc.RemoveUser(value) {
			return "User " + value + " not found."
		}
		if err := config.SaveDiscordRuntimeConfig(
			d.stateDir, drc,
		); err != nil {
			return "Error saving config: " +
				err.Error()
		}
		d.refreshAllowedUsers()
		return "Removed user " + value + "."
	case "list":
		if len(drc.AllowedUsers) == 0 {
			return "No additional users " +
				"(owner always allowed)."
		}
		return "Allowed users: " +
			strings.Join(drc.AllowedUsers, ", ")
	default:
		return "Unknown action: " + action
	}
}

func (d *Daemon) handleConfigureChannel(
	drc *config.DiscordRuntimeConfig,
	action, value string,
) string {
	switch action {
	case "set":
		if value == "" {
			return "Channel ID required."
		}
		drc.NotificationChannel = value
		if err := config.SaveDiscordRuntimeConfig(
			d.stateDir, drc,
		); err != nil {
			return "Error saving config: " +
				err.Error()
		}
		return "Notifications will post to " +
			"channel " + value + "."
	case "clear":
		drc.NotificationChannel = ""
		if err := config.SaveDiscordRuntimeConfig(
			d.stateDir, drc,
		); err != nil {
			return "Error saving config: " +
				err.Error()
		}
		return "Notifications will use DM."
	case "show":
		if drc.NotificationChannel == "" {
			return "No channel set (using DM)."
		}
		return "Current channel: " +
			drc.NotificationChannel
	default:
		return "Unknown action: " + action
	}
}

// refreshAllowedUsers updates the Discord client's
// IsAllowed callback with the current runtime config.
func (d *Daemon) refreshAllowedUsers() {
	drc := config.GetDiscordRuntimeConfig()
	ownerID := d.cfg.Discord.UserID
	d.discord.IsAllowed = func(
		userID string,
	) bool {
		return drc.IsUserAllowed(userID, ownerID)
	}
}
