// Package interactive provides the interactive test creation mode for the CLI.
//
// This file contains the REPL (Read-Eval-Print Loop) for interactive test creation.
package interactive

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/charmbracelet/lipgloss"
)

// REPL handles the interactive command loop.
type REPL struct {
	// session is the interactive session.
	session *Session

	// reader reads user input.
	reader *bufio.Reader

	// running indicates if the REPL is active (accessed atomically).
	running atomic.Bool

	// styles contains the UI styles.
	styles *REPLStyles
}

// REPLStyles contains the styling for REPL output.
type REPLStyles struct {
	// Prompt is the style for the input prompt.
	Prompt lipgloss.Style

	// Success is the style for success messages.
	Success lipgloss.Style

	// Error is the style for error messages.
	Error lipgloss.Style

	// Info is the style for info messages.
	Info lipgloss.Style

	// Dim is the style for dimmed text.
	Dim lipgloss.Style

	// StepIndex is the style for step indices.
	StepIndex lipgloss.Style

	// StepType is the style for step types.
	StepType lipgloss.Style

	// Header is the style for headers.
	Header lipgloss.Style
}

// NewREPLStyles creates default REPL styles.
//
// Returns:
//   - *REPLStyles: The default styles
func NewREPLStyles() *REPLStyles {
	return &REPLStyles{
		Prompt:    lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true),
		Success:   lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		Info:      lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
		Dim:       lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		StepIndex: lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true),
		StepType:  lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		Header:    lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true).Underline(true),
	}
}

// NewREPL creates a new REPL instance.
//
// Parameters:
//   - session: The interactive session to use
//
// Returns:
//   - *REPL: A new REPL instance
func NewREPL(session *Session) *REPL {
	return &REPL{
		session: session,
		reader:  bufio.NewReader(os.Stdin),
		styles:  NewREPLStyles(),
	}
}

// Run starts the REPL loop.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error: Any error that occurred
func (r *REPL) Run(ctx context.Context) error {
	r.running.Store(true)

	// Create a cancellable context for the REPL
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Ensure cleanup on any exit (including panic)
	defer func() {
		if r.session.State() != StateStopped {
			fmt.Println(r.styles.Info.Render("Cleaning up..."))
			_ = r.session.Stop()
		}
	}()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Handle signals in a separate goroutine
	go func() {
		select {
		case sig := <-sigChan:
			fmt.Printf("\n%s\n", r.styles.Info.Render(fmt.Sprintf("Received %v, shutting down...", sig)))
			r.running.Store(false)
			cancel() // Cancel context to unblock any waiting operations
		case <-ctx.Done():
			return
		}
	}()

	// Set up session callbacks
	r.session.SetOnLog(func(msg string) {
		fmt.Println(r.styles.Dim.Render("  " + msg))
	})

	r.session.SetOnStateChange(func(state SessionState) {
		switch state {
		case StateReady:
			// Don't print anything, just ready for input
		case StateExecuting:
			fmt.Println(r.styles.Info.Render("  Executing..."))
		case StateError:
			fmt.Println(r.styles.Error.Render("  Session error"))
		}
	})

	r.session.SetOnStepResult(func(step *StepRecord) {
		r.printStepResult(step)
	})

	// Print welcome message
	r.printWelcome()

	// Start the session
	fmt.Println(r.styles.Info.Render("Starting device..."))
	if err := r.session.Start(ctx); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}
	fmt.Println(r.styles.Success.Render("Device ready!"))
	fmt.Println()

	// Display frontend URL for live preview
	frontendURL := r.session.GetFrontendURL()
	fmt.Println(r.styles.Info.Render("Live preview: " + frontendURL))
	fmt.Println()

	// Channel for input lines
	inputChan := make(chan string)
	errChan := make(chan error)

	// Read input in a separate goroutine
	go func() {
		for r.running.Load() {
			input, err := r.reader.ReadString('\n')
			if err != nil {
				if r.running.Load() { // Only send error if we're still running
					errChan <- err
				}
				return
			}
			if r.running.Load() { // Only send input if we're still running
				inputChan <- strings.TrimSpace(input)
			}
		}
	}()

	// Main REPL loop
	for r.running.Load() {
		// Print prompt
		prompt := r.styles.Prompt.Render("revyl> ")
		fmt.Print(prompt)

		// Wait for input or cancellation
		select {
		case <-ctx.Done():
			r.running.Store(false)
			continue

		case err := <-errChan:
			if err.Error() == "EOF" {
				r.running.Store(false)
				continue
			}
			fmt.Println(r.styles.Error.Render(fmt.Sprintf("Error reading input: %v", err)))
			continue

		case input := <-inputChan:
			if input == "" {
				continue
			}

			// Parse and execute command
			if err := r.executeCommand(ctx, input); err != nil {
				fmt.Println(r.styles.Error.Render(fmt.Sprintf("Error: %v", err)))
			}
		}
	}

	// Cleanup
	fmt.Println(r.styles.Info.Render("Stopping session..."))
	if err := r.session.Stop(); err != nil {
		fmt.Println(r.styles.Error.Render(fmt.Sprintf("Warning: %v", err)))
	}
	fmt.Println(r.styles.Success.Render("Session stopped."))

	return nil
}

