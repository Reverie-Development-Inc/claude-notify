package discord

import (
	"testing"
	"time"
)

func TestValidateReply_CorrectSender(t *testing.T) {
	v := NewValidator("123456")
	err := v.Validate("123456", time.Now())
	if err != nil {
		t.Errorf("valid reply rejected: %v", err)
	}
}

func TestValidateReply_WrongSender(t *testing.T) {
	v := NewValidator("123456")
	err := v.Validate("999999", time.Now())
	if err == nil {
		t.Error("wrong sender should be rejected")
	}
}

func TestValidateReply_OldMessage(t *testing.T) {
	v := NewValidator("123456")
	v.SetNotificationTime(time.Now())

	old := time.Now().Add(-10 * time.Minute)
	err := v.Validate("123456", old)
	if err == nil {
		t.Error("old message should be rejected")
	}
}

func TestValidateReply_AfterNotification(t *testing.T) {
	v := NewValidator("123456")
	notifTime := time.Now().Add(-1 * time.Minute)
	v.SetNotificationTime(notifTime)

	err := v.Validate("123456", time.Now())
	if err != nil {
		t.Errorf(
			"valid post-notif reply rejected: %v",
			err,
		)
	}
}

func TestValidateReply_NoNotificationTimeSet(t *testing.T) {
	v := NewValidator("123456")
	// No notification time set — should accept any time
	err := v.Validate(
		"123456",
		time.Now().Add(-1*time.Hour),
	)
	if err != nil {
		t.Errorf(
			"should accept when no notification time: %v",
			err,
		)
	}
}
