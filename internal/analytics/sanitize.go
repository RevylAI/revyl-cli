package analytics

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxSanitizedStringLength = 500

var (
	apiKeyPattern      = regexp.MustCompile(`\brk_[A-Za-z0-9._-]{8,}\b`)
	issuedTokenPattern = regexp.MustCompile(`\br(?:st|rt)_[a-z0-9][a-z0-9_-]{0,31}_[A-Za-z0-9_-]{43}($|[^A-Za-z0-9_-])`)
	bearerPattern      = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	secretKVPattern    = regexp.MustCompile(`(?i)\b(api[_-]?key|authorization|token|secret|password)\s*[:=]\s*\S+`)
	emailPattern       = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
	urlPattern         = regexp.MustCompile(`https?://[^\s]+`)
	userPathPattern    = regexp.MustCompile(`(?i)(/Users/|/home/)[^\s]+`)
	windowsPathPattern = regexp.MustCompile(`(?i)\b[A-Z]:\\[^\s]+`)
)

// customerNameListPattern matches the rest of a line listing customer-authored
// names, such as a "test not found" error's local test aliases.
var customerNameListPattern = regexp.MustCompile(`(?i)\b(available (?:tests|areas)):[^\n]*`)

func sanitizeString(value string) string {
	out := strings.TrimSpace(value)
	if out == "" {
		return ""
	}
	out = customerNameListPattern.ReplaceAllString(out, "$1: <redacted>")
	out = bearerPattern.ReplaceAllString(out, "Bearer <redacted>")
	out = secretKVPattern.ReplaceAllString(out, "$1=<redacted>")
	out = apiKeyPattern.ReplaceAllString(out, "<redacted-api-key>")
	out = issuedTokenPattern.ReplaceAllString(out, "<redacted-api-key>$1")
	out = emailPattern.ReplaceAllString(out, "<email>")
	out = urlPattern.ReplaceAllString(out, "<url>")
	out = userPathPattern.ReplaceAllString(out, "<path>")
	out = windowsPathPattern.ReplaceAllString(out, "<path>")
	if utf8.RuneCountInString(out) > maxSanitizedStringLength {
		out = string([]rune(out)[:maxSanitizedStringLength]) + "...<truncated>"
	}
	return out
}

func sanitizeDiagnosticString(value string, redactions []string) string {
	for _, redaction := range redactions {
		value = redactCommandInput(value, redaction)
	}
	return sanitizeString(value)
}

// redactCommandInput redacts an exact command input when it appears as a
// standalone token. It protects short inputs such as "3" without corrupting
// unrelated diagnostic text such as "30s".
func redactCommandInput(value, redaction string) string {
	if value == "" || redaction == "" {
		return value
	}

	var out strings.Builder
	searchFrom := 0
	for {
		offset := strings.Index(value[searchFrom:], redaction)
		if offset < 0 {
			out.WriteString(value[searchFrom:])
			return out.String()
		}

		start := searchFrom + offset
		end := start + len(redaction)
		out.WriteString(value[searchFrom:start])
		if hasCommandInputBoundary(value, start, end) {
			out.WriteString("<command-input>")
		} else {
			out.WriteString(redaction)
		}
		searchFrom = end
	}
}

func hasCommandInputBoundary(value string, start, end int) bool {
	if start > 0 {
		before, _ := utf8.DecodeLastRuneInString(value[:start])
		if isCommandInputTokenRune(before) {
			return false
		}
	}
	if end < len(value) {
		after, _ := utf8.DecodeRuneInString(value[end:])
		if isCommandInputTokenRune(after) {
			return false
		}
	}
	return true
}

func isCommandInputTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}
