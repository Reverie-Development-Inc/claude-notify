# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in claude-notify,
please report it privately through
[GitHub Security Advisories](https://github.com/Reverie-Development-Inc/claude-notify/security/advisories/new).

Do not open a public issue for security vulnerabilities.

## Scope

Security-relevant areas of this project include:

- **Secret sanitization** (`internal/sanitize/`) — patterns
  that strip secrets from message previews before sending to
  Discord.
- **FIFO permissions** (`internal/wrapper/`) — named pipes
  created with restrictive permissions.
- **Reply validation** (`internal/discord/`) — sender ID and
  timestamp checks on incoming Discord messages.
- **Token handling** — bot token held in memory only, sourced
  from SSM or environment variable.

## Supported Versions

Only the latest release on `main` receives security updates.
