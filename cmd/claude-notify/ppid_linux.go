//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// findSessionByAncestorPID walks up the process tree
// to find a session metadata file matching an ancestor
// PID. On Linux, reads /proc/<pid>/status for PPid.
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
		statusData, err := os.ReadFile(
			fmt.Sprintf(
				"/proc/%d/status", pid))
		if err != nil {
			break
		}
		found := false
		for _, line := range strings.Split(
			string(statusData), "\n") {
			if strings.HasPrefix(
				line, "PPid:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					var ppid int
					fmt.Sscanf(
						fields[1],
						"%d", &ppid)
					if ppid == pid {
						break
					}
					pid = ppid
					found = true
				}
				break
			}
		}
		if !found {
			break
		}
	}
	return ""
}
