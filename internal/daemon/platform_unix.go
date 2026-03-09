//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
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
	_, err = fmt.Fprint(f, content+"\r")
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
