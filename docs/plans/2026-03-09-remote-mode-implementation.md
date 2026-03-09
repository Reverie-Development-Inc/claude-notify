# Remote Mode & Notification UX Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add remote mode (immediate DMs when replying from Discord), reaction-based quick replies, reply acknowledgement, and Claude-aware `[notify: ...]` summaries to claude-notify.

**Architecture:** Extend the existing REST-polling daemon with reaction detection, remote mode metadata tracking, and `[notify: ...]` tag parsing in the session-update hook. No gateway/websocket changes — reactions are polled via REST alongside existing message fetching.

**Tech Stack:** Go 1.24+, discordgo v0.28.1 (existing), REST API for reactions

**Design doc:** `docs/plans/2026-03-09-remote-mode-design.md`

---

### Task 1: Session Metadata — New Fields

**Files:**
- Modify: `internal/session/metadata.go:27-41`
- Modify: `internal/session/metadata_test.go`

**Step 1: Write the failing test**

Add to `internal/session/metadata_test.go`:

```go
func TestRemoteModeFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	m := &Metadata{
		PID:              1234,
		Status:           StatusWaiting,
		RemoteMode:       true,
		SkipNotification: true,
		NotifySummary:    "Need approval to deploy",
		LastInjectedAt:   time.Now().Unix(),
	}
	_ = Write(path, m)

	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RemoteMode {
		t.Error("RemoteMode should be true")
	}
	if !got.SkipNotification {
		t.Error("SkipNotification should be true")
	}
	if got.NotifySummary != "Need approval to deploy" {
		t.Errorf(
			"NotifySummary = %q, want %q",
			got.NotifySummary,
			"Need approval to deploy",
		)
	}
	if got.LastInjectedAt == 0 {
		t.Error("LastInjectedAt should be nonzero")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestRemoteModeFields -v`
Expected: FAIL — `RemoteMode`, `SkipNotification`, `NotifySummary`, `LastInjectedAt` fields don't exist

**Step 3: Write minimal implementation**

Add fields to `Metadata` struct in `internal/session/metadata.go:27-41`:

```go
type Metadata struct {
	PID                int       `json:"pid"`
	FIFO               string    `json:"fifo"`
	CWD                string    `json:"cwd"`
	Started            time.Time `json:"started"`
	Status             Status    `json:"status"`
	SessionID          string    `json:"session_id"`
	LastStop           time.Time `json:"last_stop"`
	LastMessagePreview string    `json:"last_message_preview"`
	ShortID            string    `json:"short_id"`
	NotificationSent   bool      `json:"notification_sent"`
	NotificationMsgID  string    `json:"notification_msg_id"`
	RemoteMode         bool      `json:"remote_mode"`
	SkipNotification   bool      `json:"skip_notification"`
	NotifySummary      string    `json:"notify_summary"`
	LastInjectedAt     int64     `json:"last_injected_at"`
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -run TestRemoteModeFields -v`
Expected: PASS

**Step 5: Run full test suite**

Run: `go test ./... -race -v`
Expected: All tests pass

**Step 6: Commit**

```bash
git add internal/session/metadata.go \
  internal/session/metadata_test.go
git commit -m "feat: add remote mode fields to session metadata"
```

---

### Task 2: Discord Client — Reaction Methods

**Files:**
- Modify: `internal/discord/client.go` (append new methods)
- Create: `internal/discord/client_test.go`

**Step 1: Write the failing test**

Create `internal/discord/client_test.go`:

```go
package discord

import (
	"testing"
)

func TestExpandReaction(t *testing.T) {
	tests := []struct {
		emoji string
		want  string
	}{
		{"✅", "Yes, continue"},
		{"❌", "No, stop here"},
		{"👀", "Show me what you have so far"},
		{"🤷", ""},
	}
	for _, tt := range tests {
		got := ExpandReaction(tt.emoji)
		if got != tt.want {
			t.Errorf(
				"ExpandReaction(%q) = %q, want %q",
				tt.emoji, got, tt.want,
			)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/discord/ -run TestExpandReaction -v`
