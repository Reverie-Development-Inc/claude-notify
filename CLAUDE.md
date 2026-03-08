# claude-notify

Discord DM notifications for idle Claude Code sessions.

## Build

    make build

## Test

    make test

## Install

    make install   # builds + installs binary to ~/.local/bin

## Architecture

- `cmd/claude-notify/main.go` — CLI entrypoint (cobra)
- `internal/config/` — YAML + SSM config loading
- `internal/daemon/` — session watcher, timer mgmt
- `internal/discord/` — REST API client (DM send/poll)
- `internal/sanitize/` — message sanitization
- `internal/session/` — metadata file read/write
- `internal/wrapper/` — PTY relay + FIFO stdin merge
- `install/` — systemd unit, hooks.json

## Key Patterns

- AWS SDK v2 with pointer types
- discordgo v0.28.1 for Discord REST API
- PTY relay via creack/pty for transparent stdin merge
- Session metadata as JSON files in ~/.local/state/
- FIFO files in $XDG_RUNTIME_DIR for reply injection
- Secrets from SSM, never on disk

## Dev Service Ports

None — this is a local daemon, no network listeners.
