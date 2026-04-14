// Package build provides build execution and artifact management utilities.
package build

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
)

// Runner executes build commands in a specified working directory.
type Runner struct {
	// workDir is the working directory for build commands.
	workDir string

	// Interactive, when true, connects stdin/stdout/stderr directly to the
	// terminal instead of piping. This allows interactive prompts (e.g.
	// Apple credential login during EAS builds) to render and accept input.
	// The onOutput callback is not called in interactive mode.
	Interactive bool

	// FilterOutput, when true, forces piped mode and filters build output to
	// show only significant build phases (Compiling, Linking, Signing, errors).
	// Verbose compiler invocations, destination lists, and cd commands are hidden.
	// Use --debug to disable filtering and see full raw output.
	FilterOutput bool
}

// NewRunner creates a new build runner.
//
// Parameters:
//   - workDir: The working directory for build commands
//
// Returns:
//   - *Runner: A new Runner instance
func NewRunner(workDir string) *Runner {
	return &Runner{workDir: workDir}
}

// Run executes a build command and streams output to the callback.
//
// SECURITY: The command string is passed to /bin/sh -c and can contain arbitrary
// shell operators. It originates from the project's .revyl/config.yaml. This is
// intentional (build commands inherently need shell execution), but means that
// cloning and building an untrusted repository grants that repo full shell access
// as the current user. Treat .revyl/config.yaml with the same trust level as a
// Makefile or package.json script.
//
// Parameters:
//   - command: The build command to execute (can include shell operators)
//   - onOutput: Callback function called for each line of output
//
// Returns:
//   - error: Any error that occurred during execution
func (r *Runner) Run(command string, onOutput func(line string)) error {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = r.workDir

	// In interactive mode (TTY detected), connect stdin/stdout/stderr
	// directly so interactive prompts (Apple login, etc.) work properly.
	// We lose the [prefix] output tagging but gain full prompt support.
	// FilterOutput overrides this to force piped mode for clean output.
	isTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if r.Interactive && isTTY && !r.FilterOutput {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("command failed: %w", err)
		}
		return nil
	}

	// Non-interactive: pipe stdout/stderr for prefixed streaming output.
	if isTTY {
		cmd.Stdin = os.Stdin
	}

	// Create pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	emit := onOutput
	if r.FilterOutput && onOutput != nil {
		emit = func(line string) {
			if shouldShowBuildLine(line) {
				onOutput(shortenBuildLine(strings.TrimLeft(line, " \t")))
			}
		}
	}

	var mu sync.Mutex

	// Stream stdout
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Split(scanCRLF)
		for scanner.Scan() {
			if emit != nil {
				mu.Lock()
				emit(scanner.Text())
				mu.Unlock()
			}
		}
	}()

	// Stream stderr and capture for error detection
	var stderrLines []string
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Split(scanCRLF)
		for scanner.Scan() {
			line := scanner.Text()
			mu.Lock()
			stderrLines = append(stderrLines, line)
			if emit != nil {
				emit(line)
			}
			mu.Unlock()
		}
	}()

	// Wait for command to complete
	cmdErr := cmd.Wait()
	// Wait for goroutines to finish reading all output before accessing stderrLines
	wg.Wait()

	if cmdErr != nil {
		stderrOutput := strings.Join(stderrLines, "\n")
		if toolErr := parseBuildToolError(stderrOutput, command); toolErr != nil {
			return toolErr
		}
		return fmt.Errorf("command failed: %w", cmdErr)
	}

	return nil
}