Expected: FAIL — `ExpandReaction` undefined

**Step 3: Write implementation**

Append to `internal/discord/client.go`:

```go
// Reaction emojis used for quick replies.
const (
	ReactionYes  = "✅"
	ReactionNo   = "❌"
	ReactionLook = "👀"
)

// reactionMap maps reaction emojis to reply text.
var reactionMap = map[string]string{
	ReactionYes:  "Yes, continue",
	ReactionNo:   "No, stop here",
	ReactionLook: "Show me what you have so far",
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

// FetchUserReactions returns which of the quick-reply
// emojis the configured user has reacted with on the
// given message. Returns the first match found in
// priority order: ✅, ❌, 👀.
func (c *Client) FetchUserReaction(
	msgID string,
) (string, error) {
	if err := c.checkRateLimit(); err != nil {
		return "", err
	}
	for _, emoji := range []string{
		ReactionYes, ReactionNo, ReactionLook,
	} {
		users, err := c.session.MessageReactions(
			c.dmChannel, msgID, emoji, 10, "", "",
		)
		if err != nil {
			c.handleRateLimit(err)
			return "", fmt.Errorf(
				"fetch reaction %s: %w", emoji, err,
			)
		}
		for _, u := range users {
			if u.ID == c.userID {
				return emoji, nil
			}
		}
	}
	return "", nil
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
		c.dmChannel, msgID, "❌",
	)
	if err != nil {
		c.handleRateLimit(err)
	}
	return err
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/discord/ -run TestExpandReaction -v`
Expected: PASS

**Step 5: Build check**

Run: `go build ./...`
Expected: Clean build

**Step 6: Commit**

```bash
git add internal/discord/client.go \
  internal/discord/client_test.go
git commit -m "feat: add reaction methods to Discord client"
```

---

### Task 3: Parse `[notify: ...]` Tag in Session Update

**Files:**
- Modify: `cmd/claude-notify/session_update.go:43-88`
- Modify: `internal/session/metadata.go:96-118` (UpdateStatus)
- Create: `internal/sanitize/notify_tag.go`
- Create: `internal/sanitize/notify_tag_test.go`

**Step 1: Write the failing test**

Create `internal/sanitize/notify_tag_test.go`:

```go
package sanitize

import "testing"

func TestParseNotifyTag(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		summary string
		skip    bool
		cleaned string
	}{
		{
			name:    "summary tag",
			input:   "Done fixing.\n[notify: Auth fix complete, run tests?]",
			summary: "Auth fix complete, run tests?",
			skip:    false,
			cleaned: "Done fixing.",
		},
		{
			name:    "none tag",
			input:   "Working on it.\n[notify: none]",
			summary: "",
			skip:    true,
			cleaned: "Working on it.",
		},
		{
			name:    "no tag",
			input:   "Just a regular message.",
			summary: "",
			skip:    false,
			cleaned: "Just a regular message.",
		},
		{
			name:    "tag with extra whitespace",
			input:   "Result:\n  [notify: Deploy ready]  \n",
			summary: "Deploy ready",
			skip:    false,
			cleaned: "Result:",
		},
		{
			name:    "tag mid-text ignored",
			input:   "See [notify: test] above\nMore text\n[notify: Real summary]",
			summary: "Real summary",
			skip:    false,
			cleaned: "See [notify: test] above\nMore text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, skip, cleaned := ParseNotifyTag(
				tt.input,
			)
			if summary != tt.summary {
				t.Errorf(
					"summary = %q, want %q",
					summary, tt.summary,
				)
			}
			if skip != tt.skip {
				t.Errorf(
					"skip = %v, want %v",
					skip, tt.skip,
				)
			}
			if cleaned != tt.cleaned {
				t.Errorf(
					"cleaned = %q, want %q",
					cleaned, tt.cleaned,
				)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/sanitize/ -run TestParseNotifyTag -v`
