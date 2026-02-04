// Package yaml provides YAML test schema validation.
//
// This package validates YAML test definitions against the Revyl test schema,
// checking for syntax errors, required fields, and common issues.
package yaml

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidationResult contains the result of YAML validation.
//
// Fields:
//   - Valid: Whether the YAML is valid
//   - Errors: List of validation errors
//   - Warnings: List of validation warnings (non-fatal issues)
type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// TestDefinition represents the structure of a YAML test file.
type TestDefinition struct {
	Test TestContent `yaml:"test"`
}

// TestContent contains the test definition content.
type TestContent struct {
	Metadata TestMetadata `yaml:"metadata"`
	Build    TestBuild    `yaml:"build"`
	Blocks   []Block      `yaml:"blocks"`
}

// TestMetadata contains test metadata.
type TestMetadata struct {
	Name     string `yaml:"name"`
	Platform string `yaml:"platform"`
}

// TestBuild contains build configuration.
type TestBuild struct {
	Name          string `yaml:"name"`
	PinnedVersion string `yaml:"pinned_version,omitempty"`
}

// Block represents a test block (instructions, validation, etc.)
type Block struct {
	Type            string  `yaml:"type"`
	StepDescription string  `yaml:"step_description,omitempty"`
	StepType        string  `yaml:"step_type,omitempty"`
	VariableName    string  `yaml:"variable_name,omitempty"`
	Condition       string  `yaml:"condition,omitempty"`
	Then            []Block `yaml:"then,omitempty"`
	Else            []Block `yaml:"else,omitempty"`
	Body            []Block `yaml:"body,omitempty"`
}

// validBlockTypes contains all valid block type values.
var validBlockTypes = map[string]bool{
	"instructions":   true,
	"validation":     true,
	"extraction":     true,
	"manual":         true,
	"if":             true,
	"while":          true,
	"code_execution": true,
}

// validStepTypes contains all valid manual step_type values.
var validStepTypes = map[string]bool{
	"wait":         true,
	"open_app":     true,
	"kill_app":     true,
	"go_home":      true,
	"navigate":     true,
	"set_location": true,
}

// variablePattern matches {{variable-name}} syntax.
var variablePattern = regexp.MustCompile(`\{\{([a-z0-9-]+)\}\}`)

// ValidateYAML validates a YAML test definition.
//
// This function checks:
//   - YAML syntax validity
//   - Required fields (name, platform, build.name, blocks)
//   - Block type validity
//   - Manual step_type validity
//   - Variable definitions before use
//   - Platform values (ios/android only)
//   - Common issues (warnings)
//
// Parameters:
//   - content: The YAML content as a string
//
// Returns:
//   - *ValidationResult: Validation result with errors/warnings
func ValidateYAML(content string) *ValidationResult {
	result := &ValidationResult{Valid: true}

	var test TestDefinition
	if err := yaml.Unmarshal([]byte(content), &test); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("YAML parse error: %v", err))
		return result
	}

	// Validate required fields
	if test.Test.Metadata.Name == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "Missing required field: test.metadata.name")
	}

	if test.Test.Metadata.Platform == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "Missing required field: test.metadata.platform")
	} else if test.Test.Metadata.Platform != "ios" && test.Test.Metadata.Platform != "android" {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("Invalid platform '%s': must be 'ios' or 'android'", test.Test.Metadata.Platform))
	}

	if test.Test.Build.Name == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "Missing required field: test.build.name")
	}

	if len(test.Test.Blocks) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, "Test must have at least one block")
	}

	// Validate blocks and track defined variables
	definedVars := make(map[string]bool)
	usedVars := make(map[string]bool)

	for i, block := range test.Test.Blocks {
		blockErrors, blockWarnings := validateBlock(block, i+1, "", definedVars, usedVars)
		if len(blockErrors) > 0 {
			result.Valid = false
			result.Errors = append(result.Errors, blockErrors...)
		}
		result.Warnings = append(result.Warnings, blockWarnings...)
	}

	// Check for undefined variables
	for varName := range usedVars {
		if !definedVars[varName] {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("Variable '{{%s}}' used but never defined via extraction block", varName))
		}
	}

	// Check for common issues (warnings)
	if len(test.Test.Blocks) > 0 {
		firstBlock := test.Test.Blocks[0]
		if firstBlock.Type == "manual" && firstBlock.StepType == "open_app" {
			result.Warnings = append(result.Warnings, "First block is 'open_app' - app opens automatically at test start, this may be unnecessary")
		}
	}

	return result
}

