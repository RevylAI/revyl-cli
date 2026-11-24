package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/revyl/cli/internal/status"
)

type dynamicStderr struct{}

func (dynamicStderr) Write(p []byte) (int, error) { return os.Stderr.Write(p) }

// stderrW routes messages.go and progress.go output to os.Stderr, wrapped by crlfWriter in raw mode.
var stderrW io.Writer = dynamicStderr{}

// crlfWriter replaces every '\n' byte with "\r\n" before writing to w.
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

var quietMode bool

var debugMode bool

type OutputObserver func(level string, message string)

var outputObserver OutputObserver
var outputObserverMu sync.RWMutex

func SetOutputObserver(observer OutputObserver) {
	outputObserverMu.Lock()
	defer outputObserverMu.Unlock()
	outputObserver = observer
}

func observeOutput(level, message string) {
	outputObserverMu.RLock()
	observer := outputObserver
	outputObserverMu.RUnlock()
	if observer != nil {
		observer(level, message)
	}
}

// SetQuietMode enables or disables quiet mode for informational UI helpers.
// PrintSuccess, PrintError, PrintWarning, PrintBasicStatus, and PrintBasicWorkflowStatus still print.
func SetQuietMode(quiet bool) {
	quietMode = quiet
}

func IsQuietMode() bool {
	return quietMode
}

func SetDebugMode(debug bool) {
	debugMode = debug
}

func IsDebugMode() bool {
	return debugMode
}

func Println() {
	if quietMode {
		return
	}
	observeOutput("blank", "")
	fmt.Fprintln(stderrW)
}

func PrintSuccess(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	observeOutput("success", msg)
	fmt.Fprintln(stderrW, SuccessStyle.Render("✓ "+msg))
}

func PrintError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	observeOutput("error", msg)
	fmt.Fprintln(stderrW, ErrorStyle.Render("✗ "+msg))
}

func PrintWarning(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	observeOutput("warning", msg)
	lines := strings.Split("⚠ "+msg, "\n")
	for i, line := range lines {
		lines[i] = WarningStyle.Render(line)
	}
	fmt.Fprintln(stderrW, strings.Join(lines, "\n"))
}

func PrintInfo(format string, args ...interface{}) {
	if quietMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	observeOutput("info", msg)
	fmt.Fprintln(stderrW, InfoStyle.Render(msg))
}

func PrintDim(format string, args ...interface{}) {
	if quietMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	observeOutput("dim", msg)
	fmt.Fprintln(stderrW, DimStyle.Render(msg))
}

func PrintDebug(format string, args ...interface{}) {
	if !debugMode {
		return
	}
	msg := fmt.Sprintf(format, args...)
	observeOutput("debug", msg)
	fmt.Fprintln(stderrW, DimStyle.Render("[debug] "+msg))
}

func PrintLink(label, url string) {
	if quietMode {
		return
	}
	observeOutput("link", label+" "+url)
	fmt.Fprintf(stderrW, "%s %s\n", DimStyle.Render(label+":"), LinkStyle.Render(url))
}

func PrintBox(title, content string) {
	if quietMode {
		return
	}
	titleStyled := BoxTitleStyle.Render(title)
	box := BoxStyle.Render(titleStyled + "\n" + content)
	fmt.Fprintln(stderrW, box)
}

type PreflightVerdict int

const (
	PreflightPass PreflightVerdict = iota
	PreflightFail
	PreflightWarn
)

type PreflightRow struct {
	Key   string
	Value string
	Icon  string
}

type PreflightBoxInput struct {
	Rows        []PreflightRow
	Verdict     PreflightVerdict
	VerdictText string
	Explanation string
	FixHeader   string
	FixCommands []string
	Warnings    []string
}

// PrintPreflightBox renders the pre-flight checklist box to stderrW unless quiet mode is enabled.
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

type NextStep struct {
	Label   string
	Command string
}

// PrintNextSteps prints suggested next actions unless quiet mode is enabled or steps is empty.
// Callers must guard against JSON output mode before calling.
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

func PrintKeyValue(key, value string) {
	if quietMode {
		return
	}
	keyStyled := DimStyle.Render(fmt.Sprintf("  %-16s", key))
	fmt.Fprintf(stderrW, "%s %s\n", keyStyled, InfoStyle.Render(value))
}

type WizardSummaryItem struct {
	Title  string
	OK     bool
	Detail string
}

// PrintWizardSummary renders a boxed summary of wizard items using check and dim dash markers unless quiet.
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

func PrintVerboseStatus(statusStr string, progress int, currentStep string, completedSteps, totalSteps int, duration string) {
	if quietMode {
		return
	}

	clearLine()

	statusIcon := getStyledStatusIcon(statusStr)

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

	statusLine := fmt.Sprintf("%s %s", statusIcon, InfoStyle.Render(statusStr))

	if totalSteps > 0 {
		statusLine += fmt.Sprintf(" [%d/%d steps]", completedSteps, totalSteps)
	} else if progress > 0 {
		statusLine += fmt.Sprintf(" [%d%%]", progress)
	}

	if duration != "" {
		statusLine += DimStyle.Render(fmt.Sprintf(" (%s)", duration))
	}

	fmt.Fprintln(stderrW, statusLine)

	if currentStep != "" {
		fmt.Fprintf(stderrW, "  %s %s\n", DimStyle.Render("→"), currentStep)
	}
}

