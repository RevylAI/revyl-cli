// Package ui provides message printing utilities.
package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/revyl/cli/internal/status"
)

// Println prints an empty line.
func Println() {
	fmt.Println()
}

// PrintSuccess prints a success message.
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintSuccess(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(SuccessStyle.Render("✓ " + msg))
}

// PrintError prints an error message.
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(ErrorStyle.Render("✗ " + msg))
}

// PrintWarning prints a warning message.
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintWarning(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(WarningStyle.Render("⚠ " + msg))
}

// PrintInfo prints an informational message.
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintInfo(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(InfoStyle.Render(msg))
}

// PrintDim prints a dimmed message.
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintDim(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(DimStyle.Render(msg))
}

// PrintLink prints a clickable link.
//
// Parameters:
//   - label: The link label
//   - url: The URL
func PrintLink(label, url string) {
	fmt.Printf("%s %s\n", DimStyle.Render(label+":"), LinkStyle.Render(url))
}

// PrintBox prints content in a styled box.
//
// Parameters:
//   - title: Box title
//   - content: Box content
func PrintBox(title, content string) {
	titleStyled := BoxTitleStyle.Render(title)
	box := BoxStyle.Render(titleStyled + "\n" + content)
	fmt.Println(box)
}

// PrintTableHeader prints a table header row.
//
// Parameters:
//   - columns: Column names
func PrintTableHeader(columns ...string) {
	var cells []string
	for _, col := range columns {
		cells = append(cells, TableHeaderStyle.Render(col))
	}
	fmt.Println(strings.Join(cells, ""))
	// Print separator
	fmt.Println(DimStyle.Render(strings.Repeat("─", 80)))
}

// PrintTableRow prints a table data row.
//
// Parameters:
//   - values: Cell values
func PrintTableRow(values ...string) {
	var cells []string
	for _, val := range values {
		cells = append(cells, TableCellStyle.Render(val))
	}
	fmt.Println(strings.Join(cells, ""))
}

// PrintDiff prints a diff with syntax highlighting.
//
// Parameters:
//   - diff: The diff content
func PrintDiff(diff string) {
	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			fmt.Println(DiffAddStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			fmt.Println(DiffRemoveStyle.Render(line))
		default:
			fmt.Println(DiffContextStyle.Render(line))
		}
	}
}

// PrintVerboseStatus prints detailed status information during monitoring.
//
// Parameters:
//   - statusStr: Current status string (queued, running, completed, etc.)
//   - progress: Progress percentage (0-100)
//   - currentStep: Description of current step
//   - completedSteps: Number of completed steps
//   - totalSteps: Total number of steps
//   - duration: Elapsed duration string
func PrintVerboseStatus(statusStr string, progress int, currentStep string, completedSteps, totalSteps int, duration string) {
	// Clear line and print status
	fmt.Print("\r\033[K") // Clear current line

	// Get styled status icon using the shared status package
	statusIcon := getStyledStatusIcon(statusStr)

	// Build status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, InfoStyle.Render(statusStr))

	// Add progress if available
	if totalSteps > 0 {
		statusLine += fmt.Sprintf(" [%d/%d steps]", completedSteps, totalSteps)
	} else if progress > 0 {
		statusLine += fmt.Sprintf(" [%d%%]", progress)
	}

	// Add duration if available
	if duration != "" {
		statusLine += DimStyle.Render(fmt.Sprintf(" (%s)", duration))
	}

	fmt.Println(statusLine)

	// Print current step on next line if available
	if currentStep != "" {
		fmt.Printf("  %s %s\n", DimStyle.Render("→"), currentStep)
	}
}

