//go:build windows

package main

// findSessionByAncestorPID is not supported on
// Windows. Sessions must use the
// CLAUDE_NOTIFY_SESSION env var.
func findSessionByAncestorPID(
	stateDir string,
) string {
	return ""
}