Expected: FAIL — `ParseNotifyTag` undefined

**Step 3: Write implementation**

Create `internal/sanitize/notify_tag.go`:

```go
package sanitize

import (
	"regexp"
	"strings"
)

// notifyTagRe matches [notify: ...] at the end of a
// message (last non-empty line).
var notifyTagRe = regexp.MustCompile(
	`(?m)^\s*\[notify:\s*(.*?)\]\s*$`,
)

// ParseNotifyTag extracts the [notify: ...] tag from
// the end of a message. Returns:
//   - summary: the tag content (empty if none/no tag)
//   - skip: true if [notify: none]
//   - cleaned: the message with the tag line removed
//
// Only the LAST matching [notify: ...] line is used.
func ParseNotifyTag(
	msg string,
) (summary string, skip bool, cleaned string) {
	matches := notifyTagRe.FindAllStringIndex(msg, -1)
	if len(matches) == 0 {
		return "", false, msg
	}

	// Use the last match
	last := matches[len(matches)-1]
	tagLine := strings.TrimSpace(
		msg[last[0]:last[1]],
	)

	// Extract content between [notify: and ]
	sub := notifyTagRe.FindStringSubmatch(tagLine)
	if len(sub) < 2 {
		return "", false, msg
	}
	content := strings.TrimSpace(sub[1])

	// Remove the tag line from the message
	cleaned = strings.TrimRight(
		msg[:last[0]], "\n \t",
	)

	if strings.EqualFold(content, "none") {
		return "", true, cleaned
	}
	return content, false, cleaned
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/sanitize/ -run TestParseNotifyTag -v`
Expected: PASS

**Step 5: Update `UpdateStatus` to accept notify fields**

Modify `internal/session/metadata.go` — change `UpdateStatus` signature to accept summary and skip:

```go
// UpdateStatus reads metadata, updates status and
// notification fields, then writes atomically.
func UpdateStatus(
	path string,
	status Status,
	preview string,
	notifySummary string,
	skipNotification bool,
) error {
	m, err := Read(path)
	if err != nil {
		return err
	}
	m.Status = status
	switch status {
	case StatusWaiting:
		m.LastStop = time.Now()
		m.LastMessagePreview = preview
		m.NotificationSent = false
		m.NotificationMsgID = ""
		m.NotifySummary = notifySummary
		m.SkipNotification = skipNotification
	case StatusActive:
		m.NotificationSent = false
		m.NotificationMsgID = ""
		m.SkipNotification = false
		m.NotifySummary = ""
	}
	return Write(path, m)
}
```

**Step 6: Update callers of `UpdateStatus`**

In `cmd/claude-notify/session_update.go`, update the call at the end of `runSessionUpdate`:

```go
// Parse notify tag from preview
summary, skip, cleaned := sanitize.ParseNotifyTag(
	preview,
)
if cleaned != "" {
	preview = sanitize.Preview(cleaned, 500)
}

return session.UpdateStatus(
	metaPath, status, preview, summary, skip,
)
```