// executeCommand parses and executes a user command.
func (r *REPL) executeCommand(ctx context.Context, input string) error {
	cmd, err := ParseCommand(input)
	if err != nil {
		return err
	}

	switch cmd.Type {
	case CommandHelp:
		fmt.Println(HelpText())
		return nil

	case CommandQuit:
		r.running.Store(false)
		return nil

	case CommandStatus:
		r.printStatus()
		return nil

	case CommandList:
		r.printSteps()
		return nil

	case CommandUndo:
		return r.handleUndo()

	case CommandSave:
		return r.handleSave(cmd.Args)

	case CommandClear:
		return r.handleClear()

	case CommandReplay:
		return r.handleReplay(ctx, cmd.Args)

	case CommandRun:
		return r.handleRun(ctx)

	default:
		// Step commands
		if IsStepCommand(cmd) {
			return r.executeStep(ctx, cmd)
		}
		return fmt.Errorf("unknown command: %s", cmd.Type)
	}
}

// executeStep executes a step command.
func (r *REPL) executeStep(ctx context.Context, cmd *ParsedCommand) error {
	stepType := GetStepType(cmd.Type)
	instruction := cmd.Instruction

	// For commands without explicit instruction, use the command type
	if instruction == "" {
		instruction = string(cmd.Type)
	}

	_, err := r.session.ExecuteStep(ctx, stepType, instruction)
	return err
}

// handleUndo removes the last step.
func (r *REPL) handleUndo() error {
	step, err := r.session.UndoLastStep()
	if err != nil {
		return err
	}

	fmt.Println(r.styles.Success.Render(fmt.Sprintf("Removed step %d: %s", step.Index+1, step.Instruction)))
	return nil
}

// handleSave exports the test to YAML.
func (r *REPL) handleSave(args []string) error {
	filename := "test.yaml"
	if len(args) > 0 {
		filename = args[0]
	}

	// Ensure .yaml extension
	if !strings.HasSuffix(filename, ".yaml") && !strings.HasSuffix(filename, ".yml") {
		filename += ".yaml"
	}

	steps := r.session.Steps()
	if len(steps) == 0 {
		return fmt.Errorf("no steps to save")
	}

	yaml := BuildYAML(r.session.GetTestID(), r.session.GetPlatform(), steps)

	if err := os.WriteFile(filename, []byte(yaml), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Println(r.styles.Success.Render(fmt.Sprintf("Saved %d steps to %s", len(steps), filename)))
	return nil
}

// handleClear clears all recorded steps.
func (r *REPL) handleClear() error {
	steps := r.session.Steps()
	if len(steps) == 0 {
		fmt.Println(r.styles.Info.Render("No steps to clear"))
		return nil
	}

	// Undo all steps
	for range steps {
		if _, err := r.session.UndoLastStep(); err != nil {
			return fmt.Errorf("failed to clear steps: %w", err)
		}
	}

	fmt.Println(r.styles.Success.Render(fmt.Sprintf("Cleared %d steps", len(steps))))
	return nil
}

// handleReplay re-executes a step by index.
func (r *REPL) handleReplay(ctx context.Context, args []string) error {
	steps := r.session.Steps()
	if len(steps) == 0 {
		return fmt.Errorf("no steps to replay")
	}

	// Default to last step
	index := len(steps) - 1
	if len(args) > 0 {
		i, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid step index: %s", args[0])
		}
		index = i - 1 // Convert to 0-indexed
	}

	if index < 0 || index >= len(steps) {
		return fmt.Errorf("step index out of range (1-%d)", len(steps))
	}

	step := steps[index]
	fmt.Println(r.styles.Info.Render(fmt.Sprintf("Replaying step %d: %s", index+1, step.Instruction)))

	_, err := r.session.ExecuteStep(ctx, step.Type, step.Instruction)
	return err
}

