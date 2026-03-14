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

func TestShouldNotify_SkipNotification(t *testing.T) {
	meta := &session.Metadata{
		Status:           session.StatusWaiting,
		LastStop:         time.Now().Add(-20 * time.Minute),
		SkipNotification: true,
	}
	if shouldNotify(meta, 15*time.Minute) {
		t.Error(
			"should not notify when " +
				"SkipNotification is true",
		)
	}
}

func TestShouldNotify_RemoteMode(t *testing.T) {
	// Remote mode with enough elapsed time —
	// should notify even though delay is 15min.
	meta := &session.Metadata{
		Status:     session.StatusWaiting,
		LastStop:   time.Now().Add(-20 * time.Second),
		RemoteMode: true,
	}
	if !shouldNotify(meta, 15*time.Minute) {
		t.Error(
			"should notify in remote mode " +
				"after debounce",
		)
	}

	// Remote mode but too soon — should not notify.
	meta.LastStop = time.Now().Add(-5 * time.Second)
	if shouldNotify(meta, 15*time.Minute) {
		t.Error(
			"should not notify in remote mode " +
				"before debounce",
		)
	}
}

func TestShouldNotify_RemoteModeExpired(t *testing.T) {
	meta := &session.Metadata{
		Status:     session.StatusWaiting,
		LastStop:   time.Now().Add(-20 * time.Second),
		RemoteMode: false,
	}
	if shouldNotify(meta, 15*time.Minute) {
		t.Error(
			"should not notify at 20s with " +
				"15min delay and RemoteMode off",
		)
	}
}

func TestNotificationMode(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		forum   string
		want    NotificationMode
	}{
		{"default DM", "", "", ModeDM},
		{"channel set", "ch1", "", ModeChannel},
		{"forum set", "", "f1", ModeForum},
		{
			"forum takes priority",
			"ch1", "f1", ModeForum,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveMode(
				tt.channel, tt.forum,
			)
			if got != tt.want {
				t.Errorf(
					"got %d, want %d",
					got, tt.want,
				)
			}
		})
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

func TestAllocateNumber(t *testing.T) {
	d := &Daemon{
		sessionNumbers: make(map[string]int),
		nextNumber:     1,
	}

	n1 := d.allocateNumber("abc")
	n2 := d.allocateNumber("def")
	n3 := d.allocateNumber("ghi")
	if n1 != 1 || n2 != 2 || n3 != 3 {
		t.Errorf("want 1,2,3 got %d,%d,%d",
			n1, n2, n3)
	}

	if d.allocateNumber("abc") != 1 {
		t.Error("same shortID should return 1")
	}
}

func TestReleaseAndRecycle(t *testing.T) {
	d := &Daemon{
		sessionNumbers: make(map[string]int),
		nextNumber:     1,
	}

	d.allocateNumber("a") // 1
	d.allocateNumber("b") // 2
	d.allocateNumber("c") // 3

	d.releaseNumber("b") // frees 2

	n := d.allocateNumber("d")
	if n != 2 {
		t.Errorf("want recycled 2, got %d", n)
	}

	d.releaseNumber("a") // frees 1
	d.releaseNumber("c") // frees 3

	n1 := d.allocateNumber("e")
	n2 := d.allocateNumber("f")
	if n1 != 1 || n2 != 3 {
		t.Errorf("want 1,3 got %d,%d", n1, n2)
	}
}

func TestReleaseUnknown(t *testing.T) {
	d := &Daemon{
		sessionNumbers: make(map[string]int),
		nextNumber:     1,
	}
	d.releaseNumber("nonexistent")
}

func TestSessionNumber(t *testing.T) {
	d := &Daemon{
		sessionNumbers: make(map[string]int),
		nextNumber:     1,
	}
	d.allocateNumber("abc")
	if d.sessionNumber("abc") != 1 {
		t.Error("want 1 for allocated shortID")
	}
	if d.sessionNumber("unknown") != 0 {
		t.Error("want 0 for unknown shortID")
	}
}

