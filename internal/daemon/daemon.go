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
	"syscall"
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
	cfg          *config.Config
	discord      *discord.Client
	stateDir     string
	pollInterval time.Duration
}

// New creates a Daemon with the given config and Discord
// client.
func New(cfg *config.Config, dc *discord.Client) *Daemon {
	return &Daemon{
		cfg:          cfg,
		discord:      dc,
		stateDir:     cfg.StateDir(),
		pollInterval: 10 * time.Second,
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
// waiting long enough, and check for replies on notified
// sessions.
func (d *Daemon) tick() {
	sessions, err := session.List(d.stateDir)
	if err != nil {
		log.Printf("list sessions: %v", err)
		return
	}

	for _, meta := range sessions {
		// Clean up dead sessions.
		if !isProcessAlive(meta.PID) {
			path := filepath.Join(
				d.stateDir,
				fmt.Sprintf("%d.json", meta.PID),
			)
			os.Remove(path)
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
			d.checkForReply(meta)
		}
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

	meta.NotificationSent = true
	meta.NotificationMsgID = msgID
	path := filepath.Join(
		d.stateDir,
		fmt.Sprintf("%d.json", meta.PID),
	)
	session.Write(path, meta)

	log.Printf(
		"notified for session %s (#%s)",
		projectName, meta.ShortID,
	)
}

// checkForReply polls Discord for a reply to the
// notification message. If found, expands numbered
// shortcuts, writes the reply to the session FIFO, and
// resets notification state.
func (d *Daemon) checkForReply(meta *session.Metadata) {
	reply, err := d.discord.PollForReply(
		meta.NotificationMsgID,
	)
	if err != nil {
		log.Printf(
			"poll reply PID %d: %v", meta.PID, err,
		)
		return
	}
	if reply == "" {
		return
	}

	// Expand numbered shortcuts.
	reply = expandShortcut(reply, defaultSuggestions())

	// Write to FIFO.
	if err := writeToFIFO(meta.FIFO, reply); err != nil {
		log.Printf(
			"write FIFO PID %d: %v", meta.PID, err,
		)
		return
	}

	// Reset notification state.
	meta.NotificationSent = false
	meta.NotificationMsgID = ""
	meta.Status = session.StatusActive
	path := filepath.Join(
		d.stateDir,
		fmt.Sprintf("%d.json", meta.PID),
	)
	session.Write(path, meta)

	log.Printf(
		"reply injected for session #%s", meta.ShortID,
	)
}

// writeToFIFO opens the named pipe for writing and sends
// content followed by a newline. This blocks until a reader
// has the FIFO open (by design — the wrapper's goroutine is
// always reading).
func writeToFIFO(fifoPath, content string) error {
	f, err := os.OpenFile(
		fifoPath, os.O_WRONLY, 0600,
	)
	if err != nil {
		return fmt.Errorf("open fifo: %w", err)
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, content)
	return err
}

// isProcessAlive checks whether a process with the given
// PID exists by sending signal 0.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
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
