// Package ui provides message printing utilities.
package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/revyl/cli/internal/status"
)

// dynamicStderr delegates to the current os.Stderr at write time so that
// test-time reassignment of os.Stderr (e.g. to a pipe) is respected.
type dynamicStderr struct{}

func (dynamicStderr) Write(p []byte) (int, error) { return os.Stderr.Write(p) }

// stderrW is the writer used for all UI output. Normally it resolves
// os.Stderr dynamically via dynamicStderr. When the terminal is placed in
// raw mode (e.g. for single-key dev-loop keybinds), it wraps os.Stderr with
// crlfWriter so that \n is emitted as \r\n — the kernel's OPOST translation
// is disabled in raw mode.
var stderrW io.Writer = dynamicStderr{}

// crlfWriter translates bare \n bytes to \r\n, compensating for the disabled
// OPOST/ONLCR output processing in raw terminal mode.
type crlfWriter struct{ w io.Writer }

func (c *crlfWriter) Write(p []byte) (int, error) {
	if bytes.IndexByte(p, '\n') < 0 {
		return c.w.Write(p)
	}
	_, err := c.w.Write(bytes.ReplaceAll(p, []byte{'\n'}, []byte{'\r', '\n'}))
	return len(p), err
}

// SetRawMode configures UI output for raw terminal mode. When enabled, all
// newlines are written as \r\n since the kernel no longer performs OPOST
// translation. Call with true after term.MakeRaw and false before term.Restore.
func SetRawMode(enabled bool) {
	if enabled {
		stderrW = &crlfWriter{w: os.Stderr}
	} else {
		stderrW = dynamicStderr{}
	}
}

// quietMode controls whether non-essential output is suppressed.
// Set via SetQuietMode() based on the --quiet flag.
var quietMode bool

// debugMode controls whether debug output is printed.
// Set via SetDebugMode() based on the --debug flag.
var debugMode bool

// SetQuietMode enables or disables quiet mode.
// When enabled, informational messages are suppressed; only errors and final results are shown.
//
// Parameters:
//   - quiet: true to enable quiet mode, false to disable
func SetQuietMode(quiet bool) {
	quietMode = quiet
}

// IsQuietMode returns whether quiet mode is enabled.
//
// Returns:
//   - bool: true if quiet mode is enabled
func IsQuietMode() bool {
	return quietMode
}

// SetDebugMode enables or disables debug mode.
// When enabled, debug messages are printed to help diagnose issues.
//
// Parameters:
//   - debug: true to enable debug mode, false to disable
func SetDebugMode(debug bool) {
	debugMode = debug
}

// IsDebugMode returns whether debug mode is enabled.
//
// Returns:
//   - bool: true if debug mode is enabled
func IsDebugMode() bool {
	return debugMode
}

// Println prints an empty line.
// Respects quiet mode - suppressed when quiet.
func Println() {
	if quietMode {
		return
	}
	fmt.Fprintln(stderrW)
}

// PrintSuccess prints a success message.
// Always printed, even in quiet mode (considered essential output).
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintSuccess(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(stderrW, SuccessStyle.Render("✓ "+msg))
}

// PrintError prints an error message.
// Always printed, even in quiet mode (considered essential output).
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(stderrW, ErrorStyle.Render("✗ "+msg))
}

// PrintWarning prints a warning message.
// Always printed, even in quiet mode (considered essential output).
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintWarning(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(stderrW, WarningStyle.Render("⚠ "+msg))
}

// PrintInfo prints an informational message.
// Respects quiet mode - suppressed when quiet.
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintInfo(format string, args ...interface{}) {
	if quietMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(stderrW, InfoStyle.Render(msg))
}

// PrintDim prints a dimmed message.
// Respects quiet mode - suppressed when quiet.
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintDim(format string, args ...interface{}) {
	if quietMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(stderrW, DimStyle.Render(msg))
}

// PrintDebug prints a debug message.
// Only printed when debug mode is enabled via --debug flag.
//
// Parameters:
//   - format: Printf format string
//   - args: Printf arguments
func PrintDebug(format string, args ...interface{}) {
	if !debugMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(stderrW, DimStyle.Render("[debug] "+msg))
}

