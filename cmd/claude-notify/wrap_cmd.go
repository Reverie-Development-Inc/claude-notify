package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Reverie-Development-Inc/claude-notify/internal/config"
	"github.com/Reverie-Development-Inc/claude-notify/internal/wrapper"
)

var wrapCmd = &cobra.Command{
	Use:                "wrap -- <claude-binary> [args...]",
	Short:              "Wrap Claude Code with FIFO stdin injection",
	RunE:               runWrap,
	DisableFlagParsing: true,
}

func init() {
	rootCmd.AddCommand(wrapCmd)
}

func runWrap(cmd *cobra.Command, args []string) error {
	// Find "--" separator.
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx == -1 || sepIdx+1 >= len(args) {
		return fmt.Errorf(
			"usage: claude-notify wrap" +
				" -- <claude-binary> [args...]")
	}

	binary := args[sepIdx+1]
	claudeArgs := args[sepIdx+2:]

	cfg, err := config.Load(configPath())
	if err != nil {
		// Graceful degradation: run claude directly.
		return runClaudeDirect(binary, claudeArgs)
	}

	return wrapper.Run(wrapper.Config{
		ClaudeBinary: binary,
		RuntimeDir:   cfg.RuntimeDir(),
		StateDir:     cfg.StateDir(),
	}, claudeArgs)
}