Add import for `sanitize` if not already present (it should be — it's already imported for `sanitize.Preview`).

**Step 7: Fix existing tests that call `UpdateStatus`**

Update `internal/session/metadata_test.go` — the existing `TestUpdateStatus` calls `UpdateStatus(path, StatusWaiting, "test preview")`. Add the two new args:

```go
err = UpdateStatus(
	path, StatusWaiting, "test preview", "", false,
)
```

And the `StatusActive` call:

```go
err = UpdateStatus(
	path, StatusActive, "", "", false,
)
```

**Step 8: Run full test suite**

Run: `go test ./... -race -v`
Expected: All tests pass

**Step 9: Commit**

```bash
git add internal/sanitize/notify_tag.go \
  internal/sanitize/notify_tag_test.go \
  internal/session/metadata.go \
  internal/session/metadata_test.go \
  cmd/claude-notify/session_update.go
git commit -m "feat: parse [notify: ...] tag from Claude output"
```

---

### Task 4: Update Notification Format

**Files:**
- Modify: `internal/discord/client.go` (SendNotification)
- Modify: `internal/daemon/daemon.go` (sendNotification, defaultSuggestions)

**Step 1: Update `SendNotification` signature**

Modify `internal/discord/client.go` — `SendNotification` to accept a `summary` parameter and add reactions. Update the embed format:

```go
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

	desc := fmt.Sprintf(
		"%s\n\n"+
			"React below, or **reply** to this "+
			"message to type something custom.",
		body,
	)

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf(
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
```

**Step 2: Update `sendNotification` in daemon**

Modify `internal/daemon/daemon.go` — update `sendNotification` to pass the summary and remove the `suggestions` parameter from `SendNotification` calls:

```go
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
	d.hasEverNotified = true
	log.Printf(
		"sent notification for session %d "+
			"(msg: %s)", meta.PID, msgID,
	)
}
```

**Step 3: Remove `defaultSuggestions` and `expandShortcut`**

These functions (lines 460-488) are no longer needed — reactions replace numbered suggestions. Delete them. Update any callers in `processReplies` / `deliverReply` to remove shortcut expansion (will be replaced in Task 6 with reaction expansion).

**Step 4: Build check**

Run: `go build ./...`
Expected: Clean build (may require fixing callers first — check for compilation errors and fix)

**Step 5: Run tests**

Run: `go test ./... -race -v`
Expected: All pass

**Step 6: Commit**

```bash
git add internal/discord/client.go \
  internal/daemon/daemon.go
git commit -m "feat: update notification format with reactions"
```

---

### Task 5: Reaction Polling in Daemon

**Files:**
- Modify: `internal/daemon/daemon.go` (tick, new method)

**Step 1: Add `processReactions` method**

Add to `internal/daemon/daemon.go`:

```go
// processReactions checks for user reactions on
// active notification messages and delivers them
// as replies.
func (d *Daemon) processReactions(
	notified []*session.Metadata,
) {
	for _, meta := range notified {
		if meta.NotificationMsgID == "" {
			continue
		}
		emoji, err := d.discord.FetchUserReaction(
			meta.NotificationMsgID,
		)
		if err != nil {
			log.Printf(
				"reaction poll error for %d: %v",
				meta.PID, err,
			)
			continue
		}
		if emoji == "" {
			continue
		}

		text := discord.ExpandReaction(emoji)
		if text == "" {
			continue
		}

		log.Printf(
			"reaction %s from user on session %d",
			emoji, meta.PID,
		)
		d.deliverReply(meta, text)
	}
}
```

**Step 2: Call `processReactions` in `tick()`**

In `internal/daemon/daemon.go`, in the `tick()` method, add reaction polling after the existing `processReplies` call (around line 139):

```go
// Process reactions on notification messages
if len(notified) > 0 {
	d.processReactions(notified)
}
```

**Step 3: Build and test**

Run: `go build ./... && go test ./... -race`
Expected: Clean build, all tests pass

**Step 4: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat: poll reactions on notification messages"
```

---

### Task 6: Reply Acknowledgement & Cleanup

**Files:**
- Modify: `internal/daemon/daemon.go` (deliverReply, dismissNotification)

**Step 1: Update `deliverReply` for acknowledgement**

Replace the existing `deliverReply` method in `internal/daemon/daemon.go`. The new version:
- Acknowledges typed replies with ✅
- Clears reactions on the notification message
- Greys out the notification embed
- Sets `remote_mode: true`
- Handles FIFO write failures with ❌

```go
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
```

**Step 2: Update `processReplies` to pass `replyMsgID`**

In the existing `processReplies` method, change calls from `d.deliverReply(meta, content)` to `d.deliverReplyFrom(meta, reply.Content, reply.MessageID)`. The shortcut expansion is no longer needed (reactions handle that), but keep bare text replies working:

Find the line where `deliverReply` is called for routed replies and change to `deliverReplyFrom`:

```go
d.deliverReplyFrom(
	meta, reply.Content, reply.MessageID,
)
```

**Step 3: Update `dismissNotification`**

Also grey out and clear reactions when a session goes active from terminal input:

```go
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
```

**Step 4: Build and test**

Run: `go build ./... && go test ./... -race`
Expected: Clean build, all tests pass

**Step 5: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat: reply acknowledgement and notification cleanup"
```

---

### Task 7: Remote Mode Logic

**Files:**
- Modify: `internal/daemon/daemon.go` (shouldNotify, tick)

**Step 1: Update `shouldNotify` for remote mode**

Replace `shouldNotify` in `internal/daemon/daemon.go`:

```go
const remoteDebounce = 15 * time.Second

func (d *Daemon) shouldNotify(
	meta *session.Metadata,
	delay time.Duration,
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

	elapsed := time.Since(meta.LastStop)

	// Remote mode: use short debounce instead of
	// full delay, and require a notify tag or
	// fallback preview.
	if meta.RemoteMode {
		return elapsed >= remoteDebounce
	}

	return elapsed >= delay
}
```

**Step 2: Handle remote mode exit in `tick()`**

In `tick()`, when checking sessions that went active, distinguish between FIFO echo and real terminal input. Add this logic to the dismiss block (around lines 107-121):

```go
// Check for sessions that returned to active
for _, meta := range sessions {
	if meta.Status != session.StatusActive {
		continue
	}
	if meta.NotificationSent {
		// Was this a FIFO injection echo?
		// If LastInjectedAt is within the last 30s,
		// this is the hook firing from our own FIFO
		// write — don't dismiss or exit remote mode.
		if meta.RemoteMode &&
			meta.LastInjectedAt > 0 &&
			time.Since(
				time.Unix(meta.LastInjectedAt, 0),
			) < 30*time.Second {
			continue
		}
		d.dismissNotification(meta)
	}
}
```

**Step 3: Build and test**

Run: `go build ./... && go test ./... -race`
Expected: Clean build, all tests pass

**Step 4: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat: remote mode with immediate notifications"
```

---

### Task 8: Documentation

**Files:**
- Modify: `README.md`
- Modify: `~/.claude/CLAUDE.md` (global)

**Step 1: Update README**

Add a "Remote Mode" section after "How It Works" in `README.md`:

```markdown
## Remote Mode

When you reply to a notification from Discord, claude-notify
enters **remote mode**. In this mode:

- Claude's next response is sent as a DM immediately (no
  15-minute delay)
