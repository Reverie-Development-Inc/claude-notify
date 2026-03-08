//go:build windows

package wrapper

import "fmt"

// Run is not supported on Windows. The PTY relay
// and FIFO require Unix. Use WSL2 for full
// functionality, or run without the wrapper
// (notifications only, no reply injection).
func Run(cfg Config, args []string) error {
	return fmt.Errorf(
		"PTY wrapper is not supported on " +
			"Windows. Use WSL2 for full " +
			"functionality, or run Claude " +
			"directly (notifications still " +
			"work via hooks)")
}
