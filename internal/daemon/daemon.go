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

	for _, meta := range sessions {
		// Clean up dead sessions.
		if !isProcessAlive(meta.PID) {
			d.dismissNotification(meta)
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

// sendNotification sends a Discord DM for the given session
// and updates the metadata file with the notification state.
func (d *Daemon) sendNotification(
	meta *session.Metadata,
) {
	projectName := filepath.Base(meta.CWD)

	msgID, err := d.discord.SendNotification(
		projectName,
		meta.ShortID,
		meta.LastMessagePreview,
		meta.NotifySummary,
	)
	if err != nil {
		log.Printf(
			"failed to send notification for %d: %v",
			meta.PID, err,
		)
		return
	}

	meta.NotificationSent = true
	meta.NotificationMsgID = msgID
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
			_ = d.discord.NackReply(replyMsgID)
		}
		_ = d.discord.SendHint(
			"Session is no longer active.",
		)
		return
	}

	// Acknowledge the user's reply message
	if replyMsgID != "" {
		_ = d.discord.AckReply(replyMsgID)
	}

	// Resolve the notification message
	if meta.NotificationMsgID != "" {
		_ = d.discord.RemoveAllReactions(
			meta.NotificationMsgID,
		)
		_ = d.discord.EditEmbedColor(
			meta.NotificationMsgID, ColorResolved,
		)
	}

	// Update metadata: enter remote mode
	meta.NotificationSent = false
	meta.NotificationMsgID = ""
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
		_ = d.discord.RemoveAllReactions(
			meta.NotificationMsgID,
		)
		_ = d.discord.EditEmbedColor(
			meta.NotificationMsgID, ColorResolved,
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
