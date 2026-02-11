// Package build provides build execution and artifact management utilities.
package build

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Runner executes build commands in a specified working directory.
type Runner struct {
	// workDir is the working directory for build commands.
	workDir string
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
// Parameters:
//   - command: The build command to execute (can include shell operators)
//   - onOutput: Callback function called for each line of output
//
// Returns:
//   - error: Any error that occurred during execution
//
// The command is executed via /bin/sh -c to support shell features like pipes and redirects.
func (r *Runner) Run(command string, onOutput func(line string)) error {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = r.workDir

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
	if strings.Contains(stderr, "eas build") && strings.Contains(stderr, "not found") {
		return &EASBuildError{
			Message: "EAS CLI not found",
			Guidance: `Install the EAS CLI globally:
  npm install -g eas-cli

Then authenticate:
  eas login`,
		}
	}

	if strings.Contains(stderr, "Not logged in") || strings.Contains(stderr, "eas login") {
		return &EASBuildError{
			Message: "Not logged in to EAS",
			Guidance: `Authenticate with EAS:
  eas login

Then try the build again.`,
		}
	}

	if strings.Contains(stderr, "No Expo account") {
		return &EASBuildError{
			Message: "No Expo account configured",
			Guidance: `Create an Expo account at https://expo.dev/signup
Then authenticate:
  eas login`,
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
  eas build:configure

This will create eas.json with build profiles.`,
		}
	}

	return nil
}