// scanCRLF is a bufio.SplitFunc that splits on \r\n, \r, or \n. This handles
// xcodebuild-style progress output that uses bare \r to update in-place,
// preventing multiple progress updates from being concatenated into one token.
func scanCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' {
			return i + 1, data[:i], nil
		}
		if b == '\r' {
			if i+1 < len(data) && data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// shouldShowBuildLine returns true if a build output line is significant enough
// to display when FilterOutput is enabled. Hides verbose compiler invocations,
// destination lists, and tool paths; shows build phase names, warnings, errors,
// and EAS/Expo lifecycle milestones.
func shouldShowBuildLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}

	showPrefixes := []string{
		// Xcode build phases
		"Compiling", "Linking", "Signing",
		"CodeSign", "Ld ", "CompileC",
		"SwiftCompile", "SwiftEmitModule",
		"Build Succeeded", "Build Failed",
		"BUILD SUCCEEDED", "BUILD FAILED",
		"BUILD SUCCESSFUL", "Build completed",
		"** BUILD",
		"warning:", "error:",
		"note: Building targets",
		// Gradle phases
		":app:", "> Task :",
		// EAS / Expo lifecycle milestones
		"Creating build",
		"Resolving",
		"Installing dependencies",
		"Running prebuild",
		"Generating sourcemaps",
		"Bundling",
		"Building",
		"Archiving",
		"Packaging",
		"Downloading",
		"Installing pods",
		"Running pod install",
	}
	for _, p := range showPrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}

	if strings.Contains(trimmed, "warning:") || strings.Contains(trimmed, "error:") {
		return true
	}

	// EAS progress markers that appear mid-line
	lowerTrimmed := strings.ToLower(trimmed)
	if strings.Contains(lowerTrimmed, "creating build") ||
		strings.Contains(lowerTrimmed, "prebuild") ||
		strings.Contains(lowerTrimmed, "pod install") {
		return true
	}

	return false
}

// shortenBuildLine condenses verbose compiler output into human-friendly
// single-line summaries. Long SwiftCompile / CompileC invocations become
// "Compiling File.swift"; SwiftEmitModule becomes "Emitting module Target".
// Lines that don't match a known pattern are returned unchanged.
func shortenBuildLine(line string) string {
	trimmed := strings.TrimLeft(line, " \t")

	switch {
	case strings.HasPrefix(trimmed, "SwiftCompile "):
		if name := extractFilenameFromCompileLine(trimmed); name != "" {
			return "Compiling " + name
		}
	case strings.HasPrefix(trimmed, "CompileC "):
		if name := extractFilenameFromCompileLine(trimmed); name != "" {
			return "Compiling " + name
		}
	case strings.HasPrefix(trimmed, "SwiftEmitModule "):
		if target := extractTarget(trimmed); target != "" {
			return "Emitting module " + target
		}
		return "Emitting module"
	case strings.HasPrefix(trimmed, "Ld "):
		if target := extractTarget(trimmed); target != "" {
			return "Linking " + target
		}
	case strings.HasPrefix(trimmed, "CodeSign "):
		if name := extractLastPathComponent(trimmed, "CodeSign "); name != "" {
			return "Signing " + name
		}
	}
	return line
}

func extractFilenameFromCompileLine(line string) string {
	fields := strings.Fields(line)
	for _, f := range fields {
		if strings.Contains(f, "/") && (strings.HasSuffix(f, ".swift") ||
			strings.HasSuffix(f, ".m") || strings.HasSuffix(f, ".c") ||
			strings.HasSuffix(f, ".mm") || strings.HasSuffix(f, ".cpp")) {
			return filepath.Base(f)
		}
	}
	return ""
}

