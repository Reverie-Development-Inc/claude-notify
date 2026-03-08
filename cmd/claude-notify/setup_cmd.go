package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Reverie-Development-Inc/claude-notify/internal/config"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup",
	RunE:  runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("claude-notify setup")
	fmt.Println("---")

	fmt.Print("Discord user ID: ")
	userID, _ := reader.ReadString('\n')
	userID = strings.TrimSpace(userID)

	fmt.Print(
		"SSM path for bot token" +
			" [/reverie/claude-notify/bot-token]: ")
	ssmPath, _ := reader.ReadString('\n')
	ssmPath = strings.TrimSpace(ssmPath)
	if ssmPath == "" {
		ssmPath = "/reverie/claude-notify/bot-token"
	}

	fmt.Print("Notification delay in minutes [5]: ")
	delayStr, _ := reader.ReadString('\n')
	delayStr = strings.TrimSpace(delayStr)
	delay := 5
	if delayStr != "" {
		fmt.Sscanf(delayStr, "%d", &delay)
	}

	cfg := &config.Config{
		Discord: config.DiscordConfig{
			UserID:      userID,
			BotTokenSSM: ssmPath,
		},
		Notify: config.NotifyConfig{
			DelayMinutes:       delay,
			MaxPreviewChars:    500,
			IncludeSuggestions: true,
		},
	}

	cfgDir := filepath.Join(
		os.Getenv("HOME"),
		".config", "claude-notify",
	)
	os.MkdirAll(cfgDir, 0700)
	cfgPath := filepath.Join(cfgDir, "config.yaml")

	data, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(
		cfgPath, data, 0600,
	); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("Config written to %s\n", cfgPath)

	claudeBinary := getClaudeBinaryPath()

	fmt.Println("\nAdd to your ~/.zshrc:")
	fmt.Println()
	fmt.Println("  claude() {")
	fmt.Println("    claude-notify wrap -- \\")
	fmt.Printf("      %s \"$@\"\n", claudeBinary)
	fmt.Println("  }")
	fmt.Println()
	fmt.Println("Install systemd service:")
	fmt.Println("  cp install/claude-notify.service \\")
	fmt.Println("    ~/.config/systemd/user/")
	fmt.Println(
		"  systemctl --user enable --now claude-notify")

	return nil
}

func getClaudeBinaryPath() string {
	paths := []string{
		os.Getenv("HOME") + "/.local/bin/claude",
		"/usr/local/bin/claude",
		"/usr/bin/claude",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "claude"
}
