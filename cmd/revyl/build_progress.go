// Package main provides shared build progress tracking for CLI build commands.
package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/ui"
)

// BuildProgressResult holds output and timing data from a tracked build.
type BuildProgressResult struct {
	// Duration is the wall-clock time the build took.
	Duration time.Duration

	// Output is the full captured build output (all lines, unfiltered).
	Output string

	// LineCount is the number of filtered lines that were displayed.
	LineCount int

	// Err is the build error, if any.
	Err error
}

// RunBuildWithProgress executes a build command using the given runner and
// displays live progress via a spinner, line count, and periodic quiet-period
// recaps. This centralises the build-progress UX used by both `revyl dev`
// initial builds and `revyl build upload`.
//
// Parameters:
//   - runner: A configured build.Runner (caller sets FilterOutput, Interactive, etc.).
//   - command: The shell command string to execute.
//   - platformKey: Platform label for spinner messages (e.g. "ios-dev").
//   - recapInterval: How often to print a quiet-period recap. Zero disables recaps.
//
// Returns:
//   - BuildProgressResult: Timing, captured output, and any error.
func RunBuildWithProgress(runner *build.Runner, command, platformKey string, recapInterval time.Duration) BuildProgressResult {
	var result BuildProgressResult
	showSpinner := !ui.IsDebugMode()

	var mu sync.Mutex
	var lineCount int
	var recentLines []string
	var output []byte

	if recapInterval <= 0 {
		recapInterval = 10 * time.Second
	}

	if showSpinner {
		ui.StartSpinner(buildProgressMessage(platformKey, 0))
	}

	var ticker *time.Ticker
	var tickerDone chan struct{}
	if showSpinner {
		ticker = time.NewTicker(recapInterval)
		tickerDone = make(chan struct{})
		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-tickerDone:
					return
				case <-ticker.C:
					mu.Lock()
					snap := make([]string, len(recentLines))
					copy(snap, recentLines)
					count := lineCount
					mu.Unlock()
					if len(snap) > 0 {
						ui.StopSpinner()
						printBuildRecap(platformKey, snap)
						ui.StartSpinner(buildProgressMessage(platformKey, count))
					}
				}
			}
		}()
	}

	start := time.Now()

	buildErr := runner.Run(command, func(line string) {
		output = append(output, line...)
		output = append(output, '\n')

		mu.Lock()
		lineCount++
		recentLines = appendBuildLine(recentLines, line, 5)
		count := lineCount
		mu.Unlock()

		if showSpinner {
			ui.StopSpinner()
		}
		ui.PrintDim("  %s", line)
		if showSpinner {
			ui.StartSpinner(buildProgressMessage(platformKey, count))
		}
	})

	if tickerDone != nil {
		close(tickerDone)
	}
	if showSpinner {
		ui.StopSpinner()
	}

	result.Duration = time.Since(start)
	result.Output = string(output)
	result.LineCount = lineCount
	result.Err = buildErr
	return result
}

// buildProgressMessage returns a spinner label including the platform and
// the number of filtered build lines seen so far.
//
// Parameters:
//   - platformKey: The build platform identifier (e.g. "ios", "android").
//   - lineCount: Number of filtered build output lines emitted so far.
//
// Returns:
//   - string: Spinner message like "Building ios... (3 updates)".
func buildProgressMessage(platformKey string, lineCount int) string {
	if lineCount <= 0 {
		return fmt.Sprintf("Building %s...", platformKey)
	}
	return fmt.Sprintf("Building %s... (%d updates)", platformKey, lineCount)
}

// appendBuildLine appends a line to a bounded slice, dropping the oldest
// entry when the limit is reached.
//
// Parameters:
//   - lines: The current slice of recent lines.
//   - line: The new line to append.
//   - limit: Maximum number of lines to retain.
//
// Returns:
//   - []string: Updated slice with the new line appended.
func appendBuildLine(lines []string, line string, limit int) []string {
	lines = append(lines, line)
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

// printBuildRecap prints the most recent filtered build lines when the build
// has gone silent for a while.
//
// Parameters:
//   - platformKey: The build platform identifier for the header message.
//   - recentLines: The rolling tail of recent filtered build lines.
func printBuildRecap(platformKey string, recentLines []string) {
	if len(recentLines) == 0 {
		return
	}
	ui.PrintDim("  Still building %s... recent output:", platformKey)
	for _, l := range recentLines {
		ui.PrintDim("    %s", l)
	}
}
