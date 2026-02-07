// Package schema provides CLI and YAML test schema generation.
package schema

// YAMLTestSchema contains the complete YAML test schema documentation for LLMs.
// This is embedded in the schema output to give LLMs everything they need to generate tests.
const YAMLTestSchema = `# Revyl YAML Test Schema - LLM Reference

## Purpose
This document provides a structured, machine-readable reference for generating Revyl YAML tests programmatically.

## Critical Behavior for Test Generation
**IMPORTANT**: When generating tests, DO NOT include manual navigation at the start:
- **Mobile tests**: Automatically open the app at test start
- Do NOT add an "open_app" block as the first step unless testing a specific app launch scenario

## Test Structure

` + "```yaml" + `
test:
  metadata:
    name: "test-name"           # Required: Test name
    platform: "ios"             # Required: "ios" or "android"
  build:
    name: "build-variable-name" # Required: Build variable name from Revyl
    pinned_version: "1.0.0"     # Optional: Pin to specific version
  blocks:                       # Required: At least one block
    - type: "instructions"
      step_description: "..."
` + "```" + `

## Block Types

### 1. instructions
Execute an action on the app.

` + "```yaml" + `
- type: instructions
  step_description: "Tap the login button"
` + "```" + `

### 2. validation
Assert something is visible or true.

` + "```yaml" + `
- type: validation
  step_description: "Verify the welcome message is displayed"
` + "```" + `

### 3. extraction
Extract a value from the screen into a variable.

` + "```yaml" + `
- type: extraction
  step_description: "Extract the confirmation code from the screen"
  variable_name: "confirmation-code"  # kebab-case required
` + "```" + `

### 4. manual
Execute a system-level action.

` + "```yaml" + `
# Wait for N seconds
- type: manual
  step_type: wait
  step_description: "3"  # Number of seconds

# Open app (usually not needed - app opens automatically)
- type: manual
  step_type: open_app
  step_description: "com.example.app"  # Optional bundle ID

# Kill app
- type: manual
  step_type: kill_app

# Go to home screen
- type: manual
  step_type: go_home

# Navigate to URL/deep link
- type: manual
  step_type: navigate
  step_description: "https://example.com/path"

# Set device location
- type: manual
  step_type: set_location
  step_description: "37.7749,-122.4194"  # latitude,longitude
` + "```" + `

### 5. if (Conditional)
Execute blocks conditionally.

` + "```yaml" + `
- type: if
  condition: "Is the user logged in?"
  then:
    - type: instructions
      step_description: "Tap logout"
  else:
    - type: instructions
      step_description: "Tap login"
` + "```" + `

### 6. while (Loop)
Repeat blocks while condition is true.

` + "```yaml" + `
- type: while
  condition: "Are there more items in the list?"
  body:
    - type: instructions
      step_description: "Scroll down"
    - type: instructions
      step_description: "Tap the next item"
` + "```" + `

### 7. code_execution
Execute a server-side script.

` + "```yaml" + `
- type: code_execution
  step_description: "script-uuid-here"
  variable_name: "result"  # Optional: store result in variable
` + "```" + `

## Variable System

### Syntax
Variables use double curly braces: ` + "`{{variable-name}}`" + `

### Naming Rules
- **kebab-case only**: lowercase letters, numbers, hyphens
- No spaces, underscores, or special characters
- Must not start or end with hyphen

### Usage
Variables must be defined (via extraction or code_execution) before use:

` + "```yaml" + `
blocks:
  # First: Extract the code
  - type: extraction
    step_description: "Extract the OTP code"
    variable_name: "otp-code"
  
  # Then: Use the variable
  - type: instructions
    step_description: "Enter {{otp-code}} in the verification field"
` + "```" + `

## Best Practices

### 1. Use High-Level Instructions
For complex flows with indeterminism, use descriptive instructions:

` + "```yaml" + `
# Good - handles variations
- type: instructions
  step_description: "Complete the checkout process"

# Bad - too specific, may break
- type: instructions
  step_description: "Tap button at coordinates (150, 300)"
` + "```" + `

### 2. Use Broad Validations
When exact UI elements are unknown:

` + "```yaml" + `
# Good - flexible
- type: validation
  step_description: "Verify a success message is shown"

# Bad - too specific
- type: validation
  step_description: "Verify text 'Order #12345 confirmed' is visible"
` + "```" + `

### 3. Negative Validations
Verify errors are NOT shown:

` + "```yaml" + `
- type: validation
  step_description: "Verify no error messages are displayed"
` + "```" + `

## Pre-Generation Checklist

Before generating a test, verify:

1. [ ] Login steps include credentials (or use variables)
2. [ ] All ` + "`{{variables}}`" + ` are extracted before use
3. [ ] Validations describe VISIBLE elements
4. [ ] Instructions are specific enough to be actionable
5. [ ] Test does NOT assume pre-existing app state
6. [ ] Platform is specified (ios or android)
7. [ ] Build name matches a configured build variable

## Complete Example

` + "```yaml" + `
test:
  metadata:
    name: "login-and-verify-dashboard"
    platform: "ios"
  build:
    name: "my-ios-app"
  blocks:
    # Login flow
    - type: instructions
      step_description: "Enter 'testuser@example.com' in the email field"
    
    - type: instructions
      step_description: "Enter 'password123' in the password field"
    
    - type: instructions
      step_description: "Tap the Sign In button"
    
    # Wait for dashboard to load
    - type: manual
      step_type: wait
      step_description: "2"
    
    # Verify dashboard
    - type: validation
      step_description: "Verify the dashboard screen is displayed"
    
    # Extract user info
    - type: extraction
      step_description: "Extract the user's display name"
      variable_name: "user-name"
    
    # Conditional logout
    - type: if
      condition: "Is there a logout button visible?"
      then:
        - type: instructions
          step_description: "Tap the logout button"
        - type: validation
          step_description: "Verify the login screen is displayed"
` + "```" + `
`

