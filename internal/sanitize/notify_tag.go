package sanitize

import (
	"regexp"
	"strings"
)

// notifyTagRe matches [notify: ...] at the end of a
// message (last non-empty line).
var notifyTagRe = regexp.MustCompile(
	`(?m)^\s*\[notify:\s*(.*?)\]\s*$`,
)

// ParseNotifyTag extracts the [notify: ...] tag from
// the end of a message. Returns:
//   - summary: the tag content (empty if none/no tag)
//   - skip: true if [notify: none]
//   - cleaned: the message with the tag line removed
//
// Only the LAST matching [notify: ...] line is used.
func ParseNotifyTag(
	msg string,
) (summary string, skip bool, cleaned string) {
	matches := notifyTagRe.FindAllStringIndex(msg, -1)
	if len(matches) == 0 {
		return "", false, msg
	}

	// Use the last match
	last := matches[len(matches)-1]

	// Extract content from the last match
	tagLine := msg[last[0]:last[1]]
	sub := notifyTagRe.FindStringSubmatch(tagLine)
	if len(sub) < 2 {
		return "", false, msg
	}
	content := strings.TrimSpace(sub[1])

	// Remove the tag line from the message
	cleaned = strings.TrimRight(
		msg[:last[0]], "\n \t",
	)

	if strings.EqualFold(content, "none") {
		return "", true, cleaned
	}
	return content, false, cleaned
}
