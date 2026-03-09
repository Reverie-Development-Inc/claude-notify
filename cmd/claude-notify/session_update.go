package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Reverie-Development-Inc/claude-notify/internal/sanitize"
	"github.com/Reverie-Development-Inc/claude-notify/internal/session"
)

// hookInput is the JSON structure Claude Code sends on
// stdin to hook commands.
type hookInput struct {
	SessionID           string `json:"session_id"`
	TranscriptPath      string `json:"transcript_path"`
	CWD                 string `json:"cwd"`
	Reason              string `json:"reason"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

var sessionUpdateCmd = &cobra.Command{
	Use:   "session-update",
	Short: "Update session status (called by hooks)",
	RunE:  runSessionUpdate,
}

var updateStatus string

func init() {
	sessionUpdateCmd.Flags().StringVar(
		&updateStatus, "status", "",
		"new status: active or waiting")
	_ = sessionUpdateCmd.MarkFlagRequired("status")
	rootCmd.AddCommand(sessionUpdateCmd)
}

func runSessionUpdate(
	cmd *cobra.Command, args []string,
) error {
	// Read hook input from stdin (non-blocking — may be
	// empty when invoked manually).
	inputData, _ := io.ReadAll(os.Stdin)
	var hi hookInput
	_ = json.Unmarshal(inputData, &hi) // best-effort; empty input is valid

	// Find session metadata via env var set by wrapper.
	metaPath := os.Getenv("CLAUDE_NOTIFY_SESSION")
	if metaPath == "" {
		// Fallback: walk ancestor PIDs to find a
		// session metadata file.
		home, _ := os.UserHomeDir()
		stateDir := filepath.Join(home,
			".local", "state", "claude-notify")
		metaPath = findSessionByAncestorPID(stateDir)
		if metaPath == "" {
			// Not a wrapped session — exit silently.
			return nil
		}
	}

	status := session.Status(updateStatus)
	if status != session.StatusActive &&
		status != session.StatusWaiting {
		return fmt.Errorf(
			"invalid status %q: must be active or waiting",
			updateStatus)
	}
	var preview string

	if status == session.StatusWaiting {
		if hi.LastAssistantMessage != "" {
			preview = sanitize.Preview(
				hi.LastAssistantMessage, 500)
		} else if hi.TranscriptPath != "" {
			raw := extractLastAssistantMessage(
				hi.TranscriptPath)
			preview = sanitize.Preview(raw, 500)
		}
	}

	// Parse notify tag from preview
	summary, skip, cleaned := sanitize.ParseNotifyTag(
		preview,
	)
	if cleaned != "" {
		preview = sanitize.Preview(cleaned, 500)
	}

	return session.UpdateStatus(
		metaPath, status, preview, summary, skip,
	)
}

// extractLastAssistantMessage reads the transcript file
// and finds the last assistant message. The transcript
// format is JSONL with role/content fields.
func extractLastAssistantMessage(
	transcriptPath string,
) string {
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return ""
	}

	lines := strings.Split(
		strings.TrimSpace(string(data)), "\n")

	for i := len(lines) - 1; i >= 0; i-- {
		var turn struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Type    string `json:"type"`
		}
		if err := json.Unmarshal(
			[]byte(lines[i]), &turn,
		); err != nil {
			continue
		}
		if turn.Role == "assistant" &&
			turn.Content != "" {
			return turn.Content
		}
	}
	return ""
}

