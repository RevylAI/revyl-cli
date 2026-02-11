// Package util provides shared utility functions for the CLI.
package util

import (
	"regexp"
	"strings"
)

var (
	// disallowedChars matches anything not in [a-z0-9-_].
	disallowedChars = regexp.MustCompile(`[^a-z0-9\-_]`)
	// multiHyphen collapses consecutive hyphens.
	multiHyphen = regexp.MustCompile(`-{2,}`)
)

// SanitizeForFilename converts a string to a CLI-safe, filesystem-safe name.
//   - Lowercases
//   - Replaces spaces with hyphens
//   - Strips all characters not in [a-z0-9-_]
//   - Collapses consecutive hyphens
//   - Trims leading/trailing hyphens
//
// Example: "Login Test (iOS)" → "login-test-ios"
func SanitizeForFilename(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = disallowedChars.ReplaceAllString(s, "")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
