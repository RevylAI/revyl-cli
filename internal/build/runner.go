// Package build provides build execution and artifact management utilities.
package build

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
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
	isTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if r.Interactive && isTTY {
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

	// Stream stdout
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if onOutput != nil {
				onOutput(scanner.Text())
			}
		}
	}()

	// Stream stderr and capture for error detection
	var stderrLines []string
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			stderrLines = append(stderrLines, line)
			if onOutput != nil {
				onOutput(line)
			}
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
