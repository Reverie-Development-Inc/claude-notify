// Package daemon watches session metadata files, manages
// notification timers, sends Discord DMs, polls for replies,
// and writes replies to session FIFOs.
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
// DMs when sessions have been waiting too long, and injects
// replies back into sessions via FIFOs.
type Daemon struct {
	cfg             *config.Config
	discord         *discord.Client
	stateDir        string
	pollInterval    time.Duration
	lastProcessedID string
	hintedMsgIDs    map[string]bool
	// lastCmdCheckID tracks the last DM message checked
	// for /clear commands, to avoid re-processing.
	lastCmdCheckID string
	// hasEverNotified gates command polling — we only
	// start checking for /clear after sending at least
	// one notification (so we don't poll an empty DM).
	hasEverNotified bool
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
		hintedMsgIDs: make(map[string]bool),
	}
}

// Run starts the daemon loop. It cleans stale sessions on
// startup, then ticks every pollInterval until ctx is
// cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(d.stateDir, 0700); err != nil {
		return fmt.Errorf("mkdir state: %w", err)
	}

	d.cleanStaleSessions()

	// Start command polling immediately — there may be
	// orphaned notifications from a previous run.
	d.hasEverNotified = true

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
		}
	}
}

// tick runs one iteration of the daemon loop: list all
// sessions, clean dead ones, send notifications for those
// waiting long enough, and process replies centrally.
func (d *Daemon) tick() {
	sessions, err := session.List(d.stateDir)
	if err != nil {
		log.Printf("list sessions: %v", err)
		return
	}

	var notified []*session.Metadata
	for _, meta := range sessions {
		// Clean up dead sessions.
		if !isProcessAlive(meta.PID) {
			d.dismissNotification(meta)
			path := filepath.Join(
				d.stateDir,
				fmt.Sprintf("%d.json", meta.PID),
			)
			os.Remove(path)
			continue
		}

		// User returned to session — delete the
		// stale Discord notification.
		if meta.Status == session.StatusActive &&
			meta.NotificationSent &&
			meta.NotificationMsgID != "" {
			d.dismissNotification(meta)
			meta.NotificationSent = false
			meta.NotificationMsgID = ""
			path := filepath.Join(
				d.stateDir,
				fmt.Sprintf("%d.json", meta.PID),
			)
			session.Write(path, meta)
			continue
		}

		delay := time.Duration(
			d.cfg.Notify.DelayMinutes,
		) * time.Minute

		if shouldNotify(meta, delay) {
			d.sendNotification(meta)
		}

		if meta.NotificationSent &&
			meta.NotificationMsgID != "" {
			notified = append(notified, meta)
		}
	}

	if len(notified) > 0 {
		d.processReplies(notified)
	}

	// Check for /clear commands even when no sessions
	// are currently notified (handles orphaned embeds).
	if d.hasEverNotified {
		d.processCommands()
	}
}

// shouldNotify returns true if the session is waiting, has
// not already been notified, and has been waiting longer
// than delay.
func shouldNotify(
	meta *session.Metadata, delay time.Duration,
) bool {
	if meta.Status != session.StatusWaiting {
		return false
	}
	if meta.NotificationSent {
		return false
	}
	if meta.LastStop.IsZero() {
		return false
	}
	return time.Since(meta.LastStop) >= delay
}

// sendNotification sends a Discord DM for the given session
// and updates the metadata file with the notification state.
func (d *Daemon) sendNotification(meta *session.Metadata) {
	projectName := filepath.Base(meta.CWD)
	suggestions := defaultSuggestions()

	msgID, err := d.discord.SendNotification(
		projectName, meta.ShortID,
		meta.LastMessagePreview, suggestions,
	)
	if err != nil {
		log.Printf(
			"send notification PID %d: %v",
			meta.PID, err,
		)
		return
	}

	d.hasEverNotified = true
	meta.NotificationSent = true
	meta.NotificationMsgID = msgID
	path := filepath.Join(
		d.stateDir,
		fmt.Sprintf("%d.json", meta.PID),
	)
	if err := session.Write(path, meta); err != nil {
		log.Printf(
			"write metadata PID %d: %v",
			meta.PID, err,
		)
	}

	log.Printf(
		"notified for session %s (#%s)",
		projectName, meta.ShortID,
	)
}

