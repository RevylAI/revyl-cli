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
		// Check for EAS-specific errors
		stderrOutput := strings.Join(stderrLines, "\n")
		if easErr := parseEASError(stderrOutput); easErr != nil {
			return easErr
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
// destination lists, and tool paths; shows build phase names, warnings, and errors.
func shouldShowBuildLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}

	showPrefixes := []string{
		"Compiling", "Linking", "Signing",
		"CodeSign", "Ld ", "CompileC",
		"SwiftCompile", "SwiftEmitModule",
		"Build Succeeded", "Build Failed",
		"BUILD SUCCEEDED", "BUILD FAILED",
		"BUILD SUCCESSFUL", "Build completed",
		"** BUILD",
		"warning:", "error:",
		"note: Building targets",
		":app:", "> Task :",
	}
	for _, p := range showPrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}

	if strings.Contains(trimmed, "warning:") || strings.Contains(trimmed, "error:") {
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

// EASBuildError represents an error from Expo Application Services (EAS) builds.
type EASBuildError struct {
	// Message is the error message.
	Message string

	// Guidance provides instructions on how to fix the error.
	Guidance string
}

// Error implements the error interface.
func (e *EASBuildError) Error() string {
	return e.Message
}

// parseEASError checks stderr output for known EAS error patterns and returns
// an EASBuildError with guidance if found.
//
// Parameters:
//   - stderr: The stderr output from the build command
//
// Returns:
//   - *EASBuildError: An EAS error with guidance, or nil if not an EAS error
func parseEASError(stderr string) *EASBuildError {
	// Check for common EAS errors
	lower := strings.ToLower(stderr)

	if strings.Contains(lower, "npx: command not found") {
		return &EASBuildError{
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
		return &EASBuildError{
			Message: "invalid npx eas invocation",
			Guidance: `Use eas-cli explicitly with npx:
  npx --yes eas-cli --version
  npx --yes eas-cli login

Then run builds with:
  npx --yes eas-cli build ...`,
		}
	}

	if strings.Contains(lower, "eas") && strings.Contains(lower, "not found") {
		return &EASBuildError{
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
		return &EASBuildError{
			Message: "Not logged in to EAS",
			Guidance: `Authenticate with EAS:
  npx --yes eas-cli login

Then try the build again.`,
		}
	}

	if strings.Contains(stderr, "No Expo account") {
		return &EASBuildError{
			Message: "No Expo account configured",
			Guidance: `Create an Expo account at https://expo.dev/signup
Then authenticate:
  npx --yes eas-cli login`,
		}
	}

	if strings.Contains(stderr, "app.json") && strings.Contains(stderr, "not found") {
		return &EASBuildError{
			Message: "app.json not found",
			Guidance: `Ensure you're in the correct directory with app.json.
If this is a new project, run:
  npx create-expo-app`,
		}
	}

	if strings.Contains(stderr, "eas.json") && strings.Contains(stderr, "not found") {
		return &EASBuildError{
			Message: "eas.json not found",
			Guidance: `Initialize EAS in your project:
  npx --yes eas-cli build:configure

This will create eas.json with build profiles.`,
		}
	}

	return nil
}