func PrintBasicStatus(statusStr string, progress int, currentStep string, completedSteps, totalSteps int) {
	clearLine()

	statusIcon := getStyledStatusIcon(statusStr)

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
		printStatusLine(fmt.Sprintf("%s %s", statusIcon, InfoStyle.Render(displayStatus)))
		return
	}

	statusLine := fmt.Sprintf("%s %s", statusIcon, statusStr)

	if totalSteps > 0 {
		statusLine += fmt.Sprintf(" [%d/%d steps]", completedSteps, totalSteps)
	} else if progress > 0 {
		statusLine += fmt.Sprintf(" [%d%%]", progress)
	}

	if currentStep != "" {
		statusLine += DimStyle.Render(fmt.Sprintf(" → %s", currentStep))
	}

	printStatusLine(statusLine)
}

var statusLineOpen bool

var lastStatusLine string

func printStatusLine(statusLine string) {
	if isTTY {
		fmt.Fprint(stderrW, statusLine)
		statusLineOpen = true
		return
	}
	if statusLine == lastStatusLine {
		return
	}
	fmt.Fprintln(stderrW, statusLine)
	lastStatusLine = statusLine
}

// EndStatusLine moves terminal output below the in-place status line, so a
// line that must stay readable, such as the Definition: line, starts at the
// left margin instead of running on from the status. No-op when no status
// line is open.
func EndStatusLine() {
	if !statusLineOpen {
		return
	}
	fmt.Fprintln(stderrW)
	statusLineOpen = false
}

type ChildTestInfo struct {
	TestName string
	Platform string
	Status   string
	Success  *bool
	Duration string
}

func PrintVerboseWorkflowStatus(statusStr string, completedTests, totalTests, passedTests, failedTests int, duration string, childTests []ChildTestInfo) {
	if quietMode {
		return
	}

	clearLine()

	statusIcon := getStyledStatusIcon(statusStr)

	statusLine := fmt.Sprintf("%s %s", statusIcon, InfoStyle.Render(statusStr))

	if totalTests > 0 {
		statusLine += fmt.Sprintf(" [%d/%d tests]", completedTests, totalTests)
	}

	if passedTests > 0 || failedTests > 0 {
		statusLine += fmt.Sprintf(" (%s passed, %s failed)",
			SuccessStyle.Render(fmt.Sprintf("%d", passedTests)),
			ErrorStyle.Render(fmt.Sprintf("%d", failedTests)))
	}

	if duration != "" {
		statusLine += DimStyle.Render(fmt.Sprintf(" (%s)", duration))
	}

	fmt.Fprintln(stderrW, statusLine)
	printChildTestLines(childTests)
}

func PrintBasicWorkflowStatus(statusStr string, completedTests, totalTests int, childTests []ChildTestInfo) {
	clearLine()

	statusIcon := getStyledStatusIcon(statusStr)

	statusLine := fmt.Sprintf("%s %s", statusIcon, statusStr)

	if totalTests > 0 {
		statusLine += fmt.Sprintf(" [%d/%d tests]", completedTests, totalTests)
	}

	if !isTTY {
		fmt.Fprintln(stderrW, statusLine)
		printChildTestLines(childTests)
		return
	}

	fmt.Fprint(stderrW, statusLine)
	statusLineOpen = true
}

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
		fmt.Fprintln(stderrW, line)
	}
}

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

type Table struct {
	Headers   []string
	Rows      [][]string
	MinWidths map[int]int
	// MaxWidths specifies maximum byte width per column index (truncating with "..." when width > 3).
	MaxWidths map[int]int
}

func NewTable(headers ...string) *Table {
	return &Table{
		Headers:   headers,
		Rows:      make([][]string, 0),
		MinWidths: make(map[int]int),
		MaxWidths: make(map[int]int),
	}
}

func (t *Table) AddRow(values ...string) {
	t.Rows = append(t.Rows, values)
}

// SetMinWidth sets the minimum byte width for column index col.
func (t *Table) SetMinWidth(col, width int) {
	t.MinWidths[col] = width
}

// SetMaxWidth sets the maximum byte width for column index col (truncating with "..." when width > 3).
func (t *Table) SetMaxWidth(col, width int) {
	t.MaxWidths[col] = width
}

