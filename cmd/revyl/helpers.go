// Package main provides shared helper functions for CLI commands.
package main

import (
	"sort"
)

// isValidUUID checks if a string is a valid UUID format.
//
// UUID format: 8-4-4-4-12 hex characters (36 total with dashes)
// Example: 027b91de-4a21-4bca-acfe-32db2a628f51
//
// Parameters:
//   - s: The string to validate
//
// Returns:
//   - bool: True if the string is a valid UUID format
func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}

	// Check dashes are in the right places
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}

	// Check all other characters are hex digits
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue // Skip dash positions
		}
		if !isHexDigit(byte(c)) {
			return false
		}
	}

	return true
}

// isHexDigit checks if a byte is a valid hexadecimal digit.
//
// Parameters:
//   - c: The byte to check
//
// Returns:
//   - bool: True if the byte is 0-9, a-f, or A-F
func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// looksLikeUUID checks if a string looks like a UUID (36 chars with hyphens at positions 8, 13, 18, 23).
// Does not validate hex digits; use isValidUUID for strict validation.
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

// getTestNames returns a sorted slice of test names from the config.
//
// Parameters:
//   - tests: Map of test name to test ID
//
// Returns:
//   - []string: Sorted slice of test names
func getTestNames(tests map[string]string) []string {
	if tests == nil {
		return []string{}
	}

	names := make([]string, 0, len(tests))
	for name := range tests {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// getWorkflowNames returns a sorted slice of workflow names from the config.
//
// Parameters:
//   - workflows: Map of workflow name to workflow ID
//
// Returns:
//   - []string: Sorted slice of workflow names
func getWorkflowNames(workflows map[string]string) []string {
	if workflows == nil {
		return []string{}
	}

	names := make([]string, 0, len(workflows))
	for name := range workflows {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
