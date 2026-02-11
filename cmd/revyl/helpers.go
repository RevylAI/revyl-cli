// Package main provides shared helper functions for CLI commands.
package main

import (
	"fmt"
	"strings"
	"unicode"
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

// validateResourceName checks that a test or workflow name is safe for use as a
// config key, file name, URL component, and CLI argument.
//
// Rules:
//   - Must be non-empty
//   - Max 128 characters
//   - No whitespace
//   - Only lowercase alphanumeric, hyphens, and underscores
//   - Cannot collide with a reserved subcommand name
//   - Cannot look like a file path (contains / or \)
//   - Cannot end with a known extension (.yaml, .yml, .json)
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

	// Check for whitespace
	for _, r := range name {
		if unicode.IsSpace(r) {
			return fmt.Errorf("%s name cannot contain spaces — use hyphens instead (e.g. 'login-flow')", kind)
		}
	}

	// Check for path separators
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("%s name cannot contain path separators — use a plain name (e.g. 'login-flow')", kind)
	}

	// Check for file extensions
	lower := strings.ToLower(name)
	for _, ext := range []string{".yaml", ".yml", ".json"} {
		if strings.HasSuffix(lower, ext) {
			return fmt.Errorf("%s name should not include a file extension (got '%s')", kind, name)
		}
	}

	// Check allowed character set: a-z 0-9 - _
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("%s name contains invalid character '%c' — only lowercase letters, numbers, hyphens, and underscores are allowed", kind, r)
		}
	}

	// Check reserved words
	if reservedNames[name] {
		return fmt.Errorf("'%s' is a reserved command name and cannot be used as a %s name", name, kind)
	}

	return nil
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