// handleRun executes all steps from the beginning.
func (r *REPL) handleRun(ctx context.Context) error {
	steps := r.session.Steps()
	if len(steps) == 0 {
		return fmt.Errorf("no steps to run")
	}

	fmt.Println(r.styles.Info.Render(fmt.Sprintf("Running %d steps...", len(steps))))

	for i, step := range steps {
		fmt.Println(r.styles.Info.Render(fmt.Sprintf("Step %d/%d: %s", i+1, len(steps), step.Instruction)))

		_, err := r.session.ExecuteStep(ctx, step.Type, step.Instruction)
		if err != nil {
			return fmt.Errorf("step %d failed: %w", i+1, err)
		}
	}

	fmt.Println(r.styles.Success.Render("All steps completed!"))
	return nil
}

// printWelcome prints the welcome message.
func (r *REPL) printWelcome() {
	fmt.Println()
	fmt.Println(r.styles.Header.Render("Revyl Interactive Test Creation"))
	fmt.Println()
	fmt.Println("Type natural language instructions to create test steps.")
	fmt.Println("Type 'help' for available commands, 'quit' to exit.")
	fmt.Println()
}

// printStatus prints the current session status.
func (r *REPL) printStatus() {
	fmt.Println()
	fmt.Println(r.styles.Header.Render("Session Status"))
	fmt.Println()
	fmt.Printf("  State:        %s\n", r.session.State())
	fmt.Printf("  Platform:     %s\n", r.session.GetPlatform())
	fmt.Printf("  Test ID:      %s\n", r.session.GetTestID())
	fmt.Printf("  Workflow Run: %s\n", r.session.WorkflowRunID())
	fmt.Printf("  Steps:        %d\n", len(r.session.Steps()))
	fmt.Println()
}

// printSteps prints all recorded steps.
func (r *REPL) printSteps() {
	steps := r.session.Steps()
	if len(steps) == 0 {
		fmt.Println(r.styles.Info.Render("No steps recorded yet"))
		return
	}

	fmt.Println()
	fmt.Println(r.styles.Header.Render("Recorded Steps"))
	fmt.Println()

	for _, step := range steps {
		indexStr := r.styles.StepIndex.Render(fmt.Sprintf("%3d.", step.Index+1))
		typeStr := r.styles.StepType.Render(fmt.Sprintf("[%s]", step.Type))

		statusIcon := "○"
		if step.Success != nil {
			if *step.Success {
				statusIcon = r.styles.Success.Render("✓")
			} else {
				statusIcon = r.styles.Error.Render("✗")
			}
		}

		fmt.Printf("  %s %s %s %s\n", indexStr, statusIcon, typeStr, step.Instruction)

		if step.Error != "" {
			fmt.Printf("      %s\n", r.styles.Error.Render(step.Error))
		}
	}
	fmt.Println()
}

// printStepResult prints the result of a step execution.
func (r *REPL) printStepResult(step *StepRecord) {
	if step.Success != nil && *step.Success {
		fmt.Println(r.styles.Success.Render(fmt.Sprintf("  ✓ Step %d completed (%dms)", step.Index+1, step.Duration)))
	} else if step.Success != nil && !*step.Success {
		fmt.Println(r.styles.Error.Render(fmt.Sprintf("  ✗ Step %d failed: %s", step.Index+1, step.Error)))
	} else {
		fmt.Println(r.styles.Info.Render(fmt.Sprintf("  ○ Step %d executed", step.Index+1)))
	}

	// Print actions taken
	if len(step.ActionsTaken) > 0 {
		for _, action := range step.ActionsTaken {
			desc := action.Description
			if desc == "" {
				desc = action.Type
			}
			fmt.Println(r.styles.Dim.Render(fmt.Sprintf("    → %s", desc)))
		}
	}
}
