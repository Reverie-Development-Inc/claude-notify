//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// writeToFIFO opens the named pipe for writing and
// sends content followed by a carriage return (\r).
// The PTY is in raw mode, so \r simulates pressing
// Enter, while \n would just move the cursor down.
// Uses O_NONBLOCK to avoid blocking the daemon if no
// reader is present.
func writeToFIFO(fifoPath, content string) error {
	f, err := os.OpenFile(
		fifoPath,
		os.O_WRONLY|syscall.O_NONBLOCK,
		0600,
	)
	if err != nil {
		return fmt.Errorf("open fifo: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Write text content first.
	if _, err := fmt.Fprint(f, content); err != nil {
		return err
	}

	// Let the TUI process the pasted text before
	// sending the submit keystroke. Without this
	// delay, content + \r arrive in the same PTY
	// read buffer and Claude Code's Ink framework
	// can swallow the carriage return during the
	// re-render triggered by the text.
	time.Sleep(50 * time.Millisecond)

	// Submit with carriage return (Enter key in
	// PTY raw mode).
	_, err = fmt.Fprint(f, "\r")
	return err
}

// isProcessAlive checks whether a process with the
// given PID exists by sending signal 0.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
