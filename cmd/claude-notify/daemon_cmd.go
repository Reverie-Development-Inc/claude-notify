package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Reverie-Development-Inc/claude-notify/internal/config"
	"github.com/Reverie-Development-Inc/claude-notify/internal/daemon"
	"github.com/Reverie-Development-Inc/claude-notify/internal/discord"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the notification daemon",
	RunE:  runDaemon,
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(configPath())
	if err != nil {
		return err
	}

	token, err := loadBotToken(cfg.Discord.BotTokenSSM)
	if err != nil {
		return err
	}

	dc, err := discord.NewClient(token, cfg.Discord.UserID)
	if err != nil {
		return err
	}
	defer dc.Close()

	d := daemon.New(cfg, dc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Print("received shutdown signal")
		cancel()
	}()

	return d.Run(ctx)
}
