// Package status provides shared status constants and helpers for test/workflow execution.
package status

import (
	"testing"
)

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{"completed", true},
		{"failed", true},
		{"cancelled", true},
		{"timeout", true},
		{"success", true},
		{"failure", true},
		{"COMPLETED", true}, // Case insensitive
		{"Failed", true},
		{"queued", false},
		{"running", false},
		{"starting", false},
		{"verifying", false},
		{"stopping", false},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := IsTerminal(tt.status)
			if result != tt.expected {
				t.Errorf("IsTerminal(%q) = %v, want %v", tt.status, result, tt.expected)
			}
		})
	}
}

func TestIsActive(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{"queued", true},
		{"starting", true},
		{"running", true},
		{"verifying", true},
		{"stopping", true},
		{"setup", true},
		{"RUNNING", true}, // Case insensitive
		{"completed", false},
		{"failed", false},
		{"cancelled", false},
		{"timeout", false},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := IsActive(tt.status)
			if result != tt.expected {
				t.Errorf("IsActive(%q) = %v, want %v", tt.status, result, tt.expected)
			}
		})
	}
}

func TestIsSuccess(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name         string
		status       string
		success      *bool
		errorMessage string
		expected     bool
	}{
		{"success field true", "completed", &trueVal, "", true},
		{"success field false", "completed", &falseVal, "", false},
		{"completed no error", "completed", nil, "", true},
		{"completed with error", "completed", nil, "some error", false},
		{"success status", "success", nil, "", true},
		{"failed status", "failed", nil, "", false},
		{"timeout status", "timeout", nil, "", false},
		{"cancelled status", "cancelled", nil, "", false},
		{"failure status", "failure", nil, "", false},
		{"unknown status", "unknown", nil, "", false},
		{"empty status", "", nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSuccess(tt.status, tt.success, tt.errorMessage)
			if result != tt.expected {
				t.Errorf("IsSuccess(%q, %v, %q) = %v, want %v", tt.status, tt.success, tt.errorMessage, result, tt.expected)
			}
		})
	}
}

func TestIsWorkflowSuccess(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		failedTests int
		expected    bool
	}{
		{"completed no failures", "completed", 0, true},
		{"completed with failures", "completed", 1, false},
		{"success no failures", "success", 0, true},
		{"success with failures", "success", 2, false},
		{"failed status", "failed", 0, false},
		{"running status", "running", 0, false},
		{"cancelled status", "cancelled", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsWorkflowSuccess(tt.status, tt.failedTests)
			if result != tt.expected {
				t.Errorf("IsWorkflowSuccess(%q, %d) = %v, want %v", tt.status, tt.failedTests, result, tt.expected)
			}
		})
	}
}

func TestStatusIcon(t *testing.T) {
	tests := []struct {
		status   string
		expected string
	}{
		{"queued", "⏳"},
		{"running", "▶"},
		{"setup", "▶"},
		{"verifying", "▶"},
		{"starting", "▶"},
		{"stopping", "▶"},
		{"completed", "✓"},
		{"success", "✓"},
		{"failed", "✗"},
		{"failure", "✗"},
		{"cancelled", "⊘"},
		{"timeout", "⏱"},
		{"unknown", "●"},
		{"", "●"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := StatusIcon(tt.status)
			if result != tt.expected {
				t.Errorf("StatusIcon(%q) = %q, want %q", tt.status, result, tt.expected)
			}
		})
	}
}

func TestStatusCategory(t *testing.T) {
	tests := []struct {
		status   string
		expected string
	}{
		{"queued", "dim"},
		{"running", "info"},
		{"setup", "info"},
		{"verifying", "info"},
		{"starting", "info"},
		{"stopping", "info"},
		{"completed", "success"},
		{"success", "success"},
		{"failed", "error"},
		{"failure", "error"},
		{"cancelled", "warning"},
		{"timeout", "warning"},
		{"unknown", "dim"},
		{"", "dim"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := StatusCategory(tt.status)
			if result != tt.expected {
				t.Errorf("StatusCategory(%q) = %q, want %q", tt.status, result, tt.expected)
			}
		})
	}
}
