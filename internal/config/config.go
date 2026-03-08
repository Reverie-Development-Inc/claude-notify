package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config holds all claude-notify configuration.
// Loaded from YAML, with environment variable overrides.
// Secrets (bot token) are NOT stored here — only SSM
// paths that point to where the secret lives.
type Config struct {
	Discord DiscordConfig `yaml:"discord"`
	Notify  NotifyConfig  `yaml:"notify"`
	Session SessionConfig `yaml:"session"`
}

// DiscordConfig holds Discord-related settings.
type DiscordConfig struct {
	// UserID is the Discord user ID to DM.
	UserID string `yaml:"user_id"`
	// BotTokenSSM is the SSM parameter path containing
	// the bot token. Resolved at startup, never stored.
	BotTokenSSM string `yaml:"bot_token_ssm"`
	// BotTokenEnv is an optional env var name holding
	// the bot token directly (skips SSM). If empty,
	// CLAUDE_NOTIFY_BOT_TOKEN is checked as a default.
	BotTokenEnv string `yaml:"bot_token_env"`
}

// NotifyConfig controls notification behavior.
type NotifyConfig struct {
	// DelayMinutes is how long to wait after the last
	// tool output before sending a notification.
	DelayMinutes int `yaml:"delay_minutes"`
	// MaxPreviewChars caps the preview text length in
	// the Discord message.
	MaxPreviewChars int `yaml:"max_preview_chars"`
	// IncludeSuggestions controls whether suggested
	// next actions are included in the notification.
	IncludeSuggestions bool `yaml:"include_suggestions"`
}

// SessionConfig allows overriding XDG-derived paths.
type SessionConfig struct {
	StateDirOverride   string `yaml:"state_dir"`
	RuntimeDirOverride string `yaml:"runtime_dir"`
}

// applyDefaults fills in zero-value fields with sane
// defaults.
func (c *Config) applyDefaults() {
	if c.Notify.DelayMinutes == 0 {
		c.Notify.DelayMinutes = 15
	}
	if c.Notify.MaxPreviewChars == 0 {
		c.Notify.MaxPreviewChars = 500
	}
}

// applyEnvOverrides reads CLAUDE_NOTIFY_* environment
// variables and applies them over file-loaded values.
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv(
		"CLAUDE_NOTIFY_DISCORD_USER_ID",
	); v != "" {
		c.Discord.UserID = v
	}
	if v := os.Getenv(
		"CLAUDE_NOTIFY_DELAY_MINUTES",
	); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Notify.DelayMinutes = n
		}
	}
	if v := os.Getenv(
		"CLAUDE_NOTIFY_BOT_TOKEN_SSM",
	); v != "" {
		c.Discord.BotTokenSSM = v
	}
}

// StateDir returns the directory for persistent state
// (session metadata, etc.). Follows XDG Base Directory
// Specification: ~/.local/state/claude-notify
func (c *Config) StateDir() string {
	if c.Session.StateDirOverride != "" {
		return os.ExpandEnv(c.Session.StateDirOverride)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(
		home, ".local", "state", "claude-notify",
	)
}

// RuntimeDir returns the directory for runtime data
// (PID files, sockets). Uses XDG_RUNTIME_DIR if set,
// otherwise falls back to os.TempDir().
func (c *Config) RuntimeDir() string {
	if c.Session.RuntimeDirOverride != "" {
		return os.ExpandEnv(c.Session.RuntimeDirOverride)
	}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "claude-notify")
	}
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home,
			"Library", "Caches", "claude-notify")
	}
	return filepath.Join(os.TempDir(), "claude-notify")
}

// Load reads configuration from a YAML file, applies
// defaults, then applies environment variable overrides.
// Missing file is not an error — defaults are used.
// Invalid YAML is an error.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Notify: NotifyConfig{
			IncludeSuggestions: true,
		},
	}

	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	cfg.applyDefaults()
	cfg.applyEnvOverrides()
	return cfg, nil
}