// GetYAMLTestSchema returns the YAML test schema documentation.
//
// Returns:
//   - string: The complete YAML test schema documentation
func GetYAMLTestSchema() string {
	return YAMLTestSchema
}

// YAMLTestSchemaJSON returns a structured JSON representation of the YAML test schema.
//
// Returns:
//   - map[string]interface{}: Structured schema for JSON output
func YAMLTestSchemaJSON() map[string]interface{} {
	return map[string]interface{}{
		"purpose": "Schema for generating YAML test files",
		"criticalBehavior": map[string]interface{}{
			"autoAppOpen":        true,
			"supportedPlatforms": []string{"ios", "android"},
			"manualStepsOnlyFor": []string{"navigate", "open_app", "kill_app", "go_home", "wait", "set_location"},
		},
		"blockTypes": map[string]interface{}{
			"instructions": map[string]interface{}{
				"description": "Execute an action on the app",
				"fields": map[string]string{
					"type":             "instructions",
					"step_description": "string (required)",
				},
			},
			"validation": map[string]interface{}{
				"description": "Assert something is visible or true",
				"fields": map[string]string{
					"type":             "validation",
					"step_description": "string (required)",
				},
			},
			"extraction": map[string]interface{}{
				"description": "Extract a value from the screen into a variable",
				"fields": map[string]string{
					"type":             "extraction",
					"step_description": "string (required)",
					"variable_name":    "string (required, kebab-case)",
				},
			},
			"manual": map[string]interface{}{
				"description": "Execute a system-level action",
				"fields": map[string]string{
					"type":             "manual",
					"step_type":        "enum (required)",
					"step_description": "string (optional, depends on step_type)",
				},
				"stepTypes": []string{"wait", "open_app", "kill_app", "go_home", "navigate", "set_location"},
				"stepDescriptionFormats": map[string]string{
					"wait":         "Number of seconds (e.g., '3')",
					"open_app":     "Bundle ID for system apps, or omit for installed app",
					"kill_app":     "Not used",
					"go_home":      "Not used",
					"navigate":     "URL or deep link",
					"set_location": "Latitude,Longitude (e.g., '37.7749,-122.4194')",
				},
			},
			"if": map[string]interface{}{
				"description": "Execute blocks conditionally",
				"fields": map[string]string{
					"type":      "if",
					"condition": "string (required)",
					"then":      "array of blocks (required)",
					"else":      "array of blocks (optional)",
				},
			},
			"while": map[string]interface{}{
				"description": "Repeat blocks while condition is true",
				"fields": map[string]string{
					"type":      "while",
					"condition": "string (required)",
					"body":      "array of blocks (required)",
				},
			},
			"code_execution": map[string]interface{}{
				"description": "Execute a server-side script",
				"fields": map[string]string{
					"type":             "code_execution",
					"step_description": "string (script UUID, required)",
					"variable_name":    "string (optional)",
				},
			},
		},
		"variableSystem": map[string]interface{}{
			"syntax":              "{{variable-name}}",
			"namingRules":         "kebab-case only, no spaces/underscores/special chars",
			"mustDefineBeforeUse": true,
		},
		"bestPractices": map[string]interface{}{
			"useHighLevelInstructions": "For complex flows with indeterminism",
			"useBroadValidations":      "When exact UI elements are unknown",
			"negativeValidations":      "Verify errors are NOT shown",
		},
		"preGenerationChecklist": []string{
			"Login steps include credentials",
			"All {{variables}} are extracted before use",
			"Validations describe VISIBLE elements",
			"Instructions are specific enough",
			"Test does NOT assume pre-existing app state",
			"Platform is specified (ios or android)",
		},
	}
}
