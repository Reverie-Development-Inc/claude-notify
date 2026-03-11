# claude-notify Stabilization Design

## Status: Approved

## Context

Taken off open-source due to bugs. Three user-reported issues:
1. FIFO reply injection intermittently pastes text without submitting
2. `/clear` command is text-based (DM clutter), slow, fails when sessions closed
3. Idle timer not respected — notifications fire at 15s instead of 15min

## Fix 1: FIFO Injection Timing

**Root cause**: `writeToFIFO` writes `content + "\r"` as a single
`fmt.Fprint`. The wrapper's `io.Copy` delivers this as one chunk to
the PTY master. Claude Code's Ink/React TUI receives the text and
submit keystroke in the same `stdin.read()` — the `\r` gets swallowed
during the re-render triggered by the text content.

**Fix**: Split into two writes with a 50ms delay. Keep `\r` (carriage
return = Enter in PTY raw mode; `\n` = newline/Shift+Enter).

```go
// Write text content
fmt.Fprint(f, content)
// Let TUI process pasted text
time.Sleep(50 * time.Millisecond)
// Submit
fmt.Fprint(f, "\r")
```

**File**: `internal/daemon/platform_unix.go`

## Fix 2: Gateway Connection + Slash Commands

**Current**: REST-only, outbound polling every 10s for replies,
reactions, and text-based `/clear` commands.

**Change**: Add minimal Discord gateway (outbound websocket).
Event-driven handlers replace all polling.

### Intents (non-privileged)

```go
discordgo.IntentsDirectMessages |
    discordgo.IntentsDirectMessageReactions
```

### Event Handlers

| Event | Replaces | Purpose |
|-------|----------|---------|
| `MessageCreate` | `processReplies` | Route DM replies to sessions |
| `MessageReactionAdd` | `processReactions` | Route reactions to sessions |
| `InteractionCreate` | `processCommands` | Handle `/clear` slash command |

### Slash Command

Registered on daemon startup via REST:

```
/clear [session:optional]
```

Response is ephemeral (only visible to invoker, no DM trail).

### Tick Loop (reduced scope)

Retains: session scanning, dead PID cleanup, shouldNotify,
sendNotification.

Removed: processReplies, processReactions, processCommands.

### Architecture

```
Daemon
├── Gateway (outbound websocket to Discord)
│   ├── InteractionCreate → /clear handler
│   ├── MessageReactionAdd → reaction router
│   └── MessageCreate → reply router
├── Tick loop (10s)
│   ├── Scan metadata, clean dead PIDs
│   └── shouldNotify → sendNotification (REST)
└── REST client (sending only, unchanged)
```

### Open-Source Considerations

- No new secrets (same bot token)
- No listening ports (outbound websocket only)
- No privileged intents
- Users add `applications.commands` scope when inviting bot
- Auto-reconnect handled by discordgo

## Fix 3: RemoteMode Sticking

**Root cause**: `UpdateStatus` for `StatusActive` does not clear
`RemoteMode`. Intentional for the FIFO-injection happy path, but
breaks when user returns to terminal — remote mode persists, causing
next notification to use 15-second debounce instead of 15-minute
delay.

**Fix**: In `UpdateStatus` StatusActive case, clear `RemoteMode` if
`LastInjectedAt` is zero or > 30 seconds ago (meaning the user
returned to the terminal, not a FIFO echo):

```go
case StatusActive:
    // ... existing clears ...
    if m.RemoteMode && (m.LastInjectedAt == 0 ||
        time.Since(
            time.Unix(m.LastInjectedAt, 0),
        ) > 30*time.Second) {
        m.RemoteMode = false
    }
```

**File**: `internal/session/metadata.go`

## Fix 4: Clear Command Improvements

**Problems with current text-based `/clear`**:
- Leaves text in DM history
- 10s polling delay
- Only scans last 50 messages (misses older notifications)
- `botID` detection via `State.User` is nil in REST-only mode

**Fix**: Slash command (Fix 2) + paginated delete + bulk delete.

- Slash command handler calls `clearNotifications` directly
- Ephemeral response (zero DM trace)
- Paginate with `before` cursor until 14-day cutoff
- Use `ChannelMessagesBulkDelete` for messages < 14 days old
- Gateway provides `State.User` for reliable bot ID detection

## Dead Code Removal

Methods removed:
- `FetchReplies`, `FetchRecentUserMessages`, `FetchUserReaction`
- `processReplies`, `processReactions`, `processCommands`

Fields removed from Daemon struct:
- `lastProcessedID`, `hintedMsgIDs`, `lastCmdCheckID`

## No Config Changes

Same `config.yaml`, same bot token, same user ID.
