# claude-notify Stabilization Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix three bugs (FIFO injection, idle timer, /clear) and migrate from REST polling to Discord gateway for all inbound communication.

**Architecture:** The daemon gains a Discord gateway (outbound websocket) alongside its existing REST client. Gateway event handlers replace all polling loops. The tick loop is reduced to session scanning + notification sending only.

**Tech Stack:** Go 1.26.1, discordgo v0.28.1 (already supports gateway natively), cobra CLI

---

### Task 1: Fix FIFO Write Timing

**Files:**
- Modify: `internal/daemon/platform_unix.go:17-29`
- Test: `internal/daemon/platform_unix_test.go` (create)

**Step 1: Write the failing test**

Create `internal/daemon/platform_unix_test.go`:

```go
//go:build !windows

package daemon

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestWriteToFIFO_DeliversContentThenSubmit(t *testing.T) {
	dir := t.TempDir()
	fifoPath := dir + "/test.fifo"
	if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
		t.Fatal(err)
	}

	// Read side: collect everything written to the FIFO.
	done := make(chan string, 1)
	go func() {
		f, err := os.Open(fifoPath)
		if err != nil {
			done <- ""
			return
		}
		defer func() { _ = f.Close() }()
		buf := make([]byte, 4096)
		var all []byte
		for {
			n, err := f.Read(buf)
			if n > 0 {
				all = append(all, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- string(all)
	}()

	// Small delay to ensure reader is blocking on Open.
	time.Sleep(50 * time.Millisecond)

	err := writeToFIFO(fifoPath, "hello world")
	if err != nil {
		t.Fatalf("writeToFIFO: %v", err)
	}

	result := <-done
	if result != "hello world\r" {
		t.Errorf(
			"got %q, want %q",
			result, "hello world\r",
		)
	}
}
```

**Step 2: Run test to verify it passes (baseline)**

Run: `cd /home/honor/src/reverie/claude-notify && go test ./internal/daemon/ -run TestWriteToFIFO -v -race`

Expected: PASS (test validates current behavior before we change it)

**Step 3: Update writeToFIFO with split write + delay**

Edit `internal/daemon/platform_unix.go`, replace the `writeToFIFO` function:

```go
func writeToFIFO(fifoPath, content string) error {
	f, err := os.OpenFile(
		fifoPath,
		os.O_WRONLY|syscall.O_NONBLOCK,
		0600,
	)
	if err != nil {
		return fmt.Errorf("open fifo: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Write text content first.
	if _, err := fmt.Fprint(f, content); err != nil {
		return err
	}

	// Let the TUI process the pasted text before
	// sending the submit keystroke. Without this
	// delay, content + \r arrive in the same PTY
	// read buffer and Claude Code's Ink framework
	// can swallow the carriage return during the
	// re-render triggered by the text.
	time.Sleep(50 * time.Millisecond)

	// Submit with carriage return (Enter key in
	// PTY raw mode).
	_, err = fmt.Fprint(f, "\r")
	return err
}
```

**Step 4: Run test to verify it still passes**

Run: `cd /home/honor/src/reverie/claude-notify && go test ./internal/daemon/ -run TestWriteToFIFO -v -race`

Expected: PASS (output is still `"hello world\r"`, just delivered in two writes now)

**Step 5: Run full test suite**

Run: `cd /home/honor/src/reverie/claude-notify && make test`

Expected: All tests pass

**Step 6: Commit**

```bash
cd /home/honor/src/reverie/claude-notify
git add internal/daemon/platform_unix.go \
  internal/daemon/platform_unix_test.go
git commit -m "fix: split FIFO write with 50ms delay to prevent swallowed submit

Content and carriage return were written as a single
fmt.Fprint, causing them to arrive in the same PTY read
buffer. Claude Code's Ink TUI sometimes swallowed the
\\r during the re-render triggered by the text content.

Split into two writes with a 50ms gap so io.Copy delivers
them as separate PTY events."
```

---

### Task 2: Fix RemoteMode Sticking in UpdateStatus

**Files:**
- Modify: `internal/session/metadata.go:116-132`
- Modify: `tests/integration_test.go` (update existing test + add new)

**Step 1: Write the failing test**

Add to `tests/integration_test.go`:

