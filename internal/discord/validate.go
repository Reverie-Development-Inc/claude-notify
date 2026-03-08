package discord

import (
	"fmt"
	"sync"
	"time"
)

// Validator checks that a reply comes from the expected
// Discord user and was sent after the notification.
type Validator struct {
	expectedUserID   string
	mu               sync.Mutex
	notificationTime time.Time
}

// NewValidator creates a validator that only accepts
// messages from the given Discord user ID.
func NewValidator(expectedUserID string) *Validator {
	return &Validator{expectedUserID: expectedUserID}
}

// SetNotificationTime records when a notification was
// sent, so replies before that time can be rejected.
func (v *Validator) SetNotificationTime(t time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.notificationTime = t
}

// Validate checks that senderID matches the expected user
// and that msgTime is not before the notification time.
// If no notification time has been set, any time is accepted.
func (v *Validator) Validate(
	senderID string,
	msgTime time.Time,
) error {
	if senderID != v.expectedUserID {
		return fmt.Errorf(
			"sender %s does not match expected %s",
			senderID, v.expectedUserID,
		)
	}

	v.mu.Lock()
	notifTime := v.notificationTime
	v.mu.Unlock()

	if !notifTime.IsZero() && msgTime.Before(notifTime) {
		return fmt.Errorf(
			"message time %v before notification %v",
			msgTime, notifTime,
		)
	}
	return nil
}
