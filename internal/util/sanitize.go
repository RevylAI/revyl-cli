// Package util provides shared utility functions for the CLI.
package util

import (
	"fmt"
	"path/filepath"
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

// SafeTestPath resolves a test alias to a .yaml path under testsDir, guarding
// against path traversal. The alias is sanitized via SanitizeForFilename and the
// resolved path is verified to remain under testsDir.
//
// Parameters:
//   - testsDir: Absolute or relative path to the .revyl/tests/ directory.
//   - alias: User-provided test alias or name (may contain unsafe characters).
//
// Returns:
//   - string: The safe, resolved file path (testsDir/<sanitized-alias>.yaml).
//   - error: Non-nil if the alias is empty after sanitization or escapes testsDir.
func SafeTestPath(testsDir, alias string) (string, error) {
	clean := SanitizeForFilename(alias)
	if clean == "" {
		return "", fmt.Errorf("test alias %q is empty after sanitization", alias)
	}

	resolved := filepath.Join(testsDir, clean+".yaml")

	absTests, err := filepath.Abs(testsDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve tests directory: %w", err)
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot resolve test path: %w", err)
	}

	if !strings.HasPrefix(absResolved, absTests+string(filepath.Separator)) {
		return "", fmt.Errorf("test alias %q resolves outside the tests directory", alias)
	}

	return resolved, nil
}
