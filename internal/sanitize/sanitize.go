// Package sanitize strips secrets and truncates messages
// before they are sent as Discord DM notifications.
// Security-critical: must never leak API keys, tokens,
// connection strings, or other credentials.
package sanitize

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// secretPattern pairs a compiled regex with its
// replacement string.
type secretPattern struct {
	re          *regexp.Regexp
	replacement string
}

// patterns is the ordered list of secret-matching regexes.
// Order matters: connection strings must be matched before
// generic env-var patterns to avoid partial matches.
var patterns = []secretPattern{
	{
		// Connection strings: scheme://user:pass@host
		re: regexp.MustCompile(
			`\w+://\S+:\S+@\S+`,
		),
		replacement: "[REDACTED_URI]",
	},
	{
		// AWS access key IDs (AKIA + 16 alphanumeric)
		re: regexp.MustCompile(
			`AKIA[0-9A-Z]{16}`,
		),
		replacement: "[REDACTED_KEY]",
	},
	{
		// Bearer tokens (case-insensitive)
		re: regexp.MustCompile(
			`(?i)(bearer\s+)\S+`,
		),
		replacement: "${1}[REDACTED]",
	},
	{
		// ENV_VAR=value assignments
		re: regexp.MustCompile(
			`([A-Z][A-Z_]{2,})=\S+`,
		),
		replacement: "${1}=[REDACTED]",
	},
	{
		// Long base64-like blobs (41+ chars)
		re: regexp.MustCompile(
			`[A-Za-z0-9+/=]{41,}`,
		),
		replacement: "[REDACTED]",
	},
}

// StripSecrets applies regex replacements to redact
// secrets such as env var values, bearer tokens,
// connection strings, AWS keys, and base64 blobs.
func StripSecrets(s string) string {
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.replacement)
	}
	return s
}

// Truncate shortens s to at most maxLen runes.
// If truncation occurs, "..." is appended. Operates on
// runes to avoid slicing multi-byte UTF-8 codepoints.
func Truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return "..."[:maxLen]
	}
	runes := []rune(s)
	return string(runes[:maxLen-3]) + "..."
}

// Preview produces a safe, length-limited preview of s
// suitable for sending in a Discord DM. It strips
// secrets, trims whitespace, and truncates.
func Preview(s string, maxLen int) string {
	s = StripSecrets(s)
	s = strings.TrimSpace(s)
	s = Truncate(s, maxLen)
	return s
}
