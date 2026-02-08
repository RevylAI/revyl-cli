// Package ui provides result rendering components.
package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// PrintTestResult prints a formatted test result.
//
// Parameters:
//   - name: Test name
//   - status: "passed" or "failed"
//   - reportURL: URL to the test report
//   - errorMsg: Error message if failed (optional)
func PrintTestResult(name, status, reportURL, errorMsg string) {
	var statusStyle lipgloss.Style
	var statusIcon string

	switch status {
	case "passed":
		statusStyle = StatusPassedStyle
		statusIcon = "✓"
	case "failed":
		statusStyle = StatusFailedStyle
		statusIcon = "✗"
	default:
		statusStyle = DimStyle
		statusIcon = "?"
	}

	// Status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, name)
	fmt.Println(statusStyle.Render(statusLine))

	// Report URL
	fmt.Printf("  %s %s\n", DimStyle.Render("Live Report:"), LinkStyle.Render(reportURL))

	// Error message if present
	if errorMsg != "" {
		fmt.Printf("  %s %s\n", DimStyle.Render("Error:"), ErrorStyle.Render(errorMsg))
	}
}

// PrintResultBox prints a boxed result summary.
//
// Parameters:
//   - status: "Passed" or "Failed"
//   - reportURL: URL to the report
//   - duration: Execution duration string
func PrintResultBox(status, reportURL, duration string) {
	var boxStyle lipgloss.Style
	var icon string

	switch status {
	case "Passed":
		boxStyle = ResultBoxPassedStyle
		icon = "✓"
	case "Failed":
		boxStyle = ResultBoxFailedStyle
		icon = "✗"
	default:
		boxStyle = BoxStyle
		icon = "•"
	}

	// Build content
	titleLine := fmt.Sprintf("%s %s", icon, status)
	if duration != "" {
		titleLine += fmt.Sprintf("  %s", DimStyle.Render(duration))
	}

	content := titleLine + "\n"
	content += fmt.Sprintf("Live Report: %s", reportURL)

	fmt.Println(boxStyle.Render(content))
}

// PrintWorkflowResult prints a workflow execution summary.
//
// Parameters:
//   - name: Workflow name
//   - passed: Number of passed tests
//   - failed: Number of failed tests
//   - total: Total number of tests
//   - reportURL: URL to the workflow report
func PrintWorkflowResult(name string, passed, failed, total int, reportURL string) {
	var statusStyle lipgloss.Style
	var statusIcon string

	if failed == 0 {
		statusStyle = StatusPassedStyle
		statusIcon = "✓"
	} else {
		statusStyle = StatusFailedStyle
		statusIcon = "✗"
	}

	// Status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, name)
	fmt.Println(statusStyle.Render(statusLine))

	// Summary
	summary := fmt.Sprintf("  %d/%d tests passed", passed, total)
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed", failed)
	}
	fmt.Println(InfoStyle.Render(summary))

	// Report URL
	fmt.Printf("  %s %s\n", DimStyle.Render("Live Report:"), LinkStyle.Render(reportURL))
}
