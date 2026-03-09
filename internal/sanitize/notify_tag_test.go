package sanitize

import "testing"

func TestParseNotifyTag(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		summary string
		skip    bool
		cleaned string
	}{
		{
			name: "summary tag",
			input: "Done fixing.\n" +
				"[notify: Auth fix complete, run tests?]",
			summary: "Auth fix complete, run tests?",
			skip:    false,
			cleaned: "Done fixing.",
		},
		{
			name:    "none tag",
			input:   "Working on it.\n[notify: none]",
			summary: "",
			skip:    true,
			cleaned: "Working on it.",
		},
		{
			name:    "no tag",
			input:   "Just a regular message.",
			summary: "",
			skip:    false,
			cleaned: "Just a regular message.",
		},
		{
			name: "tag with extra whitespace",
			input: "Result:\n" +
				"  [notify: Deploy ready]  \n",
			summary: "Deploy ready",
			skip:    false,
			cleaned: "Result:",
		},
		{
			name: "tag mid-text ignored",
			input: "See [notify: test] above\n" +
				"More text\n" +
				"[notify: Real summary]",
			summary: "Real summary",
			skip:    false,
			cleaned: "See [notify: test] above\nMore text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, skip, cleaned := ParseNotifyTag(
				tt.input,
			)
			if summary != tt.summary {
				t.Errorf(
					"summary = %q, want %q",
					summary, tt.summary,
				)
			}
			if skip != tt.skip {
				t.Errorf(
					"skip = %v, want %v",
					skip, tt.skip,
				)
			}
			if cleaned != tt.cleaned {
				t.Errorf(
					"cleaned = %q, want %q",
					cleaned, tt.cleaned,
				)
			}
		})
	}
}