// PrintLink prints a clickable link.
// Respects quiet mode - suppressed when quiet.
//
// Parameters:
//   - label: The link label
//   - url: The URL
func PrintLink(label, url string) {
	if quietMode {
		return
	}
	fmt.Fprintf(stderrW, "%s %s\n", DimStyle.Render(label+":"), LinkStyle.Render(url))
}

// PrintBox prints content in a styled box.
// Respects quiet mode - suppressed when quiet.
//
// Parameters:
//   - title: Box title
//   - content: Box content
func PrintBox(title, content string) {
	if quietMode {
		return
	}
	titleStyled := BoxTitleStyle.Render(title)
	box := BoxStyle.Render(titleStyled + "\n" + content)
	fmt.Fprintln(stderrW, box)
}

// PreflightVerdict represents the tri-state compatibility result for the pre-flight box.
type PreflightVerdict int

const (
	// PreflightPass means the build is compatible with hot reload.
	PreflightPass PreflightVerdict = iota
	// PreflightFail means the build is NOT compatible with hot reload.
	PreflightFail
	// PreflightWarn means compatibility could not be determined.
	PreflightWarn
)

// PreflightRow is a single key-value row in the pre-flight box.
//
// Fields:
//   - Key: the label (e.g. "Provider:", "Build type:")
//   - Value: the display value
//   - Icon: optional prefix icon (e.g. "✓", "✗", "⚠")
type PreflightRow struct {
	Key   string
	Value string
	Icon  string
}

// PreflightBoxInput holds all data needed to render the pre-flight box.
//
// Fields:
//   - Rows: ordered key-value rows for the info section
//   - Verdict: pass/fail/warn determines the box border color and verdict line
//   - VerdictText: the human-readable verdict message
//   - Explanation: multi-line explanation shown only when verdict is Fail or Warn
//   - FixHeader: header for the fix section (e.g. "To fix, upload a dev client build:")
//   - FixCommands: actionable commands the user can run
//   - Warnings: additional non-fatal warnings shown below the verdict
type PreflightBoxInput struct {
	Rows        []PreflightRow
	Verdict     PreflightVerdict
	VerdictText string
	Explanation string
	FixHeader   string
	FixCommands []string
	Warnings    []string
}

// PrintPreflightBox renders a structured pre-flight checklist box.
// Uses green border for pass, red for fail, purple for warn/unknown.
// Always printed (not suppressed by quiet mode) since it contains critical compatibility info.
//
// Parameters:
//   - input: the complete pre-flight box data
func PrintPreflightBox(input PreflightBoxInput) {
	if quietMode {
		return
	}

	var lines []string

	for _, row := range input.Rows {
		keyStyled := DimStyle.Render(fmt.Sprintf("  %-16s", row.Key))
		valueText := row.Value
		if row.Icon != "" {
			valueText = row.Icon + " " + valueText
		}
		lines = append(lines, fmt.Sprintf("%s %s", keyStyled, InfoStyle.Render(valueText)))
	}

	lines = append(lines, "")

	var verdictIcon, verdictLine string
	switch input.Verdict {
	case PreflightPass:
		verdictIcon = SuccessStyle.Render("✓")
		verdictLine = fmt.Sprintf("  %s %s", verdictIcon, SuccessStyle.Render(input.VerdictText))
	case PreflightFail:
		verdictIcon = ErrorStyle.Render("✗")
		verdictLine = fmt.Sprintf("  %s %s", verdictIcon, ErrorStyle.Render(input.VerdictText))
	case PreflightWarn:
		verdictIcon = WarningStyle.Render("⚠")
		verdictLine = fmt.Sprintf("  %s %s", verdictIcon, WarningStyle.Render(input.VerdictText))
	}
	lines = append(lines, verdictLine)

	if input.Explanation != "" && input.Verdict != PreflightPass {
		lines = append(lines, "")
		for _, expLine := range strings.Split(input.Explanation, "\n") {
			lines = append(lines, "  "+DimStyle.Render(expLine))
		}
	}

	if len(input.FixCommands) > 0 && input.Verdict == PreflightFail {
		lines = append(lines, "")
		if input.FixHeader != "" {
			lines = append(lines, "  "+InfoStyle.Render(input.FixHeader))
		}
		for _, cmd := range input.FixCommands {
			lines = append(lines, "    "+AccentStyle.Render(cmd))
		}
	}

	for _, w := range input.Warnings {
		lines = append(lines, "  "+WarningStyle.Render("⚠ "+w))
	}

	content := strings.Join(lines, "\n")
	title := BoxTitleStyle.Render("Dev Loop Pre-flight")

	var boxStyle lipgloss.Style
	switch input.Verdict {
	case PreflightPass:
		boxStyle = ResultBoxPassedStyle
	case PreflightFail:
		boxStyle = ResultBoxFailedStyle
	default:
		boxStyle = BoxStyle
	}

	fmt.Fprintln(stderrW, boxStyle.Render(title+"\n"+content))
}

