# claude-notify

Discord DM notifications for idle Claude Code sessions, with reply
injection back into the terminal.

## Features

- Detects when Claude Code is waiting for input (via hooks)
- Sends a Discord DM after a configurable delay (default 15 min)
- Includes a sanitized preview of Claude's last message
- Suggests numbered quick-reply options
- Injects Discord replies back into the Claude Code session
  via named pipe (FIFO), as if typed in the terminal
- Supports multiple concurrent sessions
- Secrets via AWS SSM or environment variable (no AWS required)
- Built-in health check (`claude-notify health`)

## Architecture

```
Terminal
  claude() shell function
    |
    v
claude-notify wrap -- claude "$@"
    |  creates FIFO, writes session metadata,
    |  merges stdin + FIFO -> claude binary
    |
    v
claude-notify daemon (systemd user service)
    |  watches session metadata files
    |  starts idle timer on Stop hook
    |  sends DM after delay (REST)
    |  receives replies + reactions (gateway)
    |  handles /clear slash command
    |  writes reply -> FIFO
    |
    v
Discord (gateway + REST)
    persistent websocket for events
    REST for sending notifications
```

## Prerequisites

- **Go 1.24+**
- **Discord bot** with DM permissions (Send Messages,
  Read Message History) and `applications.commands` scope
  for the `/clear` slash command. No server/guild
  permissions needed.
- **Bot token** provided via one of:
  - `CLAUDE_NOTIFY_BOT_TOKEN` environment variable, OR
  - AWS SSM Parameter Store (requires AWS account + credentials)
- **Linux** (systemd), **macOS** (launchd), or **Windows**

## Quick Start

### 1. Build and install

```sh
git clone https://github.com/Reverie-Development-Inc/claude-notify.git
cd claude-notify
make install
```

This builds the binary and copies it to `~/.local/bin/`.

### 2. Run interactive setup

```sh
claude-notify setup
```

Prompts for your Discord user ID, token source (SSM path or
env var), AWS region (if using SSM), and notification delay.
Writes config to `~/.config/claude-notify/config.yaml`.

### 3. Set your bot token

**Option A: Environment variable (no AWS required)**

```sh
export CLAUDE_NOTIFY_BOT_TOKEN="your-bot-token-here"
```

Add to your shell profile to persist across sessions.

**Option B: AWS SSM Parameter Store**

```sh
aws ssm put-parameter \
  --name "/claude-notify/bot-token" \
  --type SecureString \
  --value "your-bot-token-here"
```

### 4. Install the systemd service

```sh
make install-service
systemctl --user start claude-notify
```

Edit `~/.config/systemd/user/claude-notify.service` if you
need to set `AWS_PROFILE` or other environment variables.

### 5. Add the shell wrapper

Add to your `~/.zshrc` or `~/.bashrc`:

```sh
claude() {
  claude-notify wrap -- \
    /path/to/claude "$@"
}
```

The `setup` command prints the exact snippet with your
detected Claude binary path.

### 6. Verify PATH

Ensure `~/.local/bin` is in your `PATH`:

```sh
echo $PATH | grep -q "$HOME/.local/bin" \
  || echo 'export PATH="$HOME/.local/bin:$PATH"' \
     >> ~/.zshrc
```

### 7. Install Claude Code hooks