```go
func TestRemoteModeClearedOnTerminalReturn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	// Session in remote mode, but LastInjectedAt is
	// old (>30s ago) — simulates user returning to
	// terminal after being away.
	m := &session.Metadata{
		PID:            1234,
		Status:         session.StatusWaiting,
		RemoteMode:     true,
		LastInjectedAt: time.Now().Add(
			-60 * time.Second).Unix(),
	}
	_ = session.Write(path, m)

	err := session.UpdateStatus(
		path,
		session.StatusActive,
		"", "", false,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := session.Read(path)
	if got.RemoteMode {
		t.Error(
			"RemoteMode should be cleared when " +
				"user returns to terminal (old " +
				"LastInjectedAt)",
		)
	}
}

func TestRemoteModeClearedWhenNeverInjected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	// Remote mode but LastInjectedAt is zero — should
	// never happen in practice but should be handled.
	m := &session.Metadata{
		PID:        1234,
		Status:     session.StatusWaiting,
		RemoteMode: true,
	}
	_ = session.Write(path, m)

	err := session.UpdateStatus(
		path,
		session.StatusActive,
		"", "", false,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := session.Read(path)
	if got.RemoteMode {
		t.Error(
			"RemoteMode should be cleared when " +
				"LastInjectedAt is zero",
		)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/honor/src/reverie/claude-notify && go test ./tests/ -run "TestRemoteModeCleared" -v`

Expected: FAIL — both tests fail because `RemoteMode` is not cleared by `UpdateStatus`

**Step 3: Fix UpdateStatus in metadata.go**

Edit `internal/session/metadata.go`, replace the `StatusActive` case (lines 124-129):

```go
	case StatusActive:
		m.NotificationSent = false
		m.NotificationMsgID = ""
		m.SkipNotification = false
		m.NotifySummary = ""
		// Exit remote mode if this activation is NOT
		// from a recent FIFO injection (user returned
		// to terminal themselves).
		if m.RemoteMode && (m.LastInjectedAt == 0 ||
			time.Since(
				time.Unix(m.LastInjectedAt, 0),
			) > 30*time.Second) {
			m.RemoteMode = false
		}
```

Note: add `"time"` to the import block if not already present.

**Step 4: Run the new tests to verify they pass**

Run: `cd /home/honor/src/reverie/claude-notify && go test ./tests/ -run "TestRemoteModeCleared" -v`

Expected: PASS

**Step 5: Run existing RemoteMode test to verify no regression**

Run: `cd /home/honor/src/reverie/claude-notify && go test ./tests/ -run "TestRemoteMode" -v`

Expected: All three tests pass — `TestRemoteModePreservation` (FIFO echo case) still passes because `LastInjectedAt` is recent (< 30s)

**Step 6: Run full test suite**

Run: `cd /home/honor/src/reverie/claude-notify && make test`

Expected: All tests pass

**Step 7: Commit**

```bash
cd /home/honor/src/reverie/claude-notify
git add internal/session/metadata.go \
  tests/integration_test.go
git commit -m "fix: clear RemoteMode when user returns to terminal

UpdateStatus for StatusActive now checks LastInjectedAt.
If zero or >30s old, the activation came from the user
typing in the terminal (not a FIFO echo), so RemoteMode
is cleared. This prevents the 15-second debounce from
being used instead of the full delay on subsequent
notifications."
```

---

### Task 3: Add Gateway Connection to Discord Client

**Files:**
- Modify: `internal/discord/client.go`
- Test: `internal/discord/client_test.go`

**Step 1: Write tests for gateway setup and event channel types**

Replace `internal/discord/client_test.go` with:

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
		{ReactionYes, "Yes, continue"},
		{ReactionNo, "No, stop here"},
		{ReactionLook, "Show me what you have so far"},
		{"🎉", ""},
	}
	for _, tt := range tests {
		got := ExpandReaction(tt.emoji)
		if got != tt.want {
			t.Errorf(
				"ExpandReaction(%s) = %q, want %q",
				tt.emoji, got, tt.want,
			)
		}
	}
}

func TestIsNotificationEmbed(t *testing.T) {
	// This tests the helper used by ClearNotificationMessages.
	// We can't easily construct discordgo.Message in tests
	// without the full struct, so this is a placeholder for
	// integration tests that will cover the gateway path.
}

