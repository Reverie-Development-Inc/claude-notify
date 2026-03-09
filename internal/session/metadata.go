// Package session manages JSON session metadata files for
// claude-notify. Each Claude Code session gets a metadata file
// containing PID, FIFO path, status, and notification state.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status represents the current state of a Claude Code session.
type Status string

const (
	// StatusActive means the session is running normally.
	StatusActive Status = "active"
	// StatusWaiting means the session is blocked waiting
	// for user input.
	StatusWaiting Status = "waiting"
)

// Metadata holds the JSON-serializable state for a single
// Claude Code session.
type Metadata struct {
	PID       int       `json:"pid"`
	FIFO      string    `json:"fifo"`
	CWD       string    `json:"cwd"`
	Started   time.Time `json:"started"`
	Status    Status    `json:"status"`
	SessionID string    `json:"session_id,omitempty"`

	LastStop           time.Time `json:"last_stop,omitempty"`
	LastMessagePreview string    `json:"last_message_preview,omitempty"`

	ShortID           string `json:"short_id,omitempty"`
	NotificationSent  bool   `json:"notification_sent"`
	NotificationMsgID string `json:"notification_msg_id,omitempty"`

	RemoteMode       bool   `json:"remote_mode"`
	SkipNotification bool   `json:"skip_notification"`
	NotifySummary    string `json:"notify_summary,omitempty"`
	LastInjectedAt   int64  `json:"last_injected_at,omitempty"`
}

// Write marshals metadata to JSON and writes it to path with
// 0600 permissions. The file is written atomically by writing
// to a temp file and renaming.
func Write(path string, m *Metadata) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Write to temp file in the same directory, then rename
	// for atomic writes.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".metadata-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// Read deserializes a metadata JSON file from disk.
func Read(path string) (*Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateStatus reads the metadata file at path, updates the
// status and related fields, then writes it back. When status
// is StatusWaiting, LastStop is set to now, the preview is
// stored, and notification state is reset.
func UpdateStatus(
	path string, status Status, preview string,
) error {
	m, err := Read(path)
	if err != nil {
		return err
	}

	m.Status = status

	switch status {
	case StatusWaiting:
		m.LastStop = time.Now()
		m.LastMessagePreview = preview
		m.NotificationSent = false
		m.NotificationMsgID = ""
	case StatusActive:
		m.NotificationSent = false
		m.NotificationMsgID = ""
	}

	return Write(path, m)
}

// List reads all .json metadata files in the given directory
// and returns their parsed contents. Non-JSON files and files
// that fail to parse are silently skipped.
func List(dir string) ([]*Metadata, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var sessions []*Metadata
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		m, err := Read(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		sessions = append(sessions, m)
	}
	return sessions, nil
}

// ListByStatus filters a slice of metadata to only those
// matching the given status.
func ListByStatus(
	sessions []*Metadata, status Status,
) []*Metadata {
	var filtered []*Metadata
	for _, s := range sessions {
		if s.Status == status {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
