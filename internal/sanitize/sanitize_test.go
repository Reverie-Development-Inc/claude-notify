package sanitize

import (
	"strings"
	"testing"
)

// --- StripSecrets tests ---

func TestStripSecrets_EnvVarPattern(t *testing.T) {
	input := "DATABASE_URL=postgres://localhost/db"
	got := StripSecrets(input)
	if !strings.Contains(got, "DATABASE_URL=[REDACTED]") {
		t.Errorf(
			"env var not redacted:\n  got: %s",
			got,
		)
	}
	if strings.Contains(got, "postgres://localhost/db") {
		t.Error("original value still present")
	}
}

func TestStripSecrets_MultipleEnvVars(t *testing.T) {
	input := "API_KEY=sk-1234 SECRET_TOKEN=abc123"
	got := StripSecrets(input)
	if !strings.Contains(got, "API_KEY=[REDACTED]") {
		t.Errorf("first env var not redacted: %s", got)
	}
	if !strings.Contains(got, "SECRET_TOKEN=[REDACTED]") {
		t.Errorf("second env var not redacted: %s", got)
	}
}

func TestStripSecrets_BearerToken(t *testing.T) {
	input := "Authorization: Bearer eyJhbGciOiJIUz.payload.sig"
	got := StripSecrets(input)
	if !strings.Contains(got, "Bearer [REDACTED]") {
		t.Errorf(
			"bearer token not redacted:\n  got: %s",
			got,
		)
	}
	if strings.Contains(got, "eyJhbGciOiJIUz") {
		t.Error("token value still present")
	}
}

func TestStripSecrets_BearerCaseInsensitive(t *testing.T) {
	input := "bearer mytoken123"
	got := StripSecrets(input)
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("lowercase bearer not redacted: %s", got)
	}
}

func TestStripSecrets_ConnectionString(t *testing.T) {
	input := "postgres://admin:s3cret@db.example.com:5432/mydb"
	got := StripSecrets(input)
	if got != "[REDACTED_URI]" {
		t.Errorf(
			"connection string not redacted:\n  got: %s",
			got,
		)
	}
}

func TestStripSecrets_ConnectionStringRedis(t *testing.T) {
	input := "redis://user:password@redis.host:6379"
	got := StripSecrets(input)
	if got != "[REDACTED_URI]" {
		t.Errorf(
			"redis URI not redacted:\n  got: %s",
			got,
		)
	}
}

func TestStripSecrets_AWSKey(t *testing.T) {
	input := "key is AKIAIOSFODNN7EXAMPLE"
	got := StripSecrets(input)
	if !strings.Contains(got, "[REDACTED_KEY]") {
		t.Errorf("AWS key not redacted:\n  got: %s", got)
	}
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("AWS key value still present")
	}
}

func TestStripSecrets_LongBase64(t *testing.T) {
	// 50 chars of base64-like content
	blob := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwx"
	input := "token: " + blob
	got := StripSecrets(input)
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf(
			"long base64 not redacted:\n  got: %s",
			got,
		)
	}
	if strings.Contains(got, blob) {
		t.Error("base64 blob still present")
	}
}

func TestStripSecrets_ShortBase64Unchanged(t *testing.T) {
	// 30 chars — under the 41-char threshold
	input := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcd"
	got := StripSecrets(input)
	if got != input {
		t.Errorf(
			"short base64 was modified:\n  got: %s",
			got,
		)
	}
}

func TestStripSecrets_CleanTextUnchanged(t *testing.T) {
	input := "Build succeeded. 42 tests passed."
	got := StripSecrets(input)
	if got != input {
		t.Errorf("clean text was modified:\n  got: %s", got)
	}
}

func TestStripSecrets_MixedContent(t *testing.T) {
	input := "Deploying with DB_HOST=secret.rds.amazonaws.com " +
		"and Bearer tok_live_abc123"
	got := StripSecrets(input)
	if strings.Contains(got, "secret.rds") {
		t.Error("DB_HOST value not stripped")
	}
	if strings.Contains(got, "tok_live_abc123") {
		t.Error("bearer token not stripped")
	}
}

