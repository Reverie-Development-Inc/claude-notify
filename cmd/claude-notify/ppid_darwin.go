//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// findSessionByAncestorPID walks up the process tree
// to find a session metadata file matching an ancestor
// PID. On macOS, uses ps(1) to read PPID.
func findSessionByAncestorPID(
	stateDir string,
) string {
	pid := os.Getpid()
	for pid > 1 {
		path := filepath.Join(stateDir,
			fmt.Sprintf("%d.json", pid))
		if _, err := os.Stat(path); err == nil {
			return path
		}
		out, err := exec.Command(
			"ps", "-o", "ppid=", "-p",
			fmt.Sprintf("%d", pid),
		).Output()
		if err != nil {
			break
		}
		ppidStr := strings.TrimSpace(
			string(bytes.TrimSpace(out)))
		var ppid int
		if _, err := fmt.Sscanf(
			ppidStr, "%d", &ppid,
		); err != nil {
			break
		}
		if ppid == pid || ppid <= 0 {
			break
		}
		pid = ppid
	}
	return ""
}