// NextStep represents a single suggested next action for the user.
//
// Fields:
//   - Label: A short description of the action (e.g., "Run your test")
//   - Command: The CLI command to execute (e.g., "revyl test run my-test")
type NextStep struct {
	Label   string
	Command string
}

// PrintNextSteps prints a styled list of suggested next actions.
// Respects quiet mode - suppressed when quiet. Callers should also
// guard against JSON output mode before calling.
//
// Parameters:
//   - steps: Ordered list of suggested next actions (max 2-3 recommended)
func PrintNextSteps(steps []NextStep) {
	if quietMode || len(steps) == 0 {
		return
	}
	Println()
	PrintDim("Next:")
	for _, s := range steps {
		fmt.Fprintf(stderrW, "  %s  %s\n",
			DimStyle.Render(s.Label),
			InfoStyle.Render(s.Command))
	}
}

// PrintStepHeader prints a prominent step header with horizontal separators.
// Used by wizard flows (e.g. revyl init) to visually delineate each step.
// Respects quiet mode - suppressed when quiet.
//
// Parameters:
//   - step: The current step number (1-based)
//   - total: The total number of steps
//   - title: The step title (e.g. "Project Setup")
func PrintStepHeader(step, total int, title string) {
	if quietMode {
		return
	}
	separator := DimStyle.Render("─────────────────────────────────────────────────")
	stepNum := AccentStyle.Render(fmt.Sprintf("Step %d/%d", step, total))
	titleStyled := TitleStyle.Render(title)
	fmt.Fprintln(stderrW)
	fmt.Fprintln(stderrW, separator)
	fmt.Fprintf(stderrW, "%s  %s\n", stepNum, titleStyled)
	fmt.Fprintln(stderrW, separator)
	fmt.Fprintln(stderrW)
}

// PrintSectionHeader renders a titled section divider without step numbers.
// Used by lightweight flows (e.g. default `revyl init`) where numbered wizard
// steps would be misleading. Respects quiet mode.
//
// Parameters:
//   - title: The section title (e.g. "Project Setup")
func PrintSectionHeader(title string) {
	if quietMode {
		return
	}
	separator := DimStyle.Render("─────────────────────────────────────────────────")
	titleStyled := TitleStyle.Render(title)
	fmt.Fprintln(stderrW)
	fmt.Fprintln(stderrW, separator)
	fmt.Fprintln(stderrW, titleStyled)
	fmt.Fprintln(stderrW, separator)
	fmt.Fprintln(stderrW)
}

// PrintKeyValue prints a key-value pair with aligned formatting.
// Useful for structured output like build details and configuration summaries.
// Respects quiet mode - suppressed when quiet.
//
// Parameters:
//   - key: The label (e.g. "App:", "Build Version:")
//   - value: The value to display
func PrintKeyValue(key, value string) {
	if quietMode {
		return
	}
	keyStyled := DimStyle.Render(fmt.Sprintf("  %-16s", key))
	fmt.Fprintf(stderrW, "%s %s\n", keyStyled, InfoStyle.Render(value))
}

// WizardSummaryItem represents a single step result in the final wizard summary.
//
// Fields:
//   - Title: Short description of the step (e.g. "Authentication")
//   - OK: Whether the step completed successfully
//   - Detail: Optional detail string (e.g. app name, test name)
type WizardSummaryItem struct {
	Title  string
	OK     bool
	Detail string
}

