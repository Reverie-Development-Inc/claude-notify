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
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	Reason         string `json:"reason"`
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
	sessionUpdateCmd.MarkFlagRequired("status")
	rootCmd.AddCommand(sessionUpdateCmd)
}

func runSessionUpdate(
	cmd *cobra.Command, args []string,
) error {
	// Read hook input from stdin (non-blocking — may be
	// empty when invoked manually).
	inputData, _ := io.ReadAll(os.Stdin)
	var hi hookInput
	json.Unmarshal(inputData, &hi) // ignore errors

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
			return fmt.Errorf(
				"no session metadata found")
		}
	}

	status := session.Status(updateStatus)
	var preview string

	if status == session.StatusWaiting &&
		hi.TranscriptPath != "" {
		raw := extractLastAssistantMessage(
			hi.TranscriptPath)
		preview = sanitize.Preview(raw, 500)
	}

	return session.UpdateStatus(metaPath, status, preview)
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

// findSessionByAncestorPID walks up the process tree to
// find a session metadata file matching an ancestor PID.
func findSessionByAncestorPID(stateDir string) string {
	pid := os.Getpid()
	for pid > 1 {
		path := filepath.Join(stateDir,
			fmt.Sprintf("%d.json", pid))
		if _, err := os.Stat(path); err == nil {
			return path
		}
		// Read PPID from /proc/pid/status
		statusData, err := os.ReadFile(
			fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			break
		}
		found := false
		for _, line := range strings.Split(
			string(statusData), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					var ppid int
					fmt.Sscanf(
						fields[1], "%d", &ppid)
					if ppid == pid {
						break // infinite loop guard
					}
					pid = ppid
					found = true
				}
				break
			}
		}
		if !found {
			break
		}
	}
	return ""
}