// validateBlock validates a single block and its nested blocks.
//
// Parameters:
//   - block: The block to validate
//   - index: Block index (1-based) for error messages
//   - prefix: Prefix for nested block paths (e.g., "then[0]")
//   - definedVars: Map of defined variable names
//   - usedVars: Map of used variable names
//
// Returns:
//   - []string: List of errors
//   - []string: List of warnings
func validateBlock(block Block, index int, prefix string, definedVars, usedVars map[string]bool) ([]string, []string) {
	var errors []string
	var warnings []string

	blockPath := fmt.Sprintf("Block %d", index)
	if prefix != "" {
		blockPath = fmt.Sprintf("%s.%s[%d]", prefix, block.Type, index-1)
	}

	// Validate block type
	if !validBlockTypes[block.Type] {
		errors = append(errors, fmt.Sprintf("%s: Invalid block type '%s'", blockPath, block.Type))
		return errors, warnings
	}

	// Extract variables used in step_description
	if block.StepDescription != "" {
		matches := variablePattern.FindAllStringSubmatch(block.StepDescription, -1)
		for _, match := range matches {
			if len(match) > 1 {
				usedVars[match[1]] = true
			}
		}
	}

	// Extract variables used in condition
	if block.Condition != "" {
		matches := variablePattern.FindAllStringSubmatch(block.Condition, -1)
		for _, match := range matches {
			if len(match) > 1 {
				usedVars[match[1]] = true
			}
		}
	}

	switch block.Type {
	case "instructions", "validation":
		if block.StepDescription == "" {
			errors = append(errors, fmt.Sprintf("%s (%s): Missing step_description", blockPath, block.Type))
		}

	case "extraction":
		if block.StepDescription == "" {
			errors = append(errors, fmt.Sprintf("%s (extraction): Missing step_description", blockPath))
		}
		if block.VariableName == "" {
			errors = append(errors, fmt.Sprintf("%s (extraction): Missing variable_name", blockPath))
		} else {
			// Validate variable name format (kebab-case)
			if !isValidVariableName(block.VariableName) {
				errors = append(errors, fmt.Sprintf("%s (extraction): Invalid variable_name '%s' - must be kebab-case (lowercase letters, numbers, hyphens)", blockPath, block.VariableName))
			}
			definedVars[block.VariableName] = true
		}

	case "manual":
		if !validStepTypes[block.StepType] {
			errors = append(errors, fmt.Sprintf("%s (manual): Invalid step_type '%s' - must be one of: wait, open_app, kill_app, go_home, navigate, set_location", blockPath, block.StepType))
		}

		// Validate step_description based on step_type
		switch block.StepType {
		case "wait":
			if block.StepDescription == "" {
				errors = append(errors, fmt.Sprintf("%s (manual/wait): Missing step_description (number of seconds)", blockPath))
			} else if !isNumeric(block.StepDescription) {
				warnings = append(warnings, fmt.Sprintf("%s (manual/wait): step_description should be a number (seconds), got '%s'", blockPath, block.StepDescription))
			}
		case "navigate":
			if block.StepDescription == "" {
				errors = append(errors, fmt.Sprintf("%s (manual/navigate): Missing step_description (URL or deep link)", blockPath))
			}
		case "set_location":
			if block.StepDescription == "" {
				errors = append(errors, fmt.Sprintf("%s (manual/set_location): Missing step_description (latitude,longitude)", blockPath))
			} else if !isValidLocation(block.StepDescription) {
				warnings = append(warnings, fmt.Sprintf("%s (manual/set_location): step_description should be 'latitude,longitude' format, got '%s'", blockPath, block.StepDescription))
			}
		}

	case "if":
		if block.Condition == "" {
			errors = append(errors, fmt.Sprintf("%s (if): Missing condition", blockPath))
		}
		if len(block.Then) == 0 {
			errors = append(errors, fmt.Sprintf("%s (if): Missing 'then' blocks", blockPath))
		}

		// Validate nested then blocks
		for i, thenBlock := range block.Then {
			nestedErrors, nestedWarnings := validateBlock(thenBlock, i+1, blockPath+".then", definedVars, usedVars)
			errors = append(errors, nestedErrors...)
			warnings = append(warnings, nestedWarnings...)
		}

		// Validate nested else blocks
		for i, elseBlock := range block.Else {
			nestedErrors, nestedWarnings := validateBlock(elseBlock, i+1, blockPath+".else", definedVars, usedVars)
			errors = append(errors, nestedErrors...)
			warnings = append(warnings, nestedWarnings...)
		}

	case "while":
		if block.Condition == "" {
			errors = append(errors, fmt.Sprintf("%s (while): Missing condition", blockPath))
		}
		if len(block.Body) == 0 {
			errors = append(errors, fmt.Sprintf("%s (while): Missing 'body' blocks", blockPath))
		}

		// Validate nested body blocks
		for i, bodyBlock := range block.Body {
			nestedErrors, nestedWarnings := validateBlock(bodyBlock, i+1, blockPath+".body", definedVars, usedVars)
			errors = append(errors, nestedErrors...)
			warnings = append(warnings, nestedWarnings...)
		}

	case "code_execution":
		if block.StepDescription == "" {
			errors = append(errors, fmt.Sprintf("%s (code_execution): Missing step_description (script UUID)", blockPath))
		}
		// If variable_name is provided, register it
		if block.VariableName != "" {
			if !isValidVariableName(block.VariableName) {
				errors = append(errors, fmt.Sprintf("%s (code_execution): Invalid variable_name '%s' - must be kebab-case", blockPath, block.VariableName))
			}
			definedVars[block.VariableName] = true
		}
	}

	return errors, warnings
}

