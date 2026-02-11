// Package main provides shared helper functions for CLI commands.
package main

import (
	"fmt"
	"strings"
)

// maxResourceNameLen is the maximum allowed length for test/workflow names.
const maxResourceNameLen = 128

// reservedNames are subcommand names that cannot be used as test/workflow names
// because they collide with Cobra's command resolution.
var reservedNames = map[string]bool{
	"run": true, "create": true, "delete": true, "open": true, "cancel": true,
	"list": true, "remote": true, "push": true, "pull": true, "diff": true,
	"validate": true, "setup": true, "help": true,
}

// validateResourceName checks that a test, workflow, or module name is valid.
//
// The backend is the source of truth for display names and accepts spaces,
// parentheses, and other characters. This validation only rejects names that
// are dangerous (path separators), ambiguous (file extensions), or would
// collide with CLI subcommands.
//
// For local config aliases and filenames, callers should use
// util.SanitizeForFilename(name) separately.
//
// Rules:
//   - Must be non-empty
//   - Max 128 characters
//   - Cannot look like a file path (contains / or \)
//   - Cannot end with a known extension (.yaml, .yml, .json)
//   - Sanitized form cannot collide with a reserved subcommand name
//
// Parameters:
//   - name: The name to validate
//   - kind: A human-readable kind label for error messages (e.g. "test", "workflow")
//
// Returns:
//   - error: A descriptive error if validation fails, nil otherwise
func validateResourceName(name, kind string) error {
	if name == "" {
		return fmt.Errorf("%s name cannot be empty", kind)
	}

	if len(name) > maxResourceNameLen {
		return fmt.Errorf("%s name too long (%d chars, max %d)", kind, len(name), maxResourceNameLen)
	}

	// Check for path separators (security)
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("%s name cannot contain path separators — use a plain name (e.g. 'login-flow')", kind)
	}

	// Check for file extensions (common mistake)
	lower := strings.ToLower(name)
	for _, ext := range []string{".yaml", ".yml", ".json"} {
		if strings.HasSuffix(lower, ext) {
			return fmt.Errorf("%s name should not include a file extension (got '%s')", kind, name)
		}
	}

	// Check reserved words against the sanitized form
	// (e.g. "run" or "Run" or "  run  " would all sanitize to "run")
	sanitized := sanitizeNameForAlias(name)
	if reservedNames[sanitized] {
		return fmt.Errorf("'%s' is a reserved command name and cannot be used as a %s name", name, kind)
	}

	return nil
}

// sanitizeNameForAlias converts a display name into a CLI-safe alias suitable
// for config keys and filenames. Mirrors util.SanitizeForFilename logic inline
// to avoid a circular import.
func sanitizeNameForAlias(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	// Strip characters not in [a-z0-9-_]
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	s = b.String()
	// Collapse consecutive hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	return s
}

// looksLikeUUID checks if a string looks like a UUID (36 chars with hyphens at positions 8, 13, 18, 23).
// Does not validate hex digits; use a stricter check if needed.
//
// Parameters:
//   - s: The string to check
//
// Returns:
//   - bool: True if the string has UUID-like structure
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}
