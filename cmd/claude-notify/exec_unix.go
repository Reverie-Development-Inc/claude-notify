//go:build !windows

package main

import (
	"os"
	"syscall"
)

func runClaudeDirect(
	binary string, args []string,
) error {
	return syscall.Exec(binary, // #nosec G204 -- binary path from user config
		append([]string{binary}, args...),
		os.Environ())
}