// isValidVariableName checks if a variable name follows kebab-case convention.
func isValidVariableName(name string) bool {
	// Must be lowercase letters, numbers, and hyphens only
	// Must not start or end with hyphen
	// Must not have consecutive hyphens
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return false
	}
	if strings.Contains(name, "--") {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

// isNumeric checks if a string represents a number.
func isNumeric(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isValidLocation checks if a string is in "latitude,longitude" format.
func isValidLocation(s string) bool {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return false
	}
	// Basic check - both parts should look like numbers (possibly with decimal and negative)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}
		// Remove leading minus sign
		if strings.HasPrefix(part, "-") {
			part = part[1:]
		}
		// Check for valid number format
		dotCount := 0
		for _, c := range part {
			if c == '.' {
				dotCount++
				if dotCount > 1 {
					return false
				}
			} else if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// ValidateYAMLFile validates a YAML test file from disk.
//
// Parameters:
//   - path: Path to the YAML file
//
// Returns:
//   - *ValidationResult: Validation result with errors/warnings
//   - error: File read error (nil if file was read successfully)
func ValidateYAMLFile(path string) (*ValidationResult, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return ValidateYAML(string(content)), nil
}

// GetTestDefinition parses YAML content and returns the test definition.
//
// Parameters:
//   - content: The YAML content as a string
//
// Returns:
//   - *TestDefinition: Parsed test definition
//   - error: Parse error
func GetTestDefinition(content string) (*TestDefinition, error) {
	var test TestDefinition
	if err := yaml.Unmarshal([]byte(content), &test); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	return &test, nil
}
