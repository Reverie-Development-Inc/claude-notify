package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscordRuntimeConfig_IsUserAllowed(
	t *testing.T,
) {
	drc := &DiscordRuntimeConfig{
		AllowedUsers: []string{"user2", "user3"},
	}
	if !drc.IsUserAllowed("owner", "owner") {
		t.Error("owner should always be allowed")
	}
	if !drc.IsUserAllowed("user2", "owner") {
		t.Error("allowed user should be allowed")
	}
	if drc.IsUserAllowed("stranger", "owner") {
		t.Error("unknown user should not be allowed")
	}
}

func TestDiscordRuntimeConfig_AddRemoveUser(
	t *testing.T,
) {
	drc := &DiscordRuntimeConfig{}
	if !drc.AddUser("u1") {
		t.Error("first add should return true")
	}
	if drc.AddUser("u1") {
		t.Error("duplicate add should return false")
	}
	if len(drc.AllowedUsers) != 1 {
		t.Errorf("want 1 user, got %d",
			len(drc.AllowedUsers))
	}
	if !drc.RemoveUser("u1") {
		t.Error("remove existing should return true")
	}
	if drc.RemoveUser("u1") {
		t.Error("remove missing should return false")
	}
}

func TestDiscordRuntimeConfig_SaveLoad(
	t *testing.T,
) {
	dir := t.TempDir()
	drc := &DiscordRuntimeConfig{
		AllowedUsers:        []string{"a", "b"},
		NotificationChannel: "ch123",
	}
	if err := SaveDiscordRuntimeConfig(
		dir, drc,
	); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file exists with 0600.
	path := filepath.Join(dir, discordConfigFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("want 0600, got %o",
			info.Mode().Perm())
	}

	loaded := LoadDiscordRuntimeConfig(dir)
	if len(loaded.AllowedUsers) != 2 {
		t.Errorf("want 2 users, got %d",
			len(loaded.AllowedUsers))
	}
	if loaded.NotificationChannel != "ch123" {
		t.Errorf("want ch123, got %s",
			loaded.NotificationChannel)
	}
}

func TestDiscordRuntimeConfig_MissingFile(
	t *testing.T,
) {
	dir := t.TempDir()
	drc := LoadDiscordRuntimeConfig(dir)
	if len(drc.AllowedUsers) != 0 {
		t.Error("empty config should have no users")
	}
	if drc.NotificationChannel != "" {
		t.Error("empty config should have no channel")
	}
}
