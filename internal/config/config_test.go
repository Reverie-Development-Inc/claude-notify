package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Notify.DelayMinutes != 15 {
		t.Errorf("want delay 15, got %d",
			cfg.Notify.DelayMinutes)
	}
	if cfg.Notify.MaxPreviewChars != 500 {
		t.Errorf("want preview 500, got %d",
			cfg.Notify.MaxPreviewChars)
	}
	if cfg.Notify.IncludeSuggestions != true {
		t.Error("want suggestions true")
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yamlContent := `discord:
  user_id: "123456"
  bot_token_ssm: "/test/bot-token"
notify:
  delay_minutes: 10
  max_preview_chars: 300
  include_suggestions: false
`
	_ = os.WriteFile(path, []byte(yamlContent), 0600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Discord.UserID != "123456" {
		t.Errorf("want user_id 123456, got %s",
			cfg.Discord.UserID)
	}
	if cfg.Discord.BotTokenSSM != "/test/bot-token" {
		t.Errorf("want ssm path, got %s",
			cfg.Discord.BotTokenSSM)
	}
	if cfg.Notify.DelayMinutes != 10 {
		t.Errorf("want delay 10, got %d",
			cfg.Notify.DelayMinutes)
	}
	if cfg.Notify.MaxPreviewChars != 300 {
		t.Errorf("want preview 300, got %d",
			cfg.Notify.MaxPreviewChars)
	}
	if cfg.Notify.IncludeSuggestions != false {
		t.Error("want suggestions false")
	}
}

func TestLoadConfig_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	t.Setenv("CLAUDE_NOTIFY_DISCORD_USER_ID", "override")
	t.Setenv("CLAUDE_NOTIFY_DELAY_MINUTES", "15")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Discord.UserID != "override" {
		t.Errorf("want override, got %s",
			cfg.Discord.UserID)
	}
	if cfg.Notify.DelayMinutes != 15 {
		t.Errorf("want 15, got %d",
			cfg.Notify.DelayMinutes)
	}
}

func TestLoadConfig_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yamlContent := `discord:
  user_id: "from-file"
notify:
  delay_minutes: 10
`
	_ = os.WriteFile(path, []byte(yamlContent), 0600)

	t.Setenv("CLAUDE_NOTIFY_DISCORD_USER_ID", "from-env")
	t.Setenv("CLAUDE_NOTIFY_DELAY_MINUTES", "20")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Discord.UserID != "from-env" {
		t.Errorf("want from-env, got %s",
			cfg.Discord.UserID)
	}
	if cfg.Notify.DelayMinutes != 20 {
		t.Errorf("want 20, got %d",
			cfg.Notify.DelayMinutes)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	_ = os.WriteFile(path, []byte(":{bad yaml"), 0600)

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadConfig_InvalidDelayMinutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	t.Setenv("CLAUDE_NOTIFY_DELAY_MINUTES", "notanumber")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should keep default when env var is invalid
	if cfg.Notify.DelayMinutes != 15 {
		t.Errorf("want default 15, got %d",
			cfg.Notify.DelayMinutes)
	}
}

func TestConfigPaths(t *testing.T) {
	cfg := &Config{}
	cfg.applyDefaults()

	state := cfg.StateDir()
	if state == "" {
		t.Error("state dir should not be empty")
	}

	runtime := cfg.RuntimeDir()
	if runtime == "" {
		t.Error("runtime dir should not be empty")
	}
}

func TestConfigPaths_Overrides(t *testing.T) {
	cfg := &Config{
		Session: SessionConfig{
			StateDirOverride:   "/custom/state",
			RuntimeDirOverride: "/custom/runtime",
		},
	}

	if cfg.StateDir() != "/custom/state" {
		t.Errorf("want /custom/state, got %s",
			cfg.StateDir())
	}
	if cfg.RuntimeDir() != "/custom/runtime" {
		t.Errorf("want /custom/runtime, got %s",
			cfg.RuntimeDir())
	}
}

func TestConfigPaths_XDGRuntime(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	cfg := &Config{}
	expected := "/run/user/1000/claude-notify"
	if cfg.RuntimeDir() != expected {
		t.Errorf("want %s, got %s",
			expected, cfg.RuntimeDir())
	}
}

func TestConfigPaths_DefaultState(t *testing.T) {
	cfg := &Config{}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(
		home, ".local", "state", "claude-notify",
	)
	if cfg.StateDir() != expected {
		t.Errorf("want %s, got %s",
			expected, cfg.StateDir())
	}
}
