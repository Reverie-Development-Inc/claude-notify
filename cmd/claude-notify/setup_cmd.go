package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func runSetup(
	cmd *cobra.Command, args []string,
) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("claude-notify setup")
	fmt.Println("---")

	fmt.Print("Discord user ID: ")
	userID, _ := reader.ReadString('\n')
	userID = strings.TrimSpace(userID)

	fmt.Print(
		"SSM path for bot token" +
			" [/claude-notify/bot-token]: ")
	ssmPath, _ := reader.ReadString('\n')
	ssmPath = strings.TrimSpace(ssmPath)
	if ssmPath == "" {
		ssmPath = "/claude-notify/bot-token"
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

	home, _ := os.UserHomeDir()
	cfgDir := filepath.Join(home,
		".config", "claude-notify")
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

	switch runtime.GOOS {
	case "darwin":
		fmt.Println("\nAdd to your shell profile" +
			" (~/.zshrc or ~/.bashrc):")
		fmt.Println()
		fmt.Println("  claude() {")
		fmt.Println("    claude-notify wrap -- \\")
		fmt.Printf("      %s \"$@\"\n", claudeBinary)
		fmt.Println("  }")
		fmt.Println()
		fmt.Println("Install launchd service:")
		fmt.Println("  cp install/com.claude-notify." +
			"daemon.plist \\")
		fmt.Println("    ~/Library/LaunchAgents/")
		fmt.Println("  launchctl load " +
			"~/Library/LaunchAgents/" +
			"com.claude-notify.daemon.plist")
	case "windows":
		fmt.Println("\nAdd to your PowerShell profile" +
			" ($PROFILE):")
		fmt.Println()
		fmt.Println("  function claude {")
		fmt.Println("    claude-notify wrap -- " +
			claudeBinary + " @args")
		fmt.Println("  }")
		fmt.Println()
		fmt.Println("Note: On Windows, reply injection" +
			" is not supported.")
		fmt.Println("Notifications still work. For full" +
			" features, use WSL2.")
		fmt.Println()
		fmt.Println("Start the daemon manually:")
		fmt.Println("  claude-notify daemon")
		fmt.Println()
		fmt.Println("Or create a scheduled task to" +
			" run it at login.")
	default: // linux
		fmt.Println("\nAdd to your shell profile" +
			" (~/.zshrc or ~/.bashrc):")
		fmt.Println()
		fmt.Println("  claude() {")
		fmt.Println("    claude-notify wrap -- \\")
		fmt.Printf("      %s \"$@\"\n", claudeBinary)
		fmt.Println("  }")
		fmt.Println()
		fmt.Println("Install systemd service:")
		fmt.Println("  make install-service")
		fmt.Println("  systemctl --user start" +
			" claude-notify")
	}

	return nil
}

func getClaudeBinaryPath() string {
	home, _ := os.UserHomeDir()
	paths := []string{
		filepath.Join(home,
			".local", "bin", "claude"),
		"/usr/local/bin/claude",
	}
	if runtime.GOOS == "windows" {
		paths = append(paths,
			filepath.Join(home, "AppData", "Local",
				"Programs", "claude", "claude.exe"),
			"claude.exe",
		)
	} else {
		paths = append(paths, "/usr/bin/claude")
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if runtime.GOOS == "windows" {
		return "claude.exe"
	}
	return "claude"
}
