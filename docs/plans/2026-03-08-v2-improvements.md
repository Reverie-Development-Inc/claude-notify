# claude-notify v2 improvements

## Status: Complete (Phase 1 + Phase 2)

## Phase 1: Critical fixes (reply routing + sanitization)
**DONE** — commit 1915abe

### 1. Fix multi-session reply routing
**Problem**: All waiting sessions race to claim any Discord reply.
Whichever gets polled first wins, regardless of user intent.

**Solution**: Discord reply-to routing with fallback.

**Changes:**

#### a. Refactor daemon reply checking
- `daemon.go`: Instead of per-session `checkForReply`, fetch
  messages once per tick and route them.
- New method: `processReplies(waitingSessions []*session.Metadata)`
  1. Fetch last 10 messages from DM channel (single API call)
  2. For each message from the configured user:
     - Has `MessageReference` → match `MessageReference.MessageID`
       against waiting sessions' `NotificationMsgID` → route
     - No reference, 1 waiting session → route to it
     - No reference, multiple waiting → send hint (once per bare
       message, track by message ID to avoid repeating)
  3. Track `lastProcessedMsgID` to avoid re-processing

#### b. Update Discord client
- `client.go`: Add `FetchRecentMessages(afterID) []*Message`
  (raw fetch, no routing logic — daemon handles routing)
- `client.go`: Add `SendHint(text)` — sends a plain DM
  (not an embed) telling user to use reply-to
- Remove `PollForReply` (replaced by FetchRecentMessages)

#### c. Update validator
- `validate.go`: No changes needed — still validates sender +
  timestamp per message

### 2. Expand sanitization patterns
**File**: `internal/sanitize/sanitize.go`

Add patterns for:
- Private key blocks: `-----BEGIN.*PRIVATE KEY-----`
- Slack tokens: `xox[bpras]-[a-zA-Z0-9-]+`
- OpenAI/common API keys: `sk[-_](live|test|proj)[-_]\S+`
- GitHub tokens: `gh[ps]_[A-Za-z0-9_]+`
- Case-insensitive env vars for common secrets:
  `(?i)(api[_-]?key|secret|password|token)\s*[=:]\s*\S+`

### 3. Track last-processed message ID
- `daemon.go`: Store `lastProcessedMsgID string` on Daemon struct
- Only fetch messages after this ID each tick
- Prevents double-processing and deduplicates replies

## Phase 2: Cross-platform
**DONE** — commit c32cd0c

### 4. macOS support
- Add `install/com.claude-notify.daemon.plist` (launchd)
- Build tags for `/proc` vs `sysctl` PPID lookup
- XDG fallback → `~/Library/Caches/claude-notify`
- `setup` command detects OS and prints correct instructions
- Makefile: `install-service-macos` target

### 5. Windows notification-only mode
- Build tags: `//go:build !windows` for wrapper/FIFO code
- Windows: hooks still work, daemon sends DMs, no reply injection
- Document WSL2 for full feature set
- `setup` detects PowerShell profile path

### 6. Rate limiting
- Exponential backoff on Discord 429 responses
- Cap concurrent API calls per tick
