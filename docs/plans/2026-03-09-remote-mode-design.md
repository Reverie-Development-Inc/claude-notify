# Remote Mode & Notification UX — Design

Status: approved
Date: 2026-03-09

## Problem

When a user replies to a claude-notify DM from Discord, there
is no acknowledgement that the reply was received. The user
also has no way to continue interacting remotely — the idle
timer resets to 15 minutes, forcing them to wait before hearing
back. The notification preview is raw truncated output rather
than a purposeful summary.

## Features

### 1. Remote Mode

When the daemon injects a Discord reply via FIFO, it marks the
session as `remote_mode: true` in metadata.

**While active:**
- `Stop` hook fires → daemon checks for `[notify: ...]` tag
- `[notify: summary]` → send DM immediately (no delay)
- `[notify: none]` → skip DM, stay in remote mode
- No tag (fallback) → raw preview with 15-second debounce

**Exit condition:** `UserPromptSubmit` fires without a
preceding daemon FIFO injection in the same cycle. The daemon
tracks whether it injected a reply — if the hook fires from
real terminal input (not FIFO echo), remote mode ends.

### 2. Notification Message Format

**Before:**
```
Claude waiting (project-name)
─────────────────────────────
{raw preview, up to 500 chars}

**1.** Yes, continue
**2.** No, stop here
**3.** Show me what you have so far

Or type a custom reply.
```

**After:**
```
Claude waiting (project-name)
─────────────────────────────
{[notify: ...] summary OR raw preview fallback}

React below, or **reply** to this message to
type something custom.
─────────────────────────────
Session: project-name #a3f2
[✅] [❌] [👀]  ← bot-added reactions
```

Changes:
- Summary from `[notify: ...]` tag replaces raw preview
- Numbered suggestions removed — reactions carry the meaning
- Single-line instruction replaces verbose suggestion block
- Bot adds ✅ ❌ 👀 reactions to its own message after sending

Reaction meanings:
- ✅ → "Yes, continue"
- ❌ → "No, stop here"
- 👀 → "Show me what you have so far"

### 3. Reply Acknowledgement

Regardless of reply type (reaction or typed message), the
notification is "resolved":

1. Daemon detects reply, validates sender + timestamp
2. Expands reaction/shortcut to full text, writes to FIFO
3. **Notification message**: remove bot's ✅ ❌ 👀 reactions,
   change embed color to grey (visually "handled")
4. **Typed replies only**: react with ✅ on the user's reply

Resolved notifications are visually distinct when scrolling
DM history: no reaction buttons, greyed embed = done.

**Failure case:** FIFO write fails (session dead) → react with
❌ on the reply, send follow-up: "Session is no longer active."

### 4. Claude Awareness (`[notify: ...]` Tag)

**CLAUDE.md instruction** (global):
```
When your input begins with [discord], the user is replying
remotely via Discord and can't see your full terminal output.
End your response with one of:
- [notify: one-line summary of what you need]
- [notify: none] if no user input is needed yet
```

**Daemon side:**
- FIFO injection prefixed with `[discord] ` (e.g.
  `[discord] Yes, continue`)
- `session-update` hook parses `[notify: ...]` from Claude's
  last message, stores summary in metadata separately
- `[notify: none]` → `skip_notification: true` in metadata
- No tag → fallback to raw preview with 15-second debounce

**Tag visibility:** Left visible in terminal output as a paper
trail that a notification was sent. Stripped from the Discord
DM (the summary IS the embed content).

### 5. Reaction Polling

Reactions detected via REST polling (no gateway connection).

**Added to existing 10-second tick loop:** For each notified
session with a `NotificationMsgID`, fetch reactions via
`GET /channels/{id}/messages/{id}/reactions/{emoji}` for each
of ✅ ❌ 👀. Check for the configured user ID.

**Cost:** 3 API calls per notified session per tick. ~18
calls/min per active notification. Well within Discord rate
limits (50 req/sec per route).

**Optimization:** Check ✅ first, skip ❌ and 👀 if found.
Stop polling once handled.

## Data Model Changes

### Session Metadata (new fields)

```go
type Metadata struct {
    // ... existing fields ...
    RemoteMode       bool   `json:"remote_mode"`
    SkipNotification bool   `json:"skip_notification"`
    NotifySummary    string `json:"notify_summary"`
    LastInjectedAt   int64  `json:"last_injected_at"`
}
```

- `RemoteMode`: true when last reply came from Discord
- `SkipNotification`: true when Claude sent `[notify: none]`
- `NotifySummary`: parsed summary from `[notify: ...]` tag
- `LastInjectedAt`: unix timestamp of last FIFO injection,
  used to distinguish FIFO echo from real terminal input

## Files Affected

| File | Change |
|------|--------|
| `internal/session/metadata.go` | New fields, parse/write |
| `internal/daemon/daemon.go` | Remote mode logic, reaction polling, notification cleanup |
| `internal/discord/client.go` | `AddReaction()`, `RemoveReaction()`, `EditEmbed()`, `FetchReactions()` |
| `cmd/claude-notify/session_update.go` | Parse `[notify: ...]` tag from hook input |
| `internal/sanitize/sanitize.go` | Strip `[notify: ...]` from preview (optional) |
| `README.md` | Document reactions, remote mode, `[notify: ...]` tag |
| `~/.claude/CLAUDE.md` | Add `[discord]` / `[notify: ...]` instruction |

## Non-Goals

- Gateway/websocket connection (stay REST-only)
- Discord buttons/components (reactions are sufficient)
- Per-project notification settings
- Multiple notification channels (Slack, email)
