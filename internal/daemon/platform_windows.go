//go:build windows

package daemon

import "fmt"

// writeToFIFO is a no-op on Windows. Reply injection
// requires Unix FIFOs. The daemon still sends
// notifications but cannot inject replies.
func writeToFIFO(
	fifoPath, content string,
) error {
	return fmt.Errorf(
		"FIFO not supported on Windows; " +
			"reply injection unavailable")
}

// isProcessAlive on Windows returns true as a
// best-effort. Stale sessions are cleaned when
// metadata files age out.
func isProcessAlive(pid int) bool {
	return pid > 0
}