- Claude includes a summary of what it needs instead of raw
  output
- Remote mode stays active until you type directly in the
  terminal

### Quick Reactions

Notification messages include three reaction emojis:

| Reaction | Meaning |
|----------|---------|
| ✅ | Yes, continue |
| ❌ | No, stop here |
| 👀 | Show me what you have so far |

React to respond quickly, or use Discord's **Reply** feature
to type a custom response.

### Reply Acknowledgement

- **Typed replies**: Bot reacts with ✅ on your message
- **Reactions**: Bot clears the reaction bar on the
  notification
- **Both**: Notification embed turns grey to show it's handled

If the session is no longer active, the bot reacts with ❌
and sends "Session is no longer active."
```

Update the Configuration table — add new env var if needed (none required for this feature).

**Step 2: Update global CLAUDE.md**

Add to `~/.claude/CLAUDE.md` before the "NEVER include Generated with Claude Code" line:

```markdown
- When your input begins with `[discord]`, the user is
  replying remotely via Discord and cannot see your full
  terminal output. End your response with one of:
  - `[notify: one-line summary of what you need]` — triggers
    an immediate Discord DM with your summary
  - `[notify: none]` — no notification needed yet
  The `[notify: ...]` line is visible in the terminal as a
  paper trail that a notification was sent.
