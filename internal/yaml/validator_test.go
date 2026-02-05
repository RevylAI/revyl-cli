// Package yaml provides YAML test schema validation.
package yaml

import (
	"testing"
)

func TestValidateYAML_ValidTest(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Login Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: instructions
      step_description: "Tap the login button"
    - type: validation
      step_description: "Verify the dashboard is visible"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_MissingName(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: instructions
      step_description: "Tap the login button"
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to missing name")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected at least one error")
	}
	found := false
	for _, err := range result.Errors {
		if err == "Missing required field: test.metadata.name" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error about missing name, got: %v", result.Errors)
	}
}

func TestValidateYAML_InvalidPlatform(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "windows"
  build:
    name: "My App"
  blocks:
    - type: instructions
      step_description: "Do something"
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to invalid platform")
	}
	found := false
	for _, err := range result.Errors {
		if err == "Invalid platform 'windows': must be 'ios' or 'android'" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error about invalid platform, got: %v", result.Errors)
	}
}

func TestValidateYAML_NoBlocks(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks: []
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to no blocks")
	}
	found := false
	for _, err := range result.Errors {
		if err == "Test must have at least one block" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error about no blocks, got: %v", result.Errors)
	}
}

func TestValidateYAML_InvalidBlockType(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: invalid_type
      step_description: "Something"
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to invalid block type")
	}
	found := false
	for _, err := range result.Errors {
		if err == "Block 1: Invalid block type 'invalid_type'" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error about invalid block type, got: %v", result.Errors)
	}
}

func TestValidateYAML_ExtractionBlock(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: extraction
      step_description: "Extract the user name"
      variable_name: "user-name"
    - type: instructions
      step_description: "Use {{user-name}} in the search"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_UndefinedVariable(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: instructions
      step_description: "Use {{undefined-var}} in the search"
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to undefined variable")
	}
	found := false
	for _, err := range result.Errors {
		if err == "Variable '{{undefined-var}}' used but never defined via extraction block" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error about undefined variable, got: %v", result.Errors)
	}
}

func TestValidateYAML_ManualBlock(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: wait
      step_description: "5"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_InvalidManualStepType(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: invalid_step
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to invalid step_type")
	}
}

func TestValidateYAML_IfBlock(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: if
      condition: "login button is visible"
      then:
        - type: instructions
          step_description: "Tap login"
      else:
        - type: instructions
          step_description: "Skip login"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_IfBlockMissingCondition(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: if
      then:
        - type: instructions
          step_description: "Do something"
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to missing condition")
	}
}

func TestValidateYAML_WhileBlock(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: while
      condition: "more items exist"
      body:
        - type: instructions
          step_description: "Scroll down"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_ParseError(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test
    platform: android
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to parse error")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected at least one error")
	}
}

func TestIsValidVariableName(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"user-name", true},
		{"user123", true},
		{"a", true},
		{"user-name-123", true},
		{"-invalid", false},
		{"invalid-", false},
		{"in--valid", false},
		{"UPPERCASE", false},
		{"with_underscore", false},
		{"with space", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidVariableName(tt.name)
			if result != tt.expected {
				t.Errorf("isValidVariableName(%q) = %v, want %v", tt.name, result, tt.expected)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"123", true},
		{"0", true},
		{"5", true},
		{"abc", false},
		{"12.3", false},
		{"-5", false},
		{"", false},
		{"  ", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isNumeric(tt.input)
			if result != tt.expected {
				t.Errorf("isNumeric(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsValidLocation(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"37.7749,-122.4194", true},
		{"-33.8688,151.2093", true},
		{"0,0", true},
		{"37.7749", false},
		{"abc,def", false},
		{"37.7749,", false},
		{",122.4194", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isValidLocation(tt.input)
			if result != tt.expected {
				t.Errorf("isValidLocation(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
