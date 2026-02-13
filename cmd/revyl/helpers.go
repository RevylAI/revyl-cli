// Package main provides shared helper functions for CLI commands.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// maxResourceNameLen is the maximum allowed length for test/workflow names.
const maxResourceNameLen = 128

// reservedNames are subcommand names that cannot be used as test/workflow names
// because they collide with Cobra's command resolution.
var reservedNames = map[string]bool{
	"run": true, "create": true, "delete": true, "open": true, "cancel": true,
	"list": true, "remote": true, "push": true, "pull": true, "diff": true,
	"validate": true, "setup": true, "help": true,
	"status": true, "history": true, "report": true, "share": true,
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

// capitalizeFirst uppercases the first character of a string.
// Replaces deprecated strings.Title for single-word status values.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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

// resolveTestID resolves a test name or ID to a test UUID and display name.
// Resolution chain: config alias → UUID format check → API search by name.
//
// Parameters:
//   - ctx: Context for cancellation
//   - nameOrID: The test name, alias, or UUID
//   - cfg: The project config (may be nil)
//   - client: The API client
//
// Returns:
//   - testID: The resolved test UUID
//   - testName: The display name (alias or API name)
//   - error: Any error that occurred
func resolveTestID(ctx context.Context, nameOrID string, cfg *config.ProjectConfig, client *api.Client) (string, string, error) {
	// 1. Try config alias
	if cfg != nil && cfg.Tests != nil {
		if id, ok := cfg.Tests[nameOrID]; ok {
			return id, nameOrID, nil
		}
	}

	// 2. Check if it looks like a UUID
	if looksLikeUUID(nameOrID) {
		return nameOrID, "", nil
	}

	// 3. Search via API by name
	ui.StartSpinner("Searching for test...")
	testsResp, err := client.ListOrgTests(ctx, 200, 0)
	ui.StopSpinner()

	if err != nil {
		return "", "", fmt.Errorf("failed to search for test: %w", err)
	}

	for _, t := range testsResp.Tests {
		if t.Name == nameOrID {
			return t.ID, t.Name, nil
		}
	}

	// Not found - build helpful error message
	var availableTests []string
	if cfg != nil && cfg.Tests != nil {
		for name := range cfg.Tests {
			availableTests = append(availableTests, name)
		}
	}
	errMsg := fmt.Sprintf("test '%s' not found", nameOrID)
	if len(availableTests) > 0 {
		errMsg += fmt.Sprintf(". Available tests: %v", availableTests)
	}
	errMsg += "\n\nHint: Run 'revyl test remote' to see all available tests."
	return "", "", fmt.Errorf("%s", errMsg)
}

// resolveLatestTaskID resolves the latest execution task ID for a test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - client: The API client
//   - testID: The test UUID
//
// Returns:
//   - taskID: The latest task/execution ID
//   - error: Any error that occurred
func resolveLatestTaskID(ctx context.Context, client *api.Client, testID string) (string, error) {
	history, err := client.GetTestEnhancedHistory(ctx, testID, 1, 0)
	if err != nil {
		return "", fmt.Errorf("failed to get test history: %w", err)
	}

	if len(history.Items) == 0 {
		return "", fmt.Errorf("no executions found for this test")
	}

	item := history.Items[0]
	if item.EnhancedTask != nil {
		return item.EnhancedTask.ID, nil
	}
	return item.ID, nil
}

// formatDurationSecs formats a duration in seconds to a compact human-readable string.
//
// Parameters:
//   - seconds: The duration in seconds
//
// Returns:
//   - string: Formatted duration (e.g., "<1s", "32s", "2m 30s", "1h 5m")
func formatDurationSecs(seconds float64) string {
	if seconds < 1 {
		return "<1s"
	}
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	if h > 0 {
		if m > 0 {
			return fmt.Sprintf("%dh %dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
	if m > 0 {
		if s > 0 {
			return fmt.Sprintf("%dm %ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", s)
}

// formatAbsoluteTime parses an ISO 8601 timestamp and returns a short local time string.
//
// Parameters:
//   - isoTimestamp: An ISO 8601 timestamp string
//
// Returns:
//   - string: Formatted time (e.g., "Jan 15, 14:30") or the original string if parsing fails
func formatAbsoluteTime(isoTimestamp string) string {
	if isoTimestamp == "" {
		return "-"
	}

	// Try common ISO 8601 formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.000000",
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		t, err := time.Parse(format, isoTimestamp)
		if err == nil {
			return t.Local().Format("Jan 02, 15:04")
		}
	}

	// Fallback: return as-is but truncated
	if len(isoTimestamp) > 16 {
		return isoTimestamp[:16]
	}
	return isoTimestamp
}

// loadConfigAndClient is a common helper to authenticate, load config, and create an API client.
//
// Parameters:
//   - devMode: Whether dev mode is enabled
//
// Returns:
//   - apiKey: The API key
//   - cfg: The project config (may be nil if not initialized)
//   - client: The API client
//   - error: Any error that occurred
var loadConfigAndClient = func(devMode bool) (string, *config.ProjectConfig, *api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return "", nil, nil, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	client := api.NewClientWithDevMode(apiKey, devMode)
	return apiKey, cfg, client, nil
}

// resolveWorkflowID resolves a workflow name or ID to a workflow UUID and display name.
// Resolution chain: config alias → UUID format check → API search by name.
func resolveWorkflowID(ctx context.Context, nameOrID string, cfg *config.ProjectConfig, client *api.Client) (string, string, error) {
	// 1. Try config alias
	if cfg != nil && cfg.Workflows != nil {
		if id, ok := cfg.Workflows[nameOrID]; ok {
			return id, nameOrID, nil
		}
	}

	// 2. Check if it looks like a UUID
	if looksLikeUUID(nameOrID) {
		return nameOrID, "", nil
	}

	// 3. Search via API by name
	ui.StartSpinner("Searching for workflow...")
	wfResp, err := client.ListWorkflows(ctx)
	ui.StopSpinner()

	if err != nil {
		return "", "", fmt.Errorf("failed to search for workflow: %w", err)
	}

	for _, w := range wfResp.Workflows {
		if w.Name == nameOrID {
			return w.ID, w.Name, nil
		}
	}

	// Not found - build helpful error message
	var availableWorkflows []string
	if cfg != nil && cfg.Workflows != nil {
		for name := range cfg.Workflows {
			availableWorkflows = append(availableWorkflows, name)
		}
	}
	errMsg := fmt.Sprintf("workflow '%s' not found", nameOrID)
	if len(availableWorkflows) > 0 {
		errMsg += fmt.Sprintf(". Available workflows: %v", availableWorkflows)
	}
	errMsg += "\n\nHint: Run 'revyl workflow list' to see all available workflows."
	return "", "", fmt.Errorf("%s", errMsg)
}

// resolveLatestWorkflowTaskID resolves the latest execution task ID for a workflow.
func resolveLatestWorkflowTaskID(ctx context.Context, client *api.Client, workflowID string) (string, error) {
	history, err := client.GetWorkflowHistory(ctx, workflowID, 1, 0)
	if err != nil {
		return "", fmt.Errorf("failed to get workflow history: %w", err)
	}

	if len(history.Executions) == 0 {
		return "", fmt.Errorf("no executions found for this workflow")
	}

	execID := history.Executions[0].ExecutionID
	if execID == "" {
		return "", fmt.Errorf("execution ID not available in history response")
	}
	return execID, nil
}