func TestEventChannelTypes(t *testing.T) {
	// Verify the event types can be instantiated.
	// This validates the channel type definitions compile.
	var _ ReplyEvent
	var _ ReactionEvent
	var _ ClearCommand
}
```

**Step 2: Define event types and update Client struct**

Add to `internal/discord/client.go`, after the `Client` struct definition:

```go
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
```

Update the `Client` struct to add event channels and gateway fields:

```go
type Client struct {
	session    *discordgo.Session
	userID     string
	dmChannel  string
	validator  *Validator
	retryAfter time.Time
	mu         sync.Mutex

	// Gateway event channels — daemon selects on these.
	Replies    chan ReplyEvent
	Reactions  chan ReactionEvent
	Clears     chan ClearCommand

	// appID is the bot's application ID, needed for
	// slash command registration.
	appID string
}
```

**Step 3: Update NewClient to open gateway and register handlers**

Replace `NewClient` in `internal/discord/client.go`:

```go
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
```

**Step 4: Implement gateway event handlers**

Add to `internal/discord/client.go`:

```go
// onReady captures the bot's application ID and logs
// the gateway connection.
func (c *Client) onReady(
	s *discordgo.Session, r *discordgo.Ready,
) {
	c.appID = r.Application.ID
	log.Printf(
		"gateway connected as %s (app: %s)",
		r.User.Username, c.appID,
	)
}

