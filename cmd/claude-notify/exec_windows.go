//go:build windows

package main

import (
	"os"
	"os/exec"
)

func runClaudeDirect(
	binary string, args []string,
) error {
	cmd := exec.Command(binary, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
