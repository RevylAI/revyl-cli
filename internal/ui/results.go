package ui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
)

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
	case "cancelled":
		statusStyle = WarningStyle
		statusIcon = "⊘"
	case "timeout":
		statusStyle = WarningStyle
		statusIcon = "⏱"
	default:
		statusStyle = DimStyle
		statusIcon = "?"
	}

	statusLine := fmt.Sprintf("%s %s", statusIcon, name)
	fmt.Fprintln(os.Stderr, statusStyle.Render(statusLine))

	fmt.Fprintf(os.Stderr, "  %s %s\n", DimStyle.Render("Live Report:"), LinkStyle.Render(reportURL))

	if errorMsg != "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", DimStyle.Render("Error:"), ErrorStyle.Render(errorMsg))
	}
}

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

	titleLine := fmt.Sprintf("%s %s", icon, status)
	if duration != "" {
		titleLine += fmt.Sprintf("  %s", DimStyle.Render(duration))
	}

	content := titleLine + "\n"
	content += fmt.Sprintf("Live Report: %s", reportURL)

	fmt.Fprintln(os.Stderr, boxStyle.Render(content))
}

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

	statusLine := fmt.Sprintf("%s %s", statusIcon, name)
	fmt.Fprintln(os.Stderr, statusStyle.Render(statusLine))

	summary := fmt.Sprintf("  %d/%d tests passed", passed, total)
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed", failed)
	}
	fmt.Fprintln(os.Stderr, InfoStyle.Render(summary))

	fmt.Fprintf(os.Stderr, "  %s %s\n", DimStyle.Render("Live Report:"), LinkStyle.Render(reportURL))
}