// onMessageCreate routes DM messages from the
// configured user to the Replies channel.
func (c *Client) onMessageCreate(
	s *discordgo.Session,
	m *discordgo.MessageCreate,
) {
	if m.Author == nil || m.Author.ID != c.userID {
		return
	}
	// Ignore messages in non-DM channels.
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

// onMessageReactionAdd routes DM reactions from the
// configured user to the Reactions channel.
func (c *Client) onMessageReactionAdd(
	s *discordgo.Session,
	r *discordgo.MessageReactionAdd,
) {
	if r.UserID != c.userID {
		return
	}
	// Only process our quick-reply emojis.
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

// onInteractionCreate handles slash command
// interactions (e.g., /clear).
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
```

**Step 5: Add slash command registration method**

Add to `internal/discord/client.go`:

```go
// RegisterCommands registers the /clear slash command
// with Discord. Safe to call multiple times — Discord
// deduplicates by name.
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

// RespondToInteraction sends an ephemeral response to
// a slash command interaction.
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
```

**Step 6: Run test to verify compilation + event types**

Run: `cd /home/honor/src/reverie/claude-notify && go test ./internal/discord/ -v -race`

Expected: PASS

**Step 7: Run full test suite**

Run: `cd /home/honor/src/reverie/claude-notify && make test`

Expected: All tests pass

**Step 8: Commit**

```bash
cd /home/honor/src/reverie/claude-notify
git add internal/discord/client.go \
  internal/discord/client_test.go
git commit -m "feat: add Discord gateway connection with event channels

Open a persistent websocket to Discord using minimal
intents (DirectMessages + DirectMessageReactions).
Gateway event handlers push ReplyEvent, ReactionEvent,
and ClearCommand to buffered channels. Add /clear slash
command registration and ephemeral interaction responses.
REST methods for sending are unchanged."
```

---

### Task 4: Migrate Daemon to Event-Driven Architecture

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/daemon_test.go`

**Step 1: Write test for the new event-driven reply routing**

Add to `internal/daemon/daemon_test.go`:

```go
func TestShouldNotify_RemoteModeExpired(t *testing.T) {
	// After the RemoteMode fix in metadata.go,
	// verify shouldNotify uses full delay when
	// RemoteMode is false.
	meta := &session.Metadata{
		Status:     session.StatusWaiting,
		LastStop:   time.Now().Add(-20 * time.Second),
		RemoteMode: false,
	}
	if shouldNotify(meta, 15*time.Minute) {
		t.Error(
			"should not notify at 20s with " +
				"15min delay and RemoteMode off",
		)
	}
}
```

**Step 2: Run test to verify it passes (baseline)**

Run: `cd /home/honor/src/reverie/claude-notify && go test ./internal/daemon/ -run TestShouldNotify -v -race`

Expected: PASS (all shouldNotify tests pass)

**Step 3: Refactor Daemon struct — remove polling fields, add event handling**

Edit `internal/daemon/daemon.go`. Replace the `Daemon` struct and `New` function:

```go
type Daemon struct {
	cfg          *config.Config
	discord      *discord.Client
	stateDir     string
	pollInterval time.Duration
}

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
```

**Step 4: Rewrite Run to select on gateway channels + tick timer**

Replace the `Run` method:

```go
func (d *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(d.stateDir, 0700); err != nil {
		return fmt.Errorf("mkdir state: %w", err)
	}

	d.cleanStaleSessions()

	// Register slash commands after gateway is ready.
	// Retry briefly — the Ready event may not have
	// fired yet.
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
```

**Step 5: Simplify tick — remove polling methods**

Replace the `tick` method (remove `processReplies`, `processReactions`, `processCommands` calls):

```go
func (d *Daemon) tick() {
	sessions, err := session.List(d.stateDir)
	if err != nil {
		log.Printf("list sessions: %v", err)
		return
	}

	for _, meta := range sessions {
		if !isProcessAlive(meta.PID) {
			d.dismissNotification(meta)
			path := filepath.Join(
				d.stateDir,
				fmt.Sprintf("%d.json", meta.PID),
			)
			_ = os.Remove(path)
			continue
		}

		if meta.Status == session.StatusActive &&
			meta.NotificationSent &&
			meta.NotificationMsgID != "" {
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
```

**Step 6: Add event handler methods**

Add to `daemon.go`:

```go
// handleReply routes a Discord reply to the correct
// session via FIFO.
func (d *Daemon) handleReply(ev discord.ReplyEvent) {
	// Skip /clear text (handled by slash command now,
	// but users might still type it).
	lower := strings.ToLower(
		strings.TrimSpace(ev.Content))
	if strings.HasPrefix(lower, "/clear") {
		return
	}

	// Route by reply-to reference.
	if ev.RefMessageID != "" {
		meta := d.findSessionByMsgID(ev.RefMessageID)
		if meta != nil {
			d.deliverReplyFrom(
				meta, ev.Content, ev.MessageID,
			)
			return
		}
		_ = d.discord.SendHint(
			"That session has already received " +
				"a response. No action needed.")
		return
	}

	// Bare message — check if only one session is
	// waiting and route to it.
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

// handleReaction routes a reaction to the correct
// session via FIFO.
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

// handleClear processes a /clear slash command.
func (d *Daemon) handleClear(cmd discord.ClearCommand) {
	result := d.clearNotifications(cmd.SessionID)
	if err := d.discord.RespondToInteraction(
		cmd.Interaction, result,
	); err != nil {
		log.Printf("respond to /clear: %v", err)
	}
	log.Printf("clear command: %s", result)
}

// findSessionByMsgID finds a session whose
// NotificationMsgID matches the given Discord message
// ID.
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

// notifiedSessions returns sessions that have an
// active notification.
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
```

**Step 7: Delete dead code**

Remove these methods from `daemon.go`:
- `processReplies` (entire function)
- `processReactions` (entire function)
- `processCommands` (entire function)

Remove these from the `Daemon` struct (already done in step 3):
- `lastProcessedID`
- `hintedMsgIDs`
- `lastCmdCheckID`
- `hasEverNotified`

**Step 8: Run full test suite**

Run: `cd /home/honor/src/reverie/claude-notify && make test`

Expected: All tests pass

**Step 9: Commit**

```bash
cd /home/honor/src/reverie/claude-notify
git add internal/daemon/daemon.go \
  internal/daemon/daemon_test.go
git commit -m "feat: migrate daemon to event-driven gateway architecture

Replace polling-based processReplies, processReactions,
and processCommands with channel-based event handlers
driven by Discord gateway events. The Run loop now selects
on gateway channels alongside the tick timer. Tick is
reduced to session scanning + notification sending only.

Remove dead code: lastProcessedID, hintedMsgIDs,
lastCmdCheckID, hasEverNotified, and all three
process* methods."
```

---

### Task 5: Remove Dead Polling Methods from Discord Client

**Files:**
- Modify: `internal/discord/client.go`

**Step 1: Remove dead methods**

Delete these methods from `client.go`:
- `FetchReplies` (replaced by `onMessageCreate` handler)
- `FetchRecentUserMessages` (replaced by `/clear` slash command)
- `FetchUserReaction` (replaced by `onMessageReactionAdd` handler)

**Step 2: Verify compilation**

Run: `cd /home/honor/src/reverie/claude-notify && go build ./...`

Expected: Build succeeds. If any references remain, fix them.

**Step 3: Run full test suite**

Run: `cd /home/honor/src/reverie/claude-notify && make test`

Expected: All tests pass

**Step 4: Commit**

```bash
cd /home/honor/src/reverie/claude-notify
git add internal/discord/client.go
git commit -m "refactor: remove dead polling methods from Discord client

FetchReplies, FetchRecentUserMessages, and
FetchUserReaction are no longer called — gateway event
handlers replaced them in the daemon. Reduces API
surface and eliminates polling overhead."
```

---

### Task 6: Improve ClearNotificationMessages with Pagination + Bulk Delete

**Files:**
- Modify: `internal/discord/client.go`

**Step 1: Replace ClearNotificationMessages with paginated version**

Edit `internal/discord/client.go`, replace `ClearNotificationMessages`:

```go
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

		for _, msg := range msgs {
			// Stop if we've gone past the 14-day
			// bulk-delete window.
			ts, _ := msg.Timestamp.Parse()
			if ts.Before(cutoff) {
				goto done
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

		beforeID = msgs[len(msgs)-1].ID
	}

done:
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
				// Single remaining — use regular
				// delete.
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
```

**Note:** Bulk delete may not work in DM channels (Discord API limitation). The fallback to individual deletes handles this gracefully. The pagination still improves coverage over the old 50-message limit.

**Step 2: Verify compilation**

Run: `cd /home/honor/src/reverie/claude-notify && go build ./...`

Expected: Build succeeds

**Step 3: Run full test suite**

Run: `cd /home/honor/src/reverie/claude-notify && make test`

Expected: All tests pass

**Step 4: Commit**

```bash
cd /home/honor/src/reverie/claude-notify
git add internal/discord/client.go
git commit -m "feat: paginate clear through 14-day history with bulk delete

ClearNotificationMessages now paginates through DM
history up to 14 days instead of checking only 50
messages. Uses ChannelMessagesBulkDelete when possible,
falls back to individual deletes."
```

---

### Task 7: Update daemon_cmd.go for Gateway Lifecycle

**Files:**
- Modify: `cmd/claude-notify/daemon_cmd.go`

**Step 1: Gateway is already opened by NewClient now — verify no changes needed**

The current `runDaemon` calls `discord.NewClient(token, userID)` which now opens the gateway. `defer dc.Close()` already calls `s.Close()` which closes the gateway. No changes should be needed, but verify.

**Step 2: Verify the build compiles**

Run: `cd /home/honor/src/reverie/claude-notify && go build ./cmd/claude-notify/`

Expected: Build succeeds

**Step 3: Run full test suite**

Run: `cd /home/honor/src/reverie/claude-notify && make test`

Expected: All tests pass

**Step 4: Commit (only if changes were needed)**

---

### Task 8: Update README and CLAUDE.md

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

**Step 1: Update README**

Key changes:
- Bot invite URL must include `applications.commands` scope
- Remove `/clear` text command documentation
- Add `/clear` slash command documentation
- Update "How It Works" section — replace polling with gateway events
- Note: bot now maintains a persistent websocket (outbound only)

**Step 2: Update CLAUDE.md**

Add note about gateway connection to architecture section.

**Step 3: Commit**

```bash
cd /home/honor/src/reverie/claude-notify
git add README.md CLAUDE.md
git commit -m "docs: update for gateway migration and /clear slash command"
```

---

### Task 9: Build + Manual Smoke Test

**Step 1: Build**

Run: `cd /home/honor/src/reverie/claude-notify && make build`

Expected: Binary at `./build/claude-notify`

**Step 2: Run full test suite one final time**

Run: `cd /home/honor/src/reverie/claude-notify && make test`

Expected: All tests pass, no race conditions

**Step 3: Install and restart service**

```bash
cd /home/honor/src/reverie/claude-notify
make install
systemctl --user restart claude-notify
systemctl --user status claude-notify
journalctl --user -u claude-notify -f
```

Verify in logs:
- "gateway connected as [bot-name]"
- "registered /clear slash command"
- "daemon started, watching ..."

**Step 4: Manual smoke tests**

1. Open a claude-notify wrapped session, let it go idle for 15 minutes → verify notification arrives (not 15 seconds)
2. Reply to notification via Discord → verify text is submitted (not just pasted)
3. Use `/clear` slash command in DMs → verify ephemeral response, notifications deleted
4. React with ✅ on a notification → verify reaction is delivered instantly

**Step 5: Commit version bump if desired**

---

## Task Dependency Order

```
Task 1 (FIFO fix) ─────────────────────────┐
Task 2 (RemoteMode fix) ───────────────────┤
Task 3 (Gateway + client) ─┬───────────────┤
                            │               │
Task 4 (Daemon refactor) ──┤ (depends on 3) │
                            │               │
Task 5 (Dead code removal)─┘ (depends on 4) │
                                            │
Task 6 (Clear pagination) ─────────────────┤
                                            │
Task 7 (daemon_cmd verify) ────────────────┤ (depends on 3,4)
                                            │
Task 8 (Docs) ─────────────────────────────┤
                                            │
Task 9 (Smoke test) ───────────────────────┘ (depends on all)
```

Tasks 1, 2, 3, and 6 can be executed in parallel.
Tasks 4 and 5 are sequential after 3.
Task 7 depends on 3 and 4.
Task 8 can be done anytime before 9.
Task 9 is the final gate.
