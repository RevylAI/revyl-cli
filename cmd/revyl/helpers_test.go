// Package main provides tests for the helper functions.
package main

import (
	"testing"
)

// TestIsValidUUID tests the UUID validation function.
func TestIsValidUUID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid UUID lowercase",
			input:    "027b91de-4a21-4bca-acfe-32db2a628f51",
			expected: true,
		},
		{
			name:     "valid UUID uppercase",
			input:    "027B91DE-4A21-4BCA-ACFE-32DB2A628F51",
			expected: true,
		},
		{
			name:     "valid UUID mixed case",
			input:    "027b91DE-4a21-4BCA-acfe-32DB2a628f51",
			expected: true,
		},
		{
			name:     "too short",
			input:    "027b91de-4a21-4bca-acfe",
			expected: false,
		},
		{
			name:     "too long",
			input:    "027b91de-4a21-4bca-acfe-32db2a628f51-extra",
			expected: false,
		},
		{
			name:     "missing dashes",
			input:    "027b91de4a214bcaacfe32db2a628f51",
			expected: false,
		},
		{
			name:     "wrong dash positions",
			input:    "027b91de4-a21-4bca-acfe-32db2a628f51",
			expected: false,
		},
		{
			name:     "invalid characters",
			input:    "027b91de-4a21-4bca-acfe-32db2a628g51",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "test name (not UUID)",
			input:    "login-flow",
			expected: false,
		},
		{
			name:     "list (common typo)",
			input:    "list",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidUUID(tt.input)
			if result != tt.expected {
				t.Errorf("isValidUUID(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestGetTestNames tests the test names extraction function.
func TestGetTestNames(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected []string
	}{
		{
			name:     "nil map",
			input:    nil,
			expected: []string{},
		},
		{
			name:     "empty map",
			input:    map[string]string{},
			expected: []string{},
		},
		{
			name: "single test",
			input: map[string]string{
				"login-flow": "abc123",
			},
			expected: []string{"login-flow"},
		},
		{
			name: "multiple tests sorted",
			input: map[string]string{
				"zebra-test":  "id3",
				"alpha-test":  "id1",
				"middle-test": "id2",
			},
			expected: []string{"alpha-test", "middle-test", "zebra-test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getTestNames(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("getTestNames() returned %d items, want %d", len(result), len(tt.expected))
				return
			}
			for i, name := range result {
				if name != tt.expected[i] {
					t.Errorf("getTestNames()[%d] = %q, want %q", i, name, tt.expected[i])
				}
			}
		})
	}
}

// TestGetWorkflowNames tests the workflow names extraction function.
func TestGetWorkflowNames(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected []string
	}{
		{
			name:     "nil map",
			input:    nil,
			expected: []string{},
		},
		{
			name:     "empty map",
			input:    map[string]string{},
			expected: []string{},
		},
		{
			name: "single workflow",
			input: map[string]string{
				"smoke-tests": "wf123",
			},
			expected: []string{"smoke-tests"},
		},
		{
			name: "multiple workflows sorted",
			input: map[string]string{
				"regression":  "wf3",
				"smoke-tests": "wf1",
				"nightly":     "wf2",
			},
			expected: []string{"nightly", "regression", "smoke-tests"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getWorkflowNames(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("getWorkflowNames() returned %d items, want %d", len(result), len(tt.expected))
				return
			}
			for i, name := range result {
				if name != tt.expected[i] {
					t.Errorf("getWorkflowNames()[%d] = %q, want %q", i, name, tt.expected[i])
				}
			}
		})
	}
}

// TestIsHexDigit tests the hex digit validation function.
func TestIsHexDigit(t *testing.T) {
	// Valid hex digits
	validChars := "0123456789abcdefABCDEF"
	for _, c := range validChars {
		if !isHexDigit(byte(c)) {
			t.Errorf("isHexDigit(%q) = false, want true", c)
		}
	}

	// Invalid characters
	invalidChars := "ghijklmnopqrstuvwxyzGHIJKLMNOPQRSTUVWXYZ!@#$%^&*()-_=+[]{}|;:',.<>?/`~"
	for _, c := range invalidChars {
		if isHexDigit(byte(c)) {
			t.Errorf("isHexDigit(%q) = true, want false", c)
		}
	}
}