func (t *Table) calculateColumnWidths() []int {
	numCols := len(t.Headers)
	widths := make([]int, numCols)

	for i, header := range t.Headers {
		widths[i] = len(header)
	}

	for _, row := range t.Rows {
		for i, val := range row {
			if i < numCols && len(val) > widths[i] {
				widths[i] = len(val)
			}
		}
	}

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

func truncateWithEllipsis(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 3 {
		return s[:width]
	}
	return s[:width-3] + "..."
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func (t *Table) Render() {
	if quietMode {
		return
	}

	if len(t.Headers) == 0 {
		return
	}

	widths := t.calculateColumnWidths()
	colGap := "  "

	var headerCells []string
	for i, header := range t.Headers {
		cell := padRight(header, widths[i])
		headerCells = append(headerCells, TableHeaderStyle.Render(cell))
	}
	fmt.Fprintln(stderrW, strings.Join(headerCells, colGap))

	totalWidth := 0
	for _, w := range widths {
		totalWidth += w
	}
	totalWidth += len(colGap) * (len(widths) - 1)
	fmt.Fprintln(stderrW, DimStyle.Render(strings.Repeat("─", totalWidth)))

	for _, row := range t.Rows {
		var cells []string
		for i := 0; i < len(t.Headers); i++ {
			val := ""
			if i < len(row) {
				val = row[i]
			}

			if max, ok := t.MaxWidths[i]; ok {
				val = truncateWithEllipsis(val, max)
			}

			cell := padRight(val, widths[i])
			cells = append(cells, TableCellStyle.Render(cell))
		}
		fmt.Fprintln(stderrW, strings.Join(cells, colGap))
	}
}

func BranchFallbackNotice(gitBranchName, defaultBranchName string) string {
	return AppBranchFallbackNotice("", gitBranchName, defaultBranchName)
}

func AppBranchFallbackNotice(appName, gitBranchName, defaultBranchName string) string {
	gitBranch := "Your Git branch"
	if gitBranchName != "" {
		gitBranch = fmt.Sprintf("Git branch %q", gitBranchName)
	}
	onApp := ""
	if appName != "" {
		onApp = fmt.Sprintf(" on %q", appName)
	}
	defaultBranch := "the app's default branch"
	if defaultBranchName != "" {
		defaultBranch += fmt.Sprintf(" %q", defaultBranchName)
	}
	return fmt.Sprintf("%s has no usable build%s; using %s.", gitBranch, onApp, defaultBranch)
}

func BranchSelectionNotice(gitBranchName, defaultBranchName string, source string, enabled *bool) string {
	return AppBranchSelectionNotice("", gitBranchName, defaultBranchName, source, enabled)
}

func AppBranchSelectionNotice(appName, gitBranchName, defaultBranchName string, source string, enabled *bool) string {
	if source == "default_branch" && enabled != nil && !*enabled {
		branch := "app default branch"
		if defaultBranchName != "" {
			branch += fmt.Sprintf(" %q", defaultBranchName)
		}
		if appName != "" {
			branch += fmt.Sprintf(" on %q", appName)
		}
		return fmt.Sprintf("Using %s because automatic Git branch detection is off.", branch)
	}
	if source == "default_branch" && gitBranchName != "" {
		return AppBranchFallbackNotice(appName, gitBranchName, defaultBranchName)
	}
	return ""
}

// NoticeWithTestDefinitionGitBranch adds to a run's branch notice the Git
// branch its test files were read from, which a build fallback doesn't change.
func NoticeWithTestDefinitionGitBranch(notice, testDefinitionGitBranchName string) string {
	if notice == "" || testDefinitionGitBranchName == "" {
		return notice
	}
	return fmt.Sprintf("%s The tests come from Git branch %q.", notice, testDefinitionGitBranchName)
}

// BranchFallbackNotices deduplicates fallback notices: Notice keys on (appID, branchID),
// while SelectionNotice appends TestDefinitionGitBranchName and keys on (appID, branchID, notice).
type BranchFallbackNotices struct {
	TestDefinitionGitBranchName string

	seen          map[[2]string]struct{}
	selectionSeen map[[3]string]struct{}
}

func (notices *BranchFallbackNotices) SelectionNotice(appID, appName, branchID, branchName, gitBranchName string, source string, enabled *bool) string {
	if appName == "" {
		appName = appID
	}
	notice := NoticeWithTestDefinitionGitBranch(AppBranchSelectionNotice(appName, gitBranchName, branchName, source, enabled), notices.TestDefinitionGitBranchName)
	if notice == "" {
		return ""
	}
	key := [3]string{appID, branchID, notice}
	if _, exists := notices.selectionSeen[key]; exists {
		return ""
	}
	if notices.selectionSeen == nil {
		notices.selectionSeen = make(map[[3]string]struct{})
	}
	notices.selectionSeen[key] = struct{}{}
	return notice
}

func (notices *BranchFallbackNotices) Notice(appID, appName, branchID, branchName, gitBranchName string, fellBack bool) string {
	if !fellBack {
		return ""
	}
	key := [2]string{appID, branchID}
	if _, exists := notices.seen[key]; exists {
		return ""
	}
	if notices.seen == nil {
		notices.seen = make(map[[2]string]struct{})
	}
	notices.seen[key] = struct{}{}
	if appName == "" {
		appName = appID
	}
	return AppBranchFallbackNotice(appName, gitBranchName, branchName)
}
