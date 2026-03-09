# Changelog

## v1.0.0 (2026-03-08)

Initial public release.

### Features

- Discord DM notifications for idle Claude Code sessions
- Reply injection via named pipe (FIFO) on Linux and macOS
- PTY wrapper for transparent stdin merging
- Secret sanitization (AWS keys, GitHub tokens, Slack tokens,
  OpenAI keys, bearer tokens, connection strings, base64 blobs,
  private key blocks)
- Multi-session support with reply-to routing
- Auto-delete Discord notifications when user returns to session
- `/clear` Discord command for orphaned notification cleanup
- Quick-reply suggestions with numbered options
- Configurable idle delay (default 15 minutes)
- AWS SSM or environment variable for bot token storage
- XDG Base Directory compliant paths
- Claude Code hooks integration (Stop + UserPromptSubmit)

### Platform Support

- Linux: full support (systemd user service)
- macOS: full support (launchd agent)
- Windows: notification-only (no reply injection; use WSL2 for
  full features)

### Security

- FIFO created with 0600 permissions on tmpfs
- Message previews sanitized before sending to Discord
- Reply validation: sender ID + timestamp checks
- Bot token held in memory only, never written to disk
