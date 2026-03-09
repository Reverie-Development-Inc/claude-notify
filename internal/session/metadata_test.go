package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	meta := &Metadata{
		PID:       12345,
		FIFO:      "/run/user/1000/claude-notify/12345.fifo",
		CWD:       "/home/user/project",
		Started:   time.Now(),
		Status:    StatusActive,
		SessionID: "sess_abc123",
	}
	path := filepath.Join(dir, "12345.json")
	if err := Write(path, meta); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.PID != 12345 {
		t.Errorf("pid mismatch")
	}
	if got.Status != StatusActive {
		t.Errorf("status mismatch")
	}
	if got.SessionID != "sess_abc123" {
		t.Errorf("session_id mismatch")
	}
}

func TestUpdateStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "12345.json")
	_ = Write(path, &Metadata{PID: 12345, Status: StatusActive})

	err := UpdateStatus(
		path, StatusWaiting, "Should I continue?",
	)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := Read(path)
	if got.Status != StatusWaiting {
		t.Error("want waiting")
	}
	if got.LastMessagePreview != "Should I continue?" {
		t.Error("preview mismatch")
	}
	if got.LastStop.IsZero() {
		t.Error("last_stop should be set")
	}
	// NotificationSent should be reset to false
	if got.NotificationSent {
		t.Error("notification should be reset")
	}
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()
	_ = Write(filepath.Join(dir, "100.json"),
		&Metadata{PID: 100, Status: StatusActive})
	_ = Write(filepath.Join(dir, "200.json"),
		&Metadata{PID: 200, Status: StatusWaiting})
	_ = Write(filepath.Join(dir, "300.json"),
		&Metadata{PID: 300, Status: StatusWaiting})

	sessions, err := List(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("want 3, got %d", len(sessions))
	}

	waiting := ListByStatus(sessions, StatusWaiting)
	if len(waiting) != 2 {
		t.Errorf("want 2 waiting, got %d", len(waiting))
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "12345.json")
	Write(path, &Metadata{PID: 12345})

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("want 0600, got %o", info.Mode().Perm())
	}
}