Add these hook definitions to your Claude Code settings
(`~/.claude/settings.json`). If you already have hooks
configured, merge the `Stop` and `UserPromptSubmit`
entries into your existing hooks arrays:

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "claude-notify session-update --status waiting",
        "timeout": 5
      }]
    }],
    "UserPromptSubmit": [{
      "hooks": [{
        "type": "command",
        "command": "claude-notify session-update --status active",
        "timeout": 5
      }]
    }]
  }
}
```

## Configuration

Config file: `~/.config/claude-notify/config.yaml`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `discord.user_id` | string | (required) | Your Discord user ID |
| `discord.bot_token_ssm` | string | `/claude-notify/bot-token` | SSM parameter path for bot token |
| `discord.bot_token_env` | string | `""` | Custom env var name for bot token |
| `discord.aws_region` | string | `us-east-1` | AWS region for SSM lookups |
| `notify.delay_minutes` | int | `15` | Minutes idle before notification |
| `notify.max_preview_chars` | int | `500` | Max preview length in DM |
| `notify.include_suggestions` | bool | `true` | Include quick-reply suggestions |
| `session.state_dir` | string | `~/.local/state/claude-notify` | Session metadata directory |
| `session.runtime_dir` | string | `$XDG_RUNTIME_DIR/claude-notify` | FIFO and runtime files |

### Environment variable overrides

| Variable | Overrides |
|----------|-----------|
| `CLAUDE_NOTIFY_DISCORD_USER_ID` | `discord.user_id` |
| `CLAUDE_NOTIFY_DELAY_MINUTES` | `notify.delay_minutes` |
| `CLAUDE_NOTIFY_BOT_TOKEN_SSM` | `discord.bot_token_ssm` |
| `CLAUDE_NOTIFY_BOT_TOKEN` | Bot token directly (skips SSM) |
| `CLAUDE_NOTIFY_AWS_REGION` | `discord.aws_region` |
| `AWS_REGION` | Fallback AWS region if neither config nor `CLAUDE_NOTIFY_AWS_REGION` is set |

## How It Works

1. You run `claude "fix the bug"` (which invokes the shell
   wrapper).
2. The wrapper creates a FIFO, writes session metadata, and
   launches Claude Code with stdin merged from both the
   terminal and the FIFO.
3. Claude Code works, then stops and waits for input.
4. The `Stop` hook fires, calling `claude-notify session-update
   --status waiting`, which records the idle timestamp and a
   preview of Claude's last message.
5. The daemon detects the idle session and starts a timer.
6. After 15 minutes (configurable), the daemon sends a Discord
   DM with the preview and quick-reaction emojis (✅ ❌ 👀).
7. You reply in Discord (react with an emoji, or use
   Discord's **Reply** feature on the notification to
   type a custom response).
8. The daemon receives the reply instantly via the
   Discord gateway, validates it (correct sender), then
   writes it to the session's FIFO.
9. Claude Code receives the reply as stdin and continues.
10. If you type in the terminal instead, the `UserPromptSubmit`
    hook fires, cancelling the notification/polling cycle.

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

### `/clear` Slash Command

Use the `/clear` slash command in your DM with the bot to
remove stale notification messages:

- `/clear` — removes all notification embeds (up to 14
  days old)
- `/clear session:ab12` — removes only notifications for
  the given session ID

The response is **ephemeral** (only visible to you), so
`/clear` never leaves a trail in your DM history. Works
even when no Claude Code sessions are active.

## Security

- **FIFO permissions**: Created with `0600` on tmpfs
  (`$XDG_RUNTIME_DIR`). Only the owning user can write.
- **Secret sanitization**: Message previews are truncated and
  stripped of patterns matching secrets (env vars, bearer
  tokens, base64 blobs, connection strings).
- **Sender validation**: Discord replies are accepted only from
  the configured user ID, with timestamps after the
  notification was sent.
- **Token storage**: Bot token is held in memory only. Never
  written to disk by the daemon. Sourced from SSM or env var.
- **Stale FIFO cleanup**: Daemon sweeps for orphaned FIFOs
  whose PIDs no longer exist.

## Troubleshooting

Run the built-in health check to verify your setup:

```sh
claude-notify health
```

This checks: config file, daemon process, bot token,
Discord connectivity, active sessions, and Claude Code
hooks. Example output:

```
config     OK    /home/user/.config/claude-notify/config.yaml
daemon     OK    running
token      OK    NjE2...OTIz
discord    OK    connected as bot
sessions   OK    2 active (/home/user/.local/state/claude-notify)
hooks      OK    found in settings.json

All checks passed.
```

## Platform Support

| Feature | Linux | macOS | Windows |
|---------|-------|-------|---------|
| Notifications (Discord DM) | Yes | Yes | Yes |
| Reply injection (FIFO) | Yes | Yes | No |
| PTY wrapper | Yes | Yes | No |
| Daemon auto-start | systemd | launchd | Manual |
| Shell wrapper | zsh/bash | zsh/bash | PowerShell |

**Windows**: Hooks and notifications work normally.
Reply injection is not available — respond by returning
to the terminal. For full features, use
[WSL2](https://learn.microsoft.com/en-us/windows/wsl/).

## License

MIT -- see [LICENSE](LICENSE).