func extractTarget(line string) string {
	idx := strings.Index(line, "in target '")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len("in target '"):]
	end := strings.Index(rest, "'")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func extractLastPathComponent(line, prefix string) string {
	rest := strings.TrimPrefix(line, prefix)
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// BuildToolError represents a build command failure that the CLI can diagnose
// and provide actionable remediation guidance for. Covers missing executables
// (Bazel, npx, EAS CLI, etc.) and misconfigured build environments.
type BuildToolError struct {
	// Message is a short summary of the problem (e.g. "bazel not found").
	Message string

	// Guidance provides step-by-step instructions on how to fix the error.
	Guidance string
}

// Error implements the error interface.
func (e *BuildToolError) Error() string {
	return e.Message
}

// parseBuildToolError checks stderr output for known build-tool error patterns
// and returns a BuildToolError with actionable guidance if a match is found.
// Checks are ordered from most-general (missing executable) to tool-specific
// (EAS auth, config files).
//
// Parameters:
//   - stderr: the combined stderr output from the build command
//   - command: the build command string from .revyl/config.yaml (used to
//     tailor guidance to the specific tool the user configured)
//
// Returns:
//   - *BuildToolError if a known pattern matched, nil otherwise
func parseBuildToolError(stderr, command string) *BuildToolError {
	lower := strings.ToLower(stderr)
	cmdLower := strings.ToLower(command)

	// --- Generic "command not found" heuristic ---
	// Shells emit "<tool>: command not found" or "command not found: <tool>".
	// Check Bazel/Bazelisk first (most specific), then fall through to a
	// catch-all that extracts the tool name from the shell message.
	if strings.Contains(lower, "bazel: command not found") ||
		strings.Contains(lower, "command not found: bazel") ||
		strings.Contains(lower, "bazelisk: command not found") ||
		strings.Contains(lower, "command not found: bazelisk") {
		return &BuildToolError{
			Message: "bazel not found",
			Guidance: `Install Bazel via Bazelisk (recommended):
  brew install bazelisk        # macOS
  npm install -g @bazel/bazelisk  # or via npm

Then verify:
  bazel --version

If your system installs the binary as "bazelisk", update .revyl/config.yaml:
  command: bazelisk build ...`,
		}
	}

	if strings.Contains(lower, "flutter: command not found") ||
		strings.Contains(lower, "command not found: flutter") {
		return &BuildToolError{
			Message: "flutter not found",
			Guidance: `Install Flutter:
  https://docs.flutter.dev/get-started/install

Then verify:
  flutter --version`,
		}
	}

	if (strings.Contains(lower, "gradle: command not found") ||
		strings.Contains(lower, "command not found: gradle") ||
		strings.Contains(lower, "gradlew: command not found") ||
		strings.Contains(lower, "command not found: gradlew")) &&
		(strings.Contains(cmdLower, "gradle") || strings.Contains(cmdLower, "gradlew")) {
		return &BuildToolError{
			Message: "gradle not found",
			Guidance: `Ensure the Gradle wrapper is present in your project:
  ls gradlew

If missing, generate it:
  gradle wrapper

Or install Gradle:
  brew install gradle`,
		}
	}

	if strings.Contains(lower, "xcodebuild: command not found") ||
		strings.Contains(lower, "command not found: xcodebuild") {
		return &BuildToolError{
			Message: "xcodebuild not found",
			Guidance: `Install Xcode from the App Store, then accept the license:
  sudo xcodebuild -license accept

Verify:
  xcodebuild -version`,
		}
	}

	// --- EAS / Expo specific errors ---
	if strings.Contains(lower, "npx: command not found") ||
		strings.Contains(lower, "command not found: npx") {
		return &BuildToolError{
			Message: "npx not found",
			Guidance: `Install Node.js (includes npm/npx):
  https://nodejs.org/

Then verify:
  npx --version`,
		}
	}

	if strings.Contains(lower, "could not determine executable to run") &&
		strings.Contains(lower, "npm exec") &&
		strings.Contains(lower, " eas ") {
		return &BuildToolError{
			Message: "invalid npx eas invocation",
			Guidance: `Use eas-cli explicitly with npx:
  npx --yes eas-cli --version
  npx --yes eas-cli login

Then run builds with:
  npx --yes eas-cli build ...`,
		}
	}

	if strings.Contains(lower, "eas") && strings.Contains(lower, "not found") {
		return &BuildToolError{
			Message: "EAS CLI not found",
			Guidance: `Run EAS via npx (recommended):
  npx --yes eas-cli --version

If needed, install globally:
  npm install -g eas-cli

Then authenticate:
  npx --yes eas-cli login`,
		}
	}

	if strings.Contains(lower, "an expo user account is required to proceed") ||
		(strings.Contains(lower, "log in to eas") && strings.Contains(lower, "stdin is not readable")) ||
		strings.Contains(lower, "not logged in") ||
		strings.Contains(lower, "eas login") {
		return &BuildToolError{
			Message: "Not logged in to EAS",
			Guidance: `Authenticate with EAS:
  npx --yes eas-cli login

Then try the build again.`,
		}
	}

	if strings.Contains(stderr, "No Expo account") {
		return &BuildToolError{
			Message: "No Expo account configured",
			Guidance: `Create an Expo account at https://expo.dev/signup
Then authenticate:
  npx --yes eas-cli login`,
		}
	}

	if strings.Contains(stderr, "app.json") && strings.Contains(stderr, "not found") {
		return &BuildToolError{
			Message: "app.json not found",
			Guidance: `Ensure you're in the correct directory with app.json.
If this is a new project, run:
  npx create-expo-app`,
		}
	}

	if strings.Contains(stderr, "eas.json") && strings.Contains(stderr, "not found") {
		return &BuildToolError{
			Message: "eas.json not found",
			Guidance: `Initialize EAS in your project:
  npx --yes eas-cli build:configure

This will create eas.json with build profiles.`,
		}
	}

	return nil
}
