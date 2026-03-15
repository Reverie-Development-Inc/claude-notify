# @claude-notify/opencode

Discord notifications for idle [OpenCode](https://opencode.ai) sessions.

When OpenCode finishes working and waits for your input, this plugin sends a Discord DM (or channel message) with the last assistant message. You can reply from Discord — via text or reaction shortcuts — and the response is injected back into your OpenCode session.

## Prerequisites

- A Discord bot with **Send Messages** and **Read Message History** permissions
- Bot token stored securely (environment variable, never committed)
- Your Discord user ID

## Installation

Add to your `opencode.json`:

```json
{
  "plugin": ["@claude-notify/opencode"]
}
```

Or for a local checkout:

```json
{
  "plugin": ["./path/to/claude-notify/plugins/opencode"]
}
```

## Configuration

Set these environment variables (never commit them):

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CLAUDE_NOTIFY_BOT_TOKEN` | Yes | — | Discord bot token |
| `CLAUDE_NOTIFY_USER_ID` | Yes | — | Your Discord user ID |
| `CLAUDE_NOTIFY_CHANNEL_ID` | No | — | Guild channel ID (DM if unset) |
| `CLAUDE_NOTIFY_ALLOWED_USERS` | No | — | Comma-separated user IDs |
| `CLAUDE_NOTIFY_DELAY_SECONDS` | No | 300 | Seconds before notification |
| `CLAUDE_NOTIFY_PREVIEW_LENGTH` | No | 500 | Max preview characters |

If `CLAUDE_NOTIFY_BOT_TOKEN` is not set, the plugin silently disables itself.

## Notification Modes

- **DM** (default) — Sends embeds to your Discord DMs
- **Channel** — Sends embeds to a guild text channel (reaction-only; text replies are DM-only)

## Reactions

| Emoji | Action |
|-------|--------|
| ✅ | "Yes, continue" |
| ❌ | "No, stop here" |
| 👀 | "Show me what you have so far" |
| 1️⃣–5️⃣ | Numbered suggestion |

Or reply to the notification message with custom text (DM mode only).

## Embed States

- **Yellow** — Session idle, waiting for your response
- **Green** — Response delivered, session working
- **Red** — Session ended (auto-deleted after 30s)

## Security

- **Never commit your bot token** to version control
- Store tokens in a secret manager (AWS SSM, 1Password, etc.) and export to env
- Only the owner and explicitly allowed users can interact with notifications
- Message previews are sanitized (secrets, tokens, keys are redacted)
- Text replies require Discord reply-to reference (prevents cross-session confusion)

## Using with Claude Code

If you also use [claude-notify](../../README.md) with Claude Code, you can share the same Discord bot token. Both systems send to the same DM channel. Embed titles distinguish "Claude Code" from "OpenCode", and reply-to binding prevents cross-session confusion.