// PrintBasicStatus prints a simple status line during monitoring (non-verbose mode).
//
// Parameters:
//   - statusStr: Current status string (queued, running, completed, etc.)
//   - progress: Progress percentage (0-100)
//   - completedSteps: Number of completed steps
//   - totalSteps: Total number of steps
func PrintBasicStatus(statusStr string, progress int, completedSteps, totalSteps int) {
	// Clear line
	fmt.Print("\r\033[K")

	// Get styled status icon using the shared status package
	statusIcon := getStyledStatusIcon(statusStr)

	// Build status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, statusStr)

	// Add progress if available
	if totalSteps > 0 {
		statusLine += fmt.Sprintf(" [%d/%d steps]", completedSteps, totalSteps)
	} else if progress > 0 {
		statusLine += fmt.Sprintf(" [%d%%]", progress)
	}

	// Print without newline so it updates in place
	fmt.Print(statusLine)
}

// PrintVerboseWorkflowStatus prints detailed workflow status information during monitoring.
//
// Parameters:
//   - statusStr: Current status string (queued, running, completed, etc.)
//   - completedTests: Number of completed tests
//   - totalTests: Total number of tests
//   - passedTests: Number of passed tests
//   - failedTests: Number of failed tests
//   - duration: Elapsed duration string
func PrintVerboseWorkflowStatus(statusStr string, completedTests, totalTests, passedTests, failedTests int, duration string) {
	// Clear line and print status
	fmt.Print("\r\033[K") // Clear current line

	// Get styled status icon using the shared status package
	statusIcon := getStyledStatusIcon(statusStr)

	// Build status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, InfoStyle.Render(statusStr))

	// Add test progress
	if totalTests > 0 {
		statusLine += fmt.Sprintf(" [%d/%d tests]", completedTests, totalTests)
	}

	// Add pass/fail counts
	if passedTests > 0 || failedTests > 0 {
		statusLine += fmt.Sprintf(" (%s passed, %s failed)",
			SuccessStyle.Render(fmt.Sprintf("%d", passedTests)),
			ErrorStyle.Render(fmt.Sprintf("%d", failedTests)))
	}

	// Add duration if available
	if duration != "" {
		statusLine += DimStyle.Render(fmt.Sprintf(" (%s)", duration))
	}

	fmt.Println(statusLine)
}

// PrintBasicWorkflowStatus prints a simple workflow status line during monitoring (non-verbose mode).
//
// Parameters:
//   - statusStr: Current status string (queued, running, completed, etc.)
//   - completedTests: Number of completed tests
//   - totalTests: Total number of tests
func PrintBasicWorkflowStatus(statusStr string, completedTests, totalTests int) {
	// Clear line
	fmt.Print("\r\033[K")

	// Get styled status icon using the shared status package
	statusIcon := getStyledStatusIcon(statusStr)

	// Build status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, statusStr)

	// Add test progress
	if totalTests > 0 {
		statusLine += fmt.Sprintf(" [%d/%d tests]", completedTests, totalTests)
	}

	// Print without newline so it updates in place
	fmt.Print(statusLine)
}

// getStyledStatusIcon returns a styled icon for the given status.
// Uses the shared status package for icon selection and applies UI styling.
//
// Parameters:
//   - statusStr: The status string
//
// Returns:
//   - string: The styled icon string
func getStyledStatusIcon(statusStr string) string {
	icon := status.StatusIcon(statusStr)
	category := status.StatusCategory(statusStr)

	switch category {
	case "dim":
		return DimStyle.Render(icon)
	case "info":
		return InfoStyle.Render(icon)
	case "success":
		return SuccessStyle.Render(icon)
	case "error":
		return ErrorStyle.Render(icon)
	case "warning":
		return WarningStyle.Render(icon)
	default:
		return DimStyle.Render(icon)
	}
}

// OpenBrowser opens a URL in the default browser.
//
// Parameters:
//   - url: The URL to open
//
// Returns:
//   - error: Any error that occurred
func OpenBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}

// Table represents a table with dynamic column widths for formatted output.
type Table struct {
	// Headers contains the column header names.
	Headers []string

	// Rows contains all data rows.
	Rows [][]string

	// MinWidths specifies minimum width per column index.
	MinWidths map[int]int

	// MaxWidths specifies maximum width per column index (truncates with ellipsis).
	MaxWidths map[int]int
}

