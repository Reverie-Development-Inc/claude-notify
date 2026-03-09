package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "claude-notify",
	Short: "Discord notifications for idle Claude Code sessions",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(
		home, ".config", "claude-notify", "config.yaml",
	)
}

func loadBotToken(
	ssmPath string, region string,
) (string, error) {
	// Check env var first — allows non-AWS users to
	// export their token directly.
	if token := os.Getenv(
		"CLAUDE_NOTIFY_BOT_TOKEN",
	); token != "" {
		log.Print(
			"bot token loaded from " +
				"CLAUDE_NOTIFY_BOT_TOKEN env var")
		return token, nil
	}

	log.Printf(
		"loading bot token from SSM: %s "+
			"(region: %s)", ssmPath, region)

	cfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return "", fmt.Errorf("load AWS config: %w", err)
	}
	client := ssm.NewFromConfig(cfg)
	out, err := client.GetParameter(
		context.Background(),
		&ssm.GetParameterInput{
			Name:           aws.String(ssmPath),
			WithDecryption: aws.Bool(true),
		},
	)
	if err != nil {
		return "", fmt.Errorf("get SSM param: %w", err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf(
			"SSM param %s has no value", ssmPath)
	}
	return *out.Parameter.Value, nil
}
