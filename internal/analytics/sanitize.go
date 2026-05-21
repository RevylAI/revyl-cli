package analytics

import (
	"regexp"
	"strings"
)

const maxSanitizedStringLength = 500

var (
	apiKeyPattern      = regexp.MustCompile(`\brk_[A-Za-z0-9._-]{8,}\b`)
	bearerPattern      = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	secretKVPattern    = regexp.MustCompile(`(?i)\b(api[_-]?key|authorization|token|secret|password)\s*[:=]\s*\S+`)
	emailPattern       = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
	urlPattern         = regexp.MustCompile(`https?://[^\s]+`)
	userPathPattern    = regexp.MustCompile(`(?i)(/Users/|/home/)[^\s]+`)
	windowsPathPattern = regexp.MustCompile(`(?i)\b[A-Z]:\\[^\s]+`)
)

func sanitizeString(value string) string {
	out := strings.TrimSpace(value)
	if out == "" {
		return ""
	}
	out = bearerPattern.ReplaceAllString(out, "Bearer <redacted>")
	out = secretKVPattern.ReplaceAllString(out, "$1=<redacted>")
	out = apiKeyPattern.ReplaceAllString(out, "<redacted-api-key>")
	out = emailPattern.ReplaceAllString(out, "<email>")
	out = urlPattern.ReplaceAllString(out, "<url>")
	out = userPathPattern.ReplaceAllString(out, "<path>")
	out = windowsPathPattern.ReplaceAllString(out, "<path>")
	if len(out) > maxSanitizedStringLength {
		out = out[:maxSanitizedStringLength] + "...<truncated>"
	}
	return out
}
