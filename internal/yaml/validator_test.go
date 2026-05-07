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
	yamlWithPresetVar := `
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
	result := ValidateYAML(yamlWithPresetVar)
	if !result.Valid {
		t.Errorf("Expected valid YAML (undefined variables are warnings, not errors), got errors: %v", result.Errors)
	}
	found := false
	for _, w := range result.Warnings {
		if w == "Variable '{{undefined-var}}' used but not defined in YAML -- ensure it is created via set_variable or the Variables tab before running" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected warning about undefined variable, got warnings: %v", result.Warnings)
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

func TestValidateYAML_WhitespaceStepDescription(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: instructions
      step_description: "   "
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to whitespace-only step_description")
	}
}

func TestValidateYAML_WhitespaceCondition(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: if
      condition: "   "
      then:
        - type: instructions
          step_description: "Tap login"
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML due to whitespace-only condition")
	}
}

func TestValidateYAML_ManualOpenAppNoDescription(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: open_app
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML for manual/open_app without description, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_ManualBlockRequiresStepType(t *testing.T) {
	missingStepType := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_description: "5"
`
	result := ValidateYAML(missingStepType)
	if result.Valid {
		t.Error("Expected invalid YAML when manual block omits step_type")
	}

	withStepType := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: wait
      step_description: "5"
    - type: manual
      step_type: wait
      step_description: "3"
`
	result = ValidateYAML(withStepType)
	if !result.Valid {
		t.Errorf("Expected valid YAML with explicit step_type, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_ManualBlockNoStepTypeNonNumericFails(t *testing.T) {
	yamlContent := `
test:
  metadata:
    name: "Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_description: "not a number"
`
	result := ValidateYAML(yamlContent)
	if result.Valid {
		t.Error("Expected invalid YAML when manual block has no step_type and non-numeric description")
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

func TestValidateYAML_DownloadFileWithURL(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: download_file
      step_description: "https://example.com/cert.pem"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML for download_file with URL, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_SetAppearanceDark(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: set_appearance
      step_description: "dark"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML for set_appearance, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_SetAppearanceInvalidValue(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: set_appearance
      step_description: "system"
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML for unsupported set_appearance value")
	}
}

func TestValidateYAML_DownloadFileWithRevylFileURI(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: download_file
      step_description: "revyl-file://a0dfedcd-26ab-4b69-916e-259f0468714e"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML for download_file with revyl-file:// URI, got errors: %v", result.Errors)
	}
}

func TestValidateYAML_DownloadFileWithOrgFileName(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: download_file
      file: "staging-cert.pem"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML for download_file with file name, got errors: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("Expected warning about org file resolution at push time")
	}
}

func TestValidateYAML_DownloadFileBothDescAndFile(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: download_file
      step_description: "https://example.com/cert.pem"
      file: "staging-cert.pem"
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML when both step_description and file are set")
	}
}

func TestValidateYAML_DownloadFileNeitherDescNorFile(t *testing.T) {
	invalidYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: download_file
`
	result := ValidateYAML(invalidYAML)
	if result.Valid {
		t.Error("Expected invalid YAML when neither step_description nor file is set")
	}
}

func TestValidateYAML_DownloadFileNonURLWarning(t *testing.T) {
	validYAML := `
test:
  metadata:
    name: "Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: manual
      step_type: download_file
      step_description: "not-a-url"
`
	result := ValidateYAML(validYAML)
	if !result.Valid {
		t.Errorf("Expected valid YAML (non-URL is warning, not error), got errors: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("Expected warning about non-URL step_description")
	}
}

func TestIsValidVariableName(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"user-name", true},
		{"user_name", true},
		{"user-name_123", true},
		{"UserName", true},
		{"USER_NAME", true},
		{"user123", true},
		{"a", true},
		{"user-name-123", true},
		{"-invalid", false},
		{"invalid-", false},
		{"_invalid", false},
		{"invalid_", false},
		{"in--valid", false},
		{"in__valid", false},
		{"in-_valid", false},
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

func TestValidateYAML_GlobalVariableNoWarning(t *testing.T) {
	yamlContent := `
test:
  metadata:
    name: "Global Var Test"
    platform: "android"
  build:
    name: "My App"
  blocks:
    - type: instructions
      step_description: "Enter {{global.login-email}} in the email field"
`
	result := ValidateYAML(yamlContent)
	if !result.Valid {
		t.Errorf("Expected valid YAML, got errors: %v", result.Errors)
	}
	for _, w := range result.Warnings {
		if w == "Variable '{{global.login-email}}' used but not defined in YAML -- ensure it is created via set_variable or the Variables tab before running" {
			t.Error("Should not warn about undefined global variable references")
		}
	}
}

func TestValidateYAML_GlobalAndLocalVarsMixed(t *testing.T) {
	yamlContent := `
test:
  metadata:
    name: "Mixed Var Test"
    platform: "ios"
  build:
    name: "My App"
  blocks:
    - type: extraction
      step_description: "Extract the OTP"
      variable_name: "otp-code"
    - type: instructions
      step_description: "Enter {{otp-code}} and {{global.api-key}} and {{undefined-local}}"
`
	result := ValidateYAML(yamlContent)
	if !result.Valid {
		t.Errorf("Expected valid YAML, got errors: %v", result.Errors)
	}

	// Should warn about undefined-local but NOT about global.api-key
	foundLocalWarning := false
	for _, w := range result.Warnings {
		if contains(w, "undefined-local") {
			foundLocalWarning = true
		}
		if contains(w, "global.api-key") {
			t.Error("Should not warn about global variable references")
		}
	}
	if !foundLocalWarning {
		t.Error("Expected warning about undefined-local variable")
	}
}

func TestVariablePattern_MatchesGlobalPrefix(t *testing.T) {
	tests := []struct {
		input   string
		matches []string
	}{
		{"{{my-var}}", []string{"my-var"}},
		{"{{my_var}}", []string{"my_var"}},
		{"{{MyVar}}", []string{"MyVar"}},
		{"{{global.my-var}}", []string{"global.my-var"}},
		{"{{global.login_email}} and {{otp_code}}", []string{"global.login_email", "otp_code"}},
		{"{{global.API_KEY}}", []string{"global.API_KEY"}},
		{"no variables here", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			allMatches := variablePattern.FindAllStringSubmatch(tt.input, -1)
			var got []string
			for _, m := range allMatches {
				if len(m) > 1 {
					got = append(got, m[1])
				}
			}
			if len(got) != len(tt.matches) {
				t.Errorf("variablePattern on %q: got %v, want %v", tt.input, got, tt.matches)
				return
			}
			for i := range got {
				if got[i] != tt.matches[i] {
					t.Errorf("variablePattern on %q: match[%d] = %q, want %q", tt.input, i, got[i], tt.matches[i])
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