// processReplies fetches replies from Discord and routes
// them to the correct session using Discord reply-to
// references. Falls back to single-session routing when
// only one session is waiting.
func (d *Daemon) processReplies(
	notified []*session.Metadata,
) {
	// Determine the "after" cursor for fetching.
	afterID := d.lastProcessedID
	if afterID == "" {
		// Use the earliest notification msg ID.
		afterID = notified[0].NotificationMsgID
		for _, m := range notified[1:] {
			// Discord snowflake IDs are chronological;
			// smaller ID = earlier message.
			if m.NotificationMsgID < afterID {
				afterID = m.NotificationMsgID
			}
		}
	}

	replies, err := d.discord.FetchReplies(afterID)
	if err != nil {
		log.Printf("fetch replies: %v", err)
		return
	}

	// Build lookup: notification msg ID -> session.
	byMsgID := make(map[string]*session.Metadata)
	for _, m := range notified {
		byMsgID[m.NotificationMsgID] = m
	}

	for _, reply := range replies {
		// Update cursor so we don't re-process.
		d.lastProcessedID = reply.MessageID

		// Handle /clear command.
		lower := strings.ToLower(
			strings.TrimSpace(reply.Content))
		if strings.HasPrefix(lower, "/clear") {
			// Already handled by processCommands,
			// but mark as seen to skip hint.
			d.lastCmdCheckID = reply.MessageID
			continue
		}

		// Route by reply-to reference.
		if reply.RefMessageID != "" {
			meta, ok := byMsgID[reply.RefMessageID]
			if ok {
				d.deliverReply(meta, reply.Content)
				delete(byMsgID, reply.RefMessageID)
				continue
			}
			// Reply-to a notification whose session
			// already resumed — let user know.
			if !d.hintedMsgIDs[reply.MessageID] {
				d.hintedMsgIDs[reply.MessageID] = true
				_ = d.discord.SendHint(
					"That session has already " +
						"received a response. " +
						"No action needed.")
			}
			continue
		}

		// Bare message (no reply-to) — always hint.
		if !d.hintedMsgIDs[reply.MessageID] {
			d.hintedMsgIDs[reply.MessageID] = true
			hint := "Trying to reply to a Claude " +
				"Code session? Use Discord's " +
				"**Reply** feature (swipe left " +
				"on mobile, right-click → Reply " +
				"on desktop) on the notification " +
				"you want to respond to."
			if err := d.discord.SendHint(
				hint,
			); err != nil {
				log.Printf("send hint: %v", err)
			}
		}
	}
}

// deliverReply expands numbered shortcuts, writes the
// reply to the session FIFO, and resets notification state.
func (d *Daemon) deliverReply(
	meta *session.Metadata, content string,
) {
	content = expandShortcut(
		content, defaultSuggestions(),
	)
	if err := writeToFIFO(meta.FIFO, content); err != nil {
		log.Printf(
			"write FIFO PID %d: %v",
			meta.PID, err,
		)
		return
	}

	meta.NotificationSent = false
	meta.NotificationMsgID = ""
	meta.Status = session.StatusActive
	path := filepath.Join(
		d.stateDir,
		fmt.Sprintf("%d.json", meta.PID),
	)
	if err := session.Write(path, meta); err != nil {
		log.Printf(
			"write metadata PID %d: %v",
			meta.PID, err,
		)
	}

	log.Printf(
		"reply injected for session #%s",
		meta.ShortID,
	)
}

// processCommands checks recent DM messages for /clear
// commands. Runs every tick independently of whether any
// sessions are currently notified, so orphaned embeds can
// still be cleared.
func (d *Daemon) processCommands() {
	msgs, err := d.discord.FetchRecentUserMessages(5)
	if err != nil {
		return // silent — don't spam logs
	}
	for _, msg := range msgs {
		// Skip already-processed messages.
		if msg.MessageID <= d.lastCmdCheckID {
			continue
		}
		d.lastCmdCheckID = msg.MessageID

		content := strings.TrimSpace(msg.Content)
		lower := strings.ToLower(content)
		if !strings.HasPrefix(lower, "/clear") {
			continue
		}

		// Parse optional session ID.
		args := strings.Fields(content)[1:]
		var sessionID string
		if len(args) > 0 &&
			strings.ToLower(args[0]) != "all" {
			sessionID = args[0]
		}

		result := d.clearNotifications(sessionID)
		_ = d.discord.SendHint(result)
		log.Printf("clear command: %s", result)
	}
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
		session.Write(path, meta)
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

// dismissNotification deletes a pending Discord
// notification message. Called when the user returns to
// the session or the session dies before they reply.
func (d *Daemon) dismissNotification(
	meta *session.Metadata,
) {
	if !meta.NotificationSent ||
		meta.NotificationMsgID == "" {
		return
	}
	if err := d.discord.DeleteMessage(
		meta.NotificationMsgID,
	); err != nil {
		log.Printf(
			"delete notification PID %d: %v",
			meta.PID, err,
		)
		return
	}
	log.Printf(
		"dismissed notification for session #%s",
		meta.ShortID,
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
			os.Remove(path)
			log.Printf(
				"cleaned stale session PID %d",
				meta.PID,
			)
		}
	}
}

// defaultSuggestions returns the numbered reply options
// included in every notification DM.
func defaultSuggestions() []string {
	return []string{
		"Yes, continue",
		"No, stop here",
		"Show me what you have so far",
	}
}

// expandShortcut maps "1", "2", "3" to the corresponding
// suggestion text. Non-matching input is returned as-is.
func expandShortcut(
	reply string, suggestions []string,
) string {
	switch reply {
	case "1":
		if len(suggestions) > 0 {
			return suggestions[0]
		}
	case "2":
		if len(suggestions) > 1 {
			return suggestions[1]
		}
	case "3":
		if len(suggestions) > 2 {
			return suggestions[2]
		}
	}
	return reply
}