```

**Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document remote mode, reactions, and notify tag"
```

Note: `~/.claude/CLAUDE.md` is a local file not tracked in this repo — update it manually.

---

### Task 9: Integration Test

**Files:**
- Create: `tests/remote_mode_test.go`

**Step 1: Write integration test**

Create `tests/remote_mode_test.go` to test the full flow with mocked Discord client:

```go
package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Reverie-Development-Inc/claude-notify/internal/sanitize"
	"github.com/Reverie-Development-Inc/claude-notify/internal/session"
)

func TestNotifyTagIntegration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	// Simulate: session in remote mode, Claude sends
	// a message with [notify: ...] tag
	m := &session.Metadata{
		PID:        1234,
		Status:     session.StatusActive,
		RemoteMode: true,
	}
	_ = session.Write(path, m)

	// Simulate hook input with notify tag
	raw := "Fixed the auth bug and updated tests.\n" +
		"[notify: Auth fix done, want me to deploy?]"
	summary, skip, cleaned := sanitize.ParseNotifyTag(
		raw,
	)

	preview := sanitize.Preview(cleaned, 500)

	err := session.UpdateStatus(
		path,
		session.StatusWaiting,
		preview,
		summary,
		skip,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := session.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	if got.NotifySummary !=
		"Auth fix done, want me to deploy?" {
		t.Errorf(
			"NotifySummary = %q",
			got.NotifySummary,
		)
	}
	if got.SkipNotification {
		t.Error("SkipNotification should be false")
	}
	if !got.RemoteMode {
		t.Error("RemoteMode should persist")
	}
}

func TestNotifyTagNone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	m := &session.Metadata{
		PID:        1234,
		Status:     session.StatusActive,
		RemoteMode: true,
	}
	_ = session.Write(path, m)

	raw := "Still working on it...\n[notify: none]"
	summary, skip, cleaned := sanitize.ParseNotifyTag(
		raw,
	)
	preview := sanitize.Preview(cleaned, 500)

	err := session.UpdateStatus(
		path,
		session.StatusWaiting,
		preview,
		summary,
		skip,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := session.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	if !got.SkipNotification {
		t.Error("SkipNotification should be true")
	}
	if got.NotifySummary != "" {
		t.Errorf(
			"NotifySummary should be empty, got %q",
			got.NotifySummary,
		)
	}
}

func TestRemoteModePreservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	m := &session.Metadata{
		PID:            1234,
		Status:         session.StatusWaiting,
		RemoteMode:     true,
		LastInjectedAt: time.Now().Unix(),
	}
	_ = session.Write(path, m)

	// Simulate: FIFO injection causes status → active
	// Remote mode should persist (FIFO echo)
	err := session.UpdateStatus(
		path,
		session.StatusActive,
		"",
		"",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := session.Read(path)
	// RemoteMode is NOT cleared by UpdateStatus —
	// only the daemon clears it when detecting real
	// terminal input
	if !got.RemoteMode {
		t.Error(
			"RemoteMode should persist through " +
				"FIFO echo",
		)
	}
}
```

**Step 2: Run integration tests**

Run: `go test ./tests/ -race -v`
Expected: All pass

**Step 3: Run full suite**

Run: `go test ./... -race`
Expected: All pass

**Step 4: Commit**

```bash
git add tests/remote_mode_test.go
git commit -m "test: integration tests for remote mode and notify tag"
```

---

### Task 10: Final Build & Push

**Step 1: Full lint and test**

Run: `golangci-lint run ./... && go test ./... -race`
Expected: Clean

**Step 2: Multi-platform build check**

Run: `GOOS=darwin GOARCH=arm64 go build -o /dev/null . && GOOS=linux GOARCH=amd64 go build -o /dev/null .`
Expected: Both succeed

**Step 3: Push**

Run: `git push`
