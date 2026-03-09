package daemon

import (
	"os"
	"testing"
	"time"

	"github.com/Reverie-Development-Inc/claude-notify/internal/session"
)

func TestShouldNotify_ActiveSession(t *testing.T) {
	meta := &session.Metadata{Status: session.StatusActive}
	if shouldNotify(meta, 5*time.Minute) {
		t.Error("active session should not notify")
	}
}

func TestShouldNotify_WaitingNotLongEnough(t *testing.T) {
	meta := &session.Metadata{
		Status:   session.StatusWaiting,
		LastStop: time.Now().Add(-2 * time.Minute),
	}
	if shouldNotify(meta, 5*time.Minute) {
		t.Error("should not notify before delay")
	}
}

func TestShouldNotify_WaitingLongEnough(t *testing.T) {
	meta := &session.Metadata{
		Status:   session.StatusWaiting,
		LastStop: time.Now().Add(-6 * time.Minute),
	}
	if !shouldNotify(meta, 5*time.Minute) {
		t.Error("should notify after delay")
	}
}

func TestShouldNotify_AlreadyNotified(t *testing.T) {
	meta := &session.Metadata{
		Status:           session.StatusWaiting,
		LastStop:         time.Now().Add(-6 * time.Minute),
		NotificationSent: true,
	}
	if shouldNotify(meta, 5*time.Minute) {
		t.Error("already notified should not notify again")
	}
}

func TestShouldNotify_ZeroLastStop(t *testing.T) {
	meta := &session.Metadata{
		Status: session.StatusWaiting,
	}
	if shouldNotify(meta, 5*time.Minute) {
		t.Error("zero LastStop should not notify")
	}
}

func TestIsProcessAlive(t *testing.T) {
	if !isProcessAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	if isProcessAlive(0) {
		t.Error("PID 0 should not be alive")
	}
}

