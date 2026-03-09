package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Reverie-Development-Inc/claude-notify/internal/config"
	"github.com/Reverie-Development-Inc/claude-notify/internal/discord"
	"github.com/Reverie-Development-Inc/claude-notify/internal/session"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check daemon, config, and Discord connectivity",
	RunE:  runHealth,
}

func init() {
	rootCmd.AddCommand(healthCmd)
}

func runHealth(cmd *cobra.Command, args []string) error {
	ok := true

	// 1. Config
	cfgPath := configPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Printf("config     FAIL  %s\n", err)
		return nil
	}
	if cfg.Discord.UserID == "" {
		fmt.Printf("config     FAIL  " +
			"discord.user_id is empty\n")
		ok = false
	} else {
		fmt.Printf("config     OK    %s\n", cfgPath)
	}

	// 2. Daemon process
	daemonRunning := isDaemonRunning()
	if daemonRunning {
		fmt.Printf("daemon     OK    running\n")
	} else {
		fmt.Printf("daemon     WARN  " +
			"not detected (systemd/launchd)\n")
		ok = false
	}

	// 3. Bot token
	token, err := loadBotToken(cfg.Discord.BotTokenSSM)
	if err != nil {
		fmt.Printf("token      FAIL  %s\n", err)
		ok = false
	} else {
		masked := token[:4] + "..." +
			token[len(token)-4:]
		fmt.Printf("token      OK    %s\n", masked)
	}

	// 4. Discord connectivity
	if token != "" {
		dc, err := discord.NewClient(
			token, cfg.Discord.UserID,
		)
		if err != nil {
			fmt.Printf("discord    FAIL  %s\n", err)
			ok = false
		} else {
			dc.Close()
			fmt.Printf("discord    OK    " +
				"connected as bot\n")
		}
	}

	// 5. State directory
	stateDir := cfg.StateDir()
	if _, err := os.Stat(stateDir); err == nil {
		sessions, _ := session.List(stateDir)
		fmt.Printf(
			"sessions   OK    %d active (%s)\n",
			len(sessions), stateDir,
		)
	} else {
		fmt.Printf(
			"sessions   OK    no state dir yet\n",
		)
	}

	// 6. Hooks
	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(
		home, ".claude", "settings.json",
	)
	if data, err := os.ReadFile(settingsPath); err == nil {
		settings := string(data)
		if strings.Contains(
			settings, "claude-notify",
		) {
			fmt.Printf("hooks      OK    " +
				"found in settings.json\n")
		} else {
			fmt.Printf("hooks      WARN  " +
				"not found in settings.json\n")
			ok = false
		}
	} else {
		fmt.Printf("hooks      WARN  " +
			"settings.json not found\n")
		ok = false
	}

	if ok {
		fmt.Println("\nAll checks passed.")
	} else {
		fmt.Println("\nSome checks need attention.")
	}
	return nil
}

func isDaemonRunning() bool {
	// Try systemctl (Linux)
	out, err := exec.Command(
		"systemctl", "--user", "is-active",
		"claude-notify",
	).Output()
	if err == nil {
		return strings.TrimSpace(
			string(out)) == "active"
	}

	// Try launchctl (macOS)
	out, err = exec.Command(
		"launchctl", "list",
		"com.claude-notify.daemon",
	).Output()
	if err == nil && len(out) > 0 {
		return true
	}

	return false
}
