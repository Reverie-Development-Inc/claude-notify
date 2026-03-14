package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// DiscordRuntimeConfig holds runtime settings managed
// via the /configure slash command. Stored as JSON in
// the state directory, separate from the YAML config.
type DiscordRuntimeConfig struct {
	// AllowedUsers lists Discord user IDs permitted to
	// react and reply to notifications (in addition to
	// the owner, who is always implicitly allowed).
	AllowedUsers []string `json:"allowed_users"`
	// NotificationChannel is a Discord channel ID. If
	// set, notifications post here instead of DM.
	NotificationChannel string `json:"notification_channel"`
	// ForumChannelID is a Discord forum channel ID. If
	// set, notifications create/update forum threads
	// instead of DM or channel messages.
	ForumChannelID string `json:"forum_channel_id,omitempty"`
}

const discordConfigFile = "discord-config.json"

var (
	drcMu      sync.Mutex
	drcInstance *DiscordRuntimeConfig
)

// LoadDiscordRuntimeConfig reads discord-config.json
// from the state directory. Returns empty config if
// file doesn't exist. Thread-safe.
func LoadDiscordRuntimeConfig(
	stateDir string,
) *DiscordRuntimeConfig {
	drcMu.Lock()
	defer drcMu.Unlock()

	drc := &DiscordRuntimeConfig{}

	path := filepath.Join(stateDir, discordConfigFile)
	data, err := os.ReadFile(path) // #nosec G304 -- path from state dir
	if err != nil {
		drcInstance = drc
		return drc
	}
	_ = json.Unmarshal(data, drc)
	drcInstance = drc
	return drc
}

// SaveDiscordRuntimeConfig writes the config to disk.
// Thread-safe.
func SaveDiscordRuntimeConfig(
	stateDir string, drc *DiscordRuntimeConfig,
) error {
	drcMu.Lock()
	defer drcMu.Unlock()

	data, err := json.MarshalIndent(drc, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, discordConfigFile)
	drcInstance = drc
	return os.WriteFile(path, data, 0600)
}

// GetDiscordRuntimeConfig returns the cached instance.
// Returns empty config if not yet loaded.
func GetDiscordRuntimeConfig() *DiscordRuntimeConfig {
	drcMu.Lock()
	defer drcMu.Unlock()
	if drcInstance == nil {
		return &DiscordRuntimeConfig{}
	}
	return drcInstance
}

// SetForum sets the forum channel ID and clears the
// notification channel (mutual exclusion).
func (drc *DiscordRuntimeConfig) SetForum(
	channelID string,
) {
	drc.ForumChannelID = channelID
	drc.NotificationChannel = ""
}

// SetChannel sets the notification channel and clears
// the forum channel ID (mutual exclusion).
func (drc *DiscordRuntimeConfig) SetChannel(
	channelID string,
) {
	drc.NotificationChannel = channelID
	drc.ForumChannelID = ""
}

// IsUserAllowed checks if a Discord user ID is the
// owner or in the allowed users list.
func (drc *DiscordRuntimeConfig) IsUserAllowed(
	userID, ownerID string,
) bool {
	if userID == ownerID {
		return true
	}
	for _, id := range drc.AllowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

// AddUser adds a user ID to the allowed list if not
// already present. Returns true if added.
func (drc *DiscordRuntimeConfig) AddUser(
	userID string,
) bool {
	for _, id := range drc.AllowedUsers {
		if id == userID {
			return false
		}
	}
	drc.AllowedUsers = append(
		drc.AllowedUsers, userID)
	return true
}

// RemoveUser removes a user ID from the allowed list.
// Returns true if removed.
func (drc *DiscordRuntimeConfig) RemoveUser(
	userID string,
) bool {
	for i, id := range drc.AllowedUsers {
		if id == userID {
			drc.AllowedUsers = append(
				drc.AllowedUsers[:i],
				drc.AllowedUsers[i+1:]...,
			)
			return true
		}
	}
	return false
}
