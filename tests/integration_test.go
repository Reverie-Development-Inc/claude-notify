package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Reverie-Development-Inc/claude-notify/internal/sanitize"
	"github.com/Reverie-Development-Inc/claude-notify/internal/session"
)

func TestMetadataLifecycle(t *testing.T) {
	dir := t.TempDir()

	// Simulate wrapper creating session
	meta := &session.Metadata{
		PID:     os.Getpid(),
		FIFO:    filepath.Join(dir, "test.fifo"),
		CWD:     "/tmp/test-project",
		Started: time.Now(),
		Status:  session.StatusActive,
		ShortID: "ab12",
	}
	path := filepath.Join(dir,
		fmt.Sprintf("%d.json", meta.PID))
	if err := session.Write(path, meta); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Simulate Stop hook updating status to waiting
	err := session.UpdateStatus(path,
		session.StatusWaiting,
		"I finished the implementation. Should I continue?",
		"", false)
	if err != nil {
		t.Fatalf("update to waiting: %v", err)
	}

	// Verify state after Stop hook
	got, err := session.Read(path)
	if err != nil {
		t.Fatalf("read after stop: %v", err)
	}
	if got.Status != session.StatusWaiting {
		t.Errorf("want waiting, got %s", got.Status)
	}
	if got.LastMessagePreview == "" {
		t.Error("preview should be set")
	}
	if got.LastStop.IsZero() {
		t.Error("last_stop should be set")
	}
	if got.NotificationSent {
		t.Error("notification should not be sent yet")
	}

	// Simulate daemon marking notification as sent
	got.NotificationSent = true
	got.NotificationMsgID = "discord_msg_123"
	if err := session.Write(path, got); err != nil {
		t.Fatalf("write notification state: %v", err)
	}

	// Verify notification state
	got2, err := session.Read(path)
	if err != nil {
		t.Fatalf("read after notification: %v", err)
	}
	if !got2.NotificationSent {
		t.Error("notification should be marked sent")
	}
	if got2.NotificationMsgID != "discord_msg_123" {
		t.Error("notification msg ID mismatch")
	}

	// Simulate UserPromptSubmit resetting to active
	if err := session.UpdateStatus(
		path, session.StatusActive, "",
		"", false,
	); err != nil {
		t.Fatalf("update to active: %v", err)
	}
	got3, err := session.Read(path)
	if err != nil {
		t.Fatalf("read after active: %v", err)
	}
	if got3.Status != session.StatusActive {
		t.Errorf("want active after prompt, got %s",
			got3.Status)
	}
	if got3.NotificationSent {
		t.Error("notification should be reset on active")
	}

	// Verify PID is preserved through all updates
	if got3.PID != os.Getpid() {
		t.Errorf("PID changed: want %d, got %d",
			os.Getpid(), got3.PID)
	}
	if got3.ShortID != "ab12" {
		t.Errorf("short ID changed: want ab12, got %s",
			got3.ShortID)
	}
}

func TestSessionListAndFilter(t *testing.T) {
	dir := t.TempDir()

	// Create multiple sessions in different states
	for i, status := range []session.Status{
		session.StatusActive,
		session.StatusWaiting,
		session.StatusWaiting,
		session.StatusActive,
	} {
		pid := 1000 + i
		path := filepath.Join(dir,
			fmt.Sprintf("%d.json", pid))
		meta := &session.Metadata{
			PID:     pid,
			Status:  status,
			CWD:     fmt.Sprintf("/project/%d", i),
			ShortID: fmt.Sprintf("%04x", pid),
		}
		if status == session.StatusWaiting {
			meta.LastStop = time.Now().Add(
				-10 * time.Minute)
			meta.LastMessagePreview = "waiting for input"
		}
		if err := session.Write(path, meta); err != nil {
			t.Fatalf("write session %d: %v", pid, err)
		}
	}

	all, err := session.List(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 sessions, got %d", len(all))
	}

	waiting := session.ListByStatus(
		all, session.StatusWaiting)
	if len(waiting) != 2 {
		t.Errorf("want 2 waiting, got %d", len(waiting))
	}

	active := session.ListByStatus(
		all, session.StatusActive)
	if len(active) != 2 {
		t.Errorf("want 2 active, got %d", len(active))
	}
}

func TestNotifyTagIntegration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	// Simulate: session in remote mode, Claude sends
	// a message with [notify: ...] tag
	m := &session.Metadata{
		PID:        1234,
		Status:     session.StatusActive,
		RemoteMode: true,
	}
	_ = session.Write(path, m)

	// Simulate hook input with notify tag
	raw := "Fixed the auth bug and updated tests.\n" +
		"[notify: Auth fix done, want me to deploy?]"
	summary, skip, cleaned := sanitize.ParseNotifyTag(
		raw,
	)

	preview := sanitize.Preview(cleaned, 500)

	err := session.UpdateStatus(
		path,
		session.StatusWaiting,
		preview,
		summary,
		skip,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := session.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	if got.NotifySummary !=
		"Auth fix done, want me to deploy?" {
		t.Errorf(
			"NotifySummary = %q",
			got.NotifySummary,
		)
	}
	if got.SkipNotification {
		t.Error("SkipNotification should be false")
	}
	if !got.RemoteMode {
		t.Error("RemoteMode should persist")
	}
}

func TestNotifyTagNone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	m := &session.Metadata{
		PID:        1234,
		Status:     session.StatusActive,
		RemoteMode: true,
	}
	_ = session.Write(path, m)

	raw := "Still working on it...\n[notify: none]"
	summary, skip, cleaned := sanitize.ParseNotifyTag(
		raw,
	)
	preview := sanitize.Preview(cleaned, 500)

	err := session.UpdateStatus(
		path,
		session.StatusWaiting,
		preview,
		summary,
		skip,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := session.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	if !got.SkipNotification {
		t.Error("SkipNotification should be true")
	}
	if got.NotifySummary != "" {
		t.Errorf(
			"NotifySummary should be empty, got %q",
			got.NotifySummary,
		)
	}
}

func TestRemoteModePreservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	m := &session.Metadata{
		PID:            1234,
		Status:         session.StatusWaiting,
		RemoteMode:     true,
		LastInjectedAt: time.Now().Unix(),
	}
	_ = session.Write(path, m)

	// Simulate: FIFO injection causes status -> active
	// Remote mode should persist (FIFO echo)
	err := session.UpdateStatus(
		path,
		session.StatusActive,
		"",
		"",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := session.Read(path)
	// RemoteMode is NOT cleared by UpdateStatus --
	// only the daemon clears it when detecting real
	// terminal input
	if !got.RemoteMode {
		t.Error(
			"RemoteMode should persist through " +
				"FIFO echo",
		)
	}
}