// PrintWizardSummary prints a boxed summary of all wizard steps at the end of the flow.
// Each step is shown with a check or cross icon and optional detail.
// Respects quiet mode - suppressed when quiet.
//
// Parameters:
//   - items: Ordered list of step results to display
func PrintWizardSummary(items []WizardSummaryItem) {
	if quietMode || len(items) == 0 {
		return
	}

	var lines []string
	for _, item := range items {
		var icon, line string
		if item.OK {
			icon = SuccessStyle.Render("✓")
		} else {
			icon = DimStyle.Render("–")
		}
		if item.Detail != "" {
			line = fmt.Sprintf("%s %s  %s", icon, InfoStyle.Render(item.Title), DimStyle.Render(item.Detail))
		} else {
			line = fmt.Sprintf("%s %s", icon, InfoStyle.Render(item.Title))
		}
		lines = append(lines, line)
	}

	content := strings.Join(lines, "\n")
	box := BoxStyle.Render(BoxTitleStyle.Render("Setup Complete") + "\n" + content)
	fmt.Fprintln(stderrW, box)
}

// PrintDiff prints a diff with syntax highlighting.
// Respects quiet mode - suppressed when quiet.
//
// Parameters:
//   - diff: The diff content
func PrintDiff(diff string) {
	if quietMode {
		return
	}
	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			fmt.Fprintln(stderrW, DiffAddStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			fmt.Fprintln(stderrW, DiffRemoveStyle.Render(line))
		default:
			fmt.Fprintln(stderrW, DiffContextStyle.Render(line))
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
	if quietMode {
		return
	}

	// Clear line and print status
	clearLine() // Clear current line

	// Get styled status icon using the shared status package
	statusIcon := getStyledStatusIcon(statusStr)

	// Special handling for non-running phases (device lifecycle + terminal states)
	statusLower := strings.ToLower(statusStr)
	var displayStatus string
	switch statusLower {
	case "starting", "queued":
		displayStatus = "Setting up device..."
	case "verifying":
		displayStatus = "Verifying results..."
	case "stopping":
		displayStatus = "Stopping device..."
	case "cancelled":
		displayStatus = "Test cancelled"
	case "timeout":
		displayStatus = "Test timed out"
	}
	if displayStatus != "" {
		statusLine := fmt.Sprintf("%s %s", statusIcon, InfoStyle.Render(displayStatus))
		if duration != "" {
			statusLine += DimStyle.Render(fmt.Sprintf(" (%s)", duration))
		}
		fmt.Fprintln(stderrW, statusLine)
		return
	}

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

	fmt.Fprintln(stderrW, statusLine)

	// Print current step on next line if available
	if currentStep != "" {
		fmt.Fprintf(stderrW, "  %s %s\n", DimStyle.Render("→"), currentStep)
	}
}

// PrintBasicStatus prints a simple status line during monitoring (non-verbose mode).
//
// Parameters:
//   - statusStr: Current status string (queued, running, completed, etc.)
//   - progress: Progress percentage (0-100)
//   - currentStep: Description of current step being executed
//   - completedSteps: Number of completed steps
//   - totalSteps: Total number of steps
func PrintBasicStatus(statusStr string, progress int, currentStep string, completedSteps, totalSteps int) {
	// Clear line
	clearLine()

	// Get styled status icon using the shared status package
	statusIcon := getStyledStatusIcon(statusStr)

	// Special handling for non-running phases (device lifecycle + terminal states)
	statusLower := strings.ToLower(statusStr)
	var displayStatus string
	switch statusLower {
	case "starting", "queued":
		displayStatus = "Setting up device..."
	case "verifying":
		displayStatus = "Verifying results..."
	case "stopping":
		displayStatus = "Stopping device..."
	case "cancelled":
		displayStatus = "Test cancelled"
	case "timeout":
		displayStatus = "Test timed out"
	}
	if displayStatus != "" {
		statusLine := fmt.Sprintf("%s %s", statusIcon, InfoStyle.Render(displayStatus))
		fmt.Fprint(stderrW, statusLine)
		return
	}

	// Build status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, statusStr)

	// Add progress if available
	if totalSteps > 0 {
		statusLine += fmt.Sprintf(" [%d/%d steps]", completedSteps, totalSteps)
	} else if progress > 0 {
		statusLine += fmt.Sprintf(" [%d%%]", progress)
	}

	// Show current step description inline
	if currentStep != "" {
		statusLine += DimStyle.Render(fmt.Sprintf(" → %s", currentStep))
	}

	// Print without newline so it updates in place
	fmt.Fprint(stderrW, statusLine)
}

// ChildTestInfo contains per-test progress data for workflow status display.
type ChildTestInfo struct {
	TestName string
	Platform string
	Status   string
	Success  *bool
	Duration string
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
//   - childTests: Per-test progress details (may be nil)
func PrintVerboseWorkflowStatus(statusStr string, completedTests, totalTests, passedTests, failedTests int, duration string, childTests []ChildTestInfo) {
	if quietMode {
		return
	}

	// Clear line and print status
	clearLine() // Clear current line

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

	fmt.Fprintln(stderrW, statusLine)
	printChildTestLines(childTests)
}

// PrintBasicWorkflowStatus prints a simple workflow status line during monitoring (non-verbose mode).
//
// Parameters:
//   - statusStr: Current status string (queued, running, completed, etc.)
//   - completedTests: Number of completed tests
//   - totalTests: Total number of tests
//   - childTests: Per-test progress details (may be nil)
func PrintBasicWorkflowStatus(statusStr string, completedTests, totalTests int, childTests []ChildTestInfo) {
	// Clear line
	clearLine()

	// Get styled status icon using the shared status package
	statusIcon := getStyledStatusIcon(statusStr)

	// Build status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, statusStr)

	// Add test progress
	if totalTests > 0 {
		statusLine += fmt.Sprintf(" [%d/%d tests]", completedTests, totalTests)
	}

	// In CI/non-TTY logs, emit line-by-line updates so progress is visible.
	if !isTTY {
		fmt.Fprintln(stderrW, statusLine)
		printChildTestLines(childTests)
		return
	}

	// Print without newline so it updates in place
	fmt.Fprint(stderrW, statusLine)
}

// printChildTestLines renders per-test progress lines below the aggregate status.
func printChildTestLines(children []ChildTestInfo) {
	for _, ct := range children {
		icon := "  ▶"
		result := ct.Status
		if ct.Success != nil {
			if *ct.Success {
				icon = "  ✓"
				result = "passed"
			} else {
				icon = "  ✗"
				result = "failed"
			}
		}
		line := fmt.Sprintf("%s %s", icon, ct.TestName)
		if ct.Platform != "" {
			line += fmt.Sprintf(" (%s)", ct.Platform)
		}
		line += fmt.Sprintf("  %s", result)
		if ct.Duration != "" {
			line += fmt.Sprintf("  %s", ct.Duration)
		}
		fmt.Println(line)
	}
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

// OpenBrowser opens a URL in the default browser after validating it uses
// http or https scheme. Non-HTTP schemes are rejected to prevent OS handler abuse.
//
// Parameters:
//   - rawURL: The URL to open (must be http:// or https://)
//
// Returns:
//   - error: If the scheme is not http/https or the platform is unsupported
func OpenBrowser(rawURL string) error {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return fmt.Errorf("refusing to open non-HTTP URL: %s", rawURL)
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
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
// Respects quiet mode - suppressed when quiet.
// Headers are styled with TableHeaderStyle, cells with TableCellStyle.
func (t *Table) Render() {
	if quietMode {
		return
	}

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
	fmt.Fprintln(stderrW, strings.Join(headerCells, colGap))

	// Print separator
	totalWidth := 0
	for _, w := range widths {
		totalWidth += w
	}
	totalWidth += len(colGap) * (len(widths) - 1)
	fmt.Fprintln(stderrW, DimStyle.Render(strings.Repeat("─", totalWidth)))

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
		fmt.Fprintln(stderrW, strings.Join(cells, colGap))
	}
}