func TestStripSecrets_PrivateKey(t *testing.T) {
	input := "key: -----BEGIN RSA PRIVATE KEY-----\n" +
		"MIIE...data\n" +
		"-----END RSA PRIVATE KEY-----"
	got := StripSecrets(input)
	if strings.Contains(got, "BEGIN") {
		t.Errorf(
			"private key not stripped: %s", got,
		)
	}
}

func TestStripSecrets_SlackToken(t *testing.T) {
	input := "token is xoxb-1234-5678-abcdef"
	got := StripSecrets(input)
	if strings.Contains(got, "xoxb") {
		t.Errorf(
			"slack token not stripped: %s", got,
		)
	}
}

func TestStripSecrets_GitHubToken(t *testing.T) {
	input := "GITHUB_TOKEN=" +
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"
	got := StripSecrets(input)
	if strings.Contains(got, "ghp_") {
		t.Errorf(
			"github token not stripped: %s", got,
		)
	}
}

func TestStripSecrets_AnthropicKey(t *testing.T) {
	input := "key: sk-ant-api03-abcdef123456789xyz"
	got := StripSecrets(input)
	if strings.Contains(got, "sk-ant") {
		t.Errorf(
			"anthropic key not stripped: %s", got,
		)
	}
}

func TestStripSecrets_OpenAIKey(t *testing.T) {
	input := "key: sk-proj-abc123def456ghi789"
	got := StripSecrets(input)
	if strings.Contains(got, "sk-proj") {
		t.Errorf(
			"openai key not stripped: %s", got,
		)
	}
}

func TestStripSecrets_CaseInsensitiveSecret(t *testing.T) {
	input := "api_key=mysecretvalue123"
	got := StripSecrets(input)
	if strings.Contains(got, "mysecretvalue") {
		t.Errorf(
			"case-insensitive secret not stripped: %s",
			got,
		)
	}
}

// --- Truncate tests ---

func TestTruncate_ShortStringUnchanged(t *testing.T) {
	input := "hello world"
	got := Truncate(input, 100)
	if got != input {
		t.Errorf("short string changed: %s", got)
	}
}

func TestTruncate_ExactLengthUnchanged(t *testing.T) {
	input := "hello"
	got := Truncate(input, 5)
	if got != input {
		t.Errorf("exact-length string changed: %s", got)
	}
}

func TestTruncate_LongStringTruncated(t *testing.T) {
	input := "abcdefghij" // 10 chars
	got := Truncate(input, 7)
	if got != "abcd..." {
		t.Errorf("want 'abcd...', got '%s'", got)
	}
	if len(got) != 7 {
		t.Errorf("want len 7, got %d", len(got))
	}
}

func TestTruncate_VerySmallMax(t *testing.T) {
	input := "abcdefghij"
	got := Truncate(input, 3)
	if got != "..." {
		t.Errorf("want '...', got '%s'", got)
	}
}

func TestTruncate_EmptyString(t *testing.T) {
	got := Truncate("", 100)
	if got != "" {
		t.Errorf("empty string changed: %s", got)
	}
}

// --- Preview tests ---

func TestPreview_StripsAndTruncates(t *testing.T) {
	input := "Status: OK. API_KEY=super_secret_value " +
		"and more text that goes on for a while"
	got := Preview(input, 60)

	// Secret must be stripped
	if strings.Contains(got, "super_secret_value") {
		t.Error("secret not stripped in preview")
	}

	// Must be within length limit
	if len(got) > 60 {
		t.Errorf("preview too long: %d > 60", len(got))
	}
}

func TestPreview_TrimsWhitespace(t *testing.T) {
	input := "  hello world  "
	got := Preview(input, 100)
	if got != "hello world" {
		t.Errorf("whitespace not trimmed: '%s'", got)
	}
}

func TestPreview_EmptyAfterStrip(t *testing.T) {
	// Input is only a secret — after stripping, mostly
	// just a replacement token remains
	input := "API_KEY=onlysecrethere"
	got := Preview(input, 100)
	if strings.Contains(got, "onlysecrethere") {
		t.Error("secret leaked through preview")
	}
}

func TestPreview_MultilineInput(t *testing.T) {
	input := "line one\nDB_PASS=secret\nline three"
	got := Preview(input, 200)
	if strings.Contains(got, "secret") {
		t.Error("secret on second line not stripped")
	}
	if !strings.Contains(got, "line one") {
		t.Error("clean first line missing")
	}
}
