// Package main provides tests for the helper functions.
package main

import (
	"testing"
)

// TestLooksLikeUUID tests the UUID-like structure detection function.
func TestLooksLikeUUID(t *testing.T) {
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
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "test name (not UUID)",
			input:    "login-flow",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := looksLikeUUID(tt.input)
			if result != tt.expected {
				t.Errorf("looksLikeUUID(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestValidateResourceName tests the name validation function.
func TestValidateResourceName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		kind      string
		wantError bool
	}{
		{name: "valid simple name", input: "login-flow", kind: "test", wantError: false},
		{name: "valid with underscores", input: "login_flow", kind: "test", wantError: false},
		{name: "valid with numbers", input: "test-123", kind: "test", wantError: false},
		{name: "valid all digits and letters", input: "abc123def", kind: "test", wantError: false},
		{name: "empty name", input: "", kind: "test", wantError: true},
		{name: "has spaces", input: "login flow", kind: "test", wantError: false},
		{name: "has path separator", input: "tests/login", kind: "test", wantError: true},
		{name: "has backslash", input: "tests\\login", kind: "test", wantError: true},
		{name: "ends with .yaml", input: "login-flow.yaml", kind: "test", wantError: true},
		{name: "ends with .yml", input: "login-flow.yml", kind: "test", wantError: true},
		{name: "ends with .json", input: "login-flow.json", kind: "test", wantError: true},
		{name: "uppercase letters", input: "Login-Flow", kind: "test", wantError: false},
		{name: "special chars", input: "login@flow", kind: "test", wantError: false},
		{name: "has parentheses", input: "login(v2)", kind: "test", wantError: false},
		{name: "has brackets", input: "test[ios]", kind: "test", wantError: false},
		{name: "reserved word run", input: "run", kind: "test", wantError: true},
		{name: "reserved word create", input: "create", kind: "test", wantError: true},
		{name: "reserved word delete", input: "delete", kind: "test", wantError: true},
		{name: "reserved word list", input: "list", kind: "test", wantError: true},
		{name: "reserved word help", input: "help", kind: "test", wantError: true},
		{name: "workflow kind", input: "smoke-tests", kind: "workflow", wantError: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResourceName(tt.input, tt.kind)
			if (err != nil) != tt.wantError {
				t.Errorf("validateResourceName(%q, %q) error = %v, wantError %v", tt.input, tt.kind, err, tt.wantError)
			}
		})
	}
}

// TestValidateResourceNameMaxLength tests that names exceeding the max length are rejected.
func TestValidateResourceNameMaxLength(t *testing.T) {
	// Build a name exactly at the limit
	name := ""
	for i := 0; i < maxResourceNameLen; i++ {
		name += "a"
	}
	if err := validateResourceName(name, "test"); err != nil {
		t.Errorf("validateResourceName(128 chars) unexpected error: %v", err)
	}

	// One char over
	name += "a"
	if err := validateResourceName(name, "test"); err == nil {
		t.Error("validateResourceName(129 chars) expected error, got nil")
	}
}