// NewTable creates a new table with the specified headers.
//
// Parameters:
//   - headers: Column header names
//
// Returns:
//   - *Table: A new table instance
func NewTable(headers ...string) *Table {
	return &Table{
		Headers:   headers,
		Rows:      make([][]string, 0),
		MinWidths: make(map[int]int),
		MaxWidths: make(map[int]int),
	}
}

// AddRow adds a data row to the table.
//
// Parameters:
//   - values: Cell values for the row
func (t *Table) AddRow(values ...string) {
	t.Rows = append(t.Rows, values)
}

// SetMinWidth sets the minimum width for a column.
//
// Parameters:
//   - col: Column index (0-based)
//   - width: Minimum width in characters
func (t *Table) SetMinWidth(col, width int) {
	t.MinWidths[col] = width
}

// SetMaxWidth sets the maximum width for a column.
// Values exceeding this width will be truncated with ellipsis.
//
// Parameters:
//   - col: Column index (0-based)
//   - width: Maximum width in characters
func (t *Table) SetMaxWidth(col, width int) {
	t.MaxWidths[col] = width
}

// calculateColumnWidths computes the optimal width for each column.
//
// Returns:
//   - []int: Width for each column
func (t *Table) calculateColumnWidths() []int {
	numCols := len(t.Headers)
	widths := make([]int, numCols)

	// Start with header widths
	for i, header := range t.Headers {
		widths[i] = len(header)
	}

	// Check all row values
	for _, row := range t.Rows {
		for i, val := range row {
			if i < numCols && len(val) > widths[i] {
				widths[i] = len(val)
			}
		}
	}

	// Apply min/max constraints
	for i := range widths {
		if min, ok := t.MinWidths[i]; ok && widths[i] < min {
			widths[i] = min
		}
		if max, ok := t.MaxWidths[i]; ok && widths[i] > max {
			widths[i] = max
		}
	}

	return widths
}

// truncateWithEllipsis truncates a string to the specified width with ellipsis.
//
// Parameters:
//   - s: String to truncate
//   - width: Maximum width
//
// Returns:
//   - string: Truncated string with ellipsis if needed
func truncateWithEllipsis(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 3 {
		return s[:width]
	}
	return s[:width-3] + "..."
}

// padRight pads a string to the specified width with spaces.
//
// Parameters:
//   - s: String to pad
//   - width: Target width
//
// Returns:
//   - string: Padded string
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// Render prints the table with calculated column widths.
// Headers are styled with TableHeaderStyle, cells with TableCellStyle.
func (t *Table) Render() {
	if len(t.Headers) == 0 {
		return
	}

	widths := t.calculateColumnWidths()
	colGap := "  " // Gap between columns

	// Print header row
	var headerCells []string
	for i, header := range t.Headers {
		cell := padRight(header, widths[i])
		headerCells = append(headerCells, TableHeaderStyle.Render(cell))
	}
	fmt.Println(strings.Join(headerCells, colGap))

	// Print separator
	totalWidth := 0
	for _, w := range widths {
		totalWidth += w
	}
	totalWidth += len(colGap) * (len(widths) - 1)
	fmt.Println(DimStyle.Render(strings.Repeat("─", totalWidth)))

	// Print data rows
	for _, row := range t.Rows {
		var cells []string
		for i := 0; i < len(t.Headers); i++ {
			val := ""
			if i < len(row) {
				val = row[i]
			}

			// Apply max width truncation
			if max, ok := t.MaxWidths[i]; ok {
				val = truncateWithEllipsis(val, max)
			}

			cell := padRight(val, widths[i])
			cells = append(cells, TableCellStyle.Render(cell))
		}
		fmt.Println(strings.Join(cells, colGap))
	}
}
