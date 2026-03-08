//go:build !windows

package main

import (
	"os"
	"syscall"
)

func runClaudeDirect(
	binary string, args []string,
) error {
	return syscall.Exec(binary,
		append([]string{binary}, args...),
		os.Environ())
}
