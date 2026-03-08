package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reverie-Development-Inc/claude-notify/internal/config"
	"github.com/Reverie-Development-Inc/claude-notify/internal/session"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show active Claude Code sessions",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(configPath())
	if err != nil {
		return err
	}

	sessions, err := session.List(cfg.StateDir())
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No active sessions.")
		return nil
	}

	for _, m := range sessions {
		age := time.Since(m.Started).Round(time.Second)
		fmt.Printf("#%s  PID=%d  %s  %s  (%s)\n",
			m.ShortID, m.PID, m.Status, m.CWD, age)
		if m.LastMessagePreview != "" {
			preview := m.LastMessagePreview
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			fmt.Printf("     %s\n", preview)
		}
	}
	return nil
}
