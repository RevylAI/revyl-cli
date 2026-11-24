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
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/hotreload"
	"github.com/revyl/cli/internal/util"
)

type REPL struct {
	session *Session

	reader *bufio.Reader

	running atomic.Bool

	styles *REPLStyles

	hotReloadManager *hotreload.Manager

	hotReloadURL string
}

type REPLStyles struct {
	Prompt lipgloss.Style

	Success lipgloss.Style

	Error lipgloss.Style

	Info lipgloss.Style

	Dim lipgloss.Style

	StepIndex lipgloss.Style

	StepType lipgloss.Style

	Header lipgloss.Style

	Running lipgloss.Style

	Action lipgloss.Style

	Duration lipgloss.Style
}

func NewREPLStyles() *REPLStyles {
	return &REPLStyles{
		Prompt:    lipgloss.NewStyle().Foreground(lipgloss.Color("#9D61FF")).Bold(true),
		Success:   lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")),
		Info:      lipgloss.NewStyle(),
		Dim:       lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")),
		StepIndex: lipgloss.NewStyle().Foreground(lipgloss.Color("#9D61FF")).Bold(true),
		StepType:  lipgloss.NewStyle().Foreground(lipgloss.Color("#9D61FF")),
		Header:    lipgloss.NewStyle().Foreground(lipgloss.Color("#9D61FF")).Bold(true).Underline(true),
		Running:   lipgloss.NewStyle().Foreground(lipgloss.Color("#14B8A6")),
		Action:    lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")),
		Duration:  lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")),
	}
}

func NewREPL(session *Session) *REPL {
	return &REPL{
		session:      session,
		reader:       bufio.NewReader(os.Stdin),
		styles:       NewREPLStyles(),
		hotReloadURL: session.GetHotReloadURL(),
	}
}

// SetHotReloadManager assigns ownership of hot reload cleanup to the REPL.
func (r *REPL) SetHotReloadManager(manager *hotreload.Manager) {
	r.hotReloadManager = manager
}

// SetHotReloadURL supplies the deep link used by a bare navigate command.
func (r *REPL) SetHotReloadURL(url string) {
	r.hotReloadURL = url
}

func (r *REPL) Run(ctx context.Context) error {
	r.running.Store(true)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer func() {
		// Stop hot reload manager first (dev server + tunnel)
		if r.hotReloadManager != nil {
			fmt.Println(r.styles.Info.Render("Stopping hot reload..."))
			r.hotReloadManager.Stop()
		}

		// Then stop the session
		if r.session.State() != StateStopped {
			fmt.Println(r.styles.Info.Render("Cleaning up..."))
			_ = r.session.Stop()
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case sig := <-sigChan:
			fmt.Printf("\n%s\n", r.styles.Info.Render(fmt.Sprintf("Received %v, shutting down...", sig)))
			r.running.Store(false)
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	var readyPromptShown atomic.Bool

	r.session.SetOnLog(func(msg string) {
		// Skip startup logs once the ready prompt is shown.
		if !readyPromptShown.Load() {
			fmt.Println(r.styles.Dim.Render("  " + msg))
		}
	})

	r.session.SetOnStateChange(func(state SessionState) {
		switch state {
		case StateReady:
		case StateExecuting:
			// Don't print here - we'll print the instruction in executeStep
		case StateError:
			fmt.Println(r.styles.Error.Render("  Session error"))
		}
	})

	r.session.SetOnStepResult(func(step *StepRecord) {
		r.printStepResult(step)
	})

	r.session.SetOnStepProgress(func(msg *api.StepStreamMessage) {
		r.printStepProgress(msg)
	})

	fmt.Println(r.styles.Info.Render("Starting device..."))
	if err := r.session.Start(ctx); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}
	if r.session.IsDeviceReady() {
		fmt.Println(r.styles.Success.Render("Device ready!"))
	} else {
		fmt.Println(r.styles.Info.Render("Connected to the worker; waiting for the device to initialize..."))
		r.session.WhenDeviceReady(func() {
			shown := readyPromptShown.Load()
			if shown {
				fmt.Println()
			}
			fmt.Println(r.styles.Success.Render("Device ready!"))
			if shown {
				fmt.Print(r.styles.Prompt.Render("revyl> "))
			}
		})
	}
	fmt.Println()

	r.printWelcome()

	frontendURL := r.session.GetFrontendURL()
	fmt.Println(r.styles.Info.Render("Live preview: " + frontendURL))
	fmt.Println()

	r.printReadyBanner()
	readyPromptShown.Store(true)

	inputChan := make(chan string)
	errChan := make(chan error)

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

	for r.running.Load() {
		prompt := r.styles.Prompt.Render("revyl> ")
		fmt.Print(prompt)

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

			if err := r.executeCommand(ctx, input); err != nil {
				fmt.Println(r.styles.Error.Render(fmt.Sprintf("Error: %v", err)))
			}
		}
	}

	fmt.Println(r.styles.Info.Render("Stopping session..."))
	if err := r.session.Stop(); err != nil {
		fmt.Println(r.styles.Error.Render(fmt.Sprintf("Warning: %v", err)))
	}
	fmt.Println(r.styles.Success.Render("Session stopped."))

	return nil
}

func (r *REPL) executeCommand(ctx context.Context, input string) error {
	cmd, err := ParseCommandWithDefaults(input, r.hotReloadURL)
	if err != nil {
		return err
	}

	switch cmd.Type {
	case CommandHelp:
		r.printHelp()
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
		if !r.session.IsDeviceReady() {
			return fmt.Errorf("device is still initializing, please wait...")
		}
		return r.handleReplay(ctx, cmd.Args)

	case CommandRun:
		if !r.session.IsDeviceReady() {
			return fmt.Errorf("device is still initializing, please wait...")
		}
		return r.handleRun(ctx)

	default:
		if IsStepCommand(cmd) {
			return r.executeStep(ctx, cmd)
		}
		return fmt.Errorf("unknown command: %s", cmd.Type)
	}
}

func (r *REPL) executeStep(ctx context.Context, cmd *ParsedCommand) error {
	if !r.session.IsDeviceReady() {
		return fmt.Errorf("device is still initializing, please wait...")
	}

	instruction := cmd.Instruction

	if instruction == "" {
		instruction = string(cmd.Type)
	}

	stepNum := len(r.session.Steps()) + 1
	fmt.Println()
	fmt.Printf("  %s %s\n",
		r.styles.Running.Render("●"),
		r.styles.Info.Render(fmt.Sprintf("Step %d", stepNum)))
	fmt.Printf("    %s %s\n",
		r.styles.StepType.Render(string(cmd.Type)),
		r.styles.Dim.Render(instruction))
	fmt.Printf("    %s\n", r.styles.Running.Render("⋯ executing"))

	_, err := r.session.ExecuteStep(ctx, cmd.Type, instruction)
	return err
}

func (r *REPL) handleUndo() error {
	step, err := r.session.UndoLastStep()
	if err != nil {
		return err
	}

	fmt.Println(r.styles.Success.Render(fmt.Sprintf("Removed step %d: %s", step.Index+1, step.Instruction)))
	return nil
}

func (r *REPL) handleSave(args []string) error {
	steps := r.session.Steps()
	if len(steps) == 0 {
		return fmt.Errorf("no steps to save")
	}

	var filename string
	if len(args) > 0 {
		filename = args[0]
	} else {
		testName := r.session.GetTestName()
		if testName != "" {
			filename = util.SanitizeForFilename(testName)
		} else {
			filename = "test"
		}
	}

	if !strings.HasSuffix(filename, ".yaml") && !strings.HasSuffix(filename, ".yml") {
		filename += ".yaml"
	}

	if _, err := os.Stat(filename); err == nil {
		fmt.Printf("  %s File %s already exists\n", r.styles.Error.Render("!"), filename)
		fmt.Printf("  %s Use 'save <new-filename>' to save with a different name\n", r.styles.Dim.Render("→"))
		return nil
	}

	yaml := BuildYAML(r.session.GetTestID(), r.session.GetPlatform(), steps)

	if err := os.WriteFile(filename, []byte(yaml), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Println()
	fmt.Printf("  %s Saved %d steps to %s\n", r.styles.Success.Render("✓"), len(steps), r.styles.Info.Render(filename))
	return nil
}

func (r *REPL) handleClear() error {
	steps := r.session.Steps()
	if len(steps) == 0 {
		fmt.Println(r.styles.Info.Render("No steps to clear"))
		return nil
	}

	for range steps {
		if _, err := r.session.UndoLastStep(); err != nil {
			return fmt.Errorf("failed to clear steps: %w", err)
		}
	}

	fmt.Println(r.styles.Success.Render(fmt.Sprintf("Cleared %d steps", len(steps))))
	return nil
}

func (r *REPL) handleReplay(ctx context.Context, args []string) error {
	steps := r.session.Steps()
	if len(steps) == 0 {
		return fmt.Errorf("no steps to replay")
	}

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

	cmdType := StepTypeToCommandType(step.StepType)
	_, err := r.session.ExecuteStep(ctx, cmdType, step.Instruction)
	return err
}

func (r *REPL) handleRun(ctx context.Context) error {
	steps := r.session.Steps()
	if len(steps) == 0 {
		return fmt.Errorf("no steps to run")
	}

	fmt.Println(r.styles.Info.Render(fmt.Sprintf("Running %d steps...", len(steps))))

	for i, step := range steps {
		fmt.Println(r.styles.Info.Render(fmt.Sprintf("Step %d/%d: %s", i+1, len(steps), step.Instruction)))

		cmdType := StepTypeToCommandType(step.StepType)
		_, err := r.session.ExecuteStep(ctx, cmdType, step.Instruction)
		if err != nil {
			return fmt.Errorf("step %d failed: %w", i+1, err)
		}
	}

	fmt.Println(r.styles.Success.Render("All steps completed!"))
	return nil
}

func (r *REPL) printWelcome() {
	fmt.Println()
	fmt.Println(r.styles.Header.Render("Revyl Interactive Test Creation"))
	fmt.Println()
	fmt.Println("Type natural language instructions to create test steps.")
	fmt.Println("Type 'help' for available commands, 'quit' to exit.")
	if r.hotReloadURL != "" {
		fmt.Println()
		fmt.Println(r.styles.Info.Render("Hot reload mode: Type 'navigate' to open the dev app"))
	}
	fmt.Println()
}

func (r *REPL) printReadyBanner() {
	fmt.Println(r.styles.Success.Render("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))
	if r.session.IsDeviceReady() {
		fmt.Println(r.styles.Success.Render("  Ready! Enter your first instruction below."))
	} else {
		fmt.Println(r.styles.Success.Render("  Connected. Enter your first instruction once the device reports ready."))
	}
	fmt.Println(r.styles.Success.Render("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))
	fmt.Println()
}

func (r *REPL) printHelp() {
	purple := lipgloss.NewStyle().Foreground(lipgloss.Color("#9D61FF"))
	teal := lipgloss.NewStyle().Foreground(lipgloss.Color("#14B8A6"))
	cmd := lipgloss.NewStyle().Bold(true)
	desc := lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	example := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#374151"))

	line := divider.Render("─────────────────────────────────────────────────────────")

	fmt.Println()

	fmt.Printf("  %s\n", purple.Bold(true).Render("STEP COMMANDS"))
	fmt.Printf("  %s\n", line)
	fmt.Println()

	fmt.Printf("  %s  %s\n", cmd.Render("<instruction>     "), desc.Render("Natural language action on the device"))
	fmt.Printf("                       %s %s\n", example.Render("→"), example.Render("Tap the Sign In button"))
	fmt.Printf("                       %s %s\n", example.Render("→"), example.Render("Type \"hello@example.com\" in the email field"))
	fmt.Println()

	fmt.Printf("  %s  %s\n", cmd.Render("validate <text>   "), desc.Render("Assert something is true on screen"))
	fmt.Printf("                       %s %s\n", example.Render("→"), example.Render("validate Welcome message is visible"))
	fmt.Println()

	fmt.Printf("  %s  %s\n", cmd.Render("wait <time/cond>  "), desc.Render("Pause or wait for a condition"))
	fmt.Printf("                       %s %s\n", example.Render("→"), example.Render("wait 3s"))
	fmt.Printf("                       %s %s\n", example.Render("→"), example.Render("wait for loading to complete"))
	fmt.Println()

	fmt.Printf("  %s  %s\n", cmd.Render("navigate <url>    "), desc.Render("Open a URL or deep link"))
	fmt.Printf("                       %s %s\n", example.Render("→"), example.Render("navigate myapp://settings"))
	fmt.Println()

	fmt.Printf("  %s  %s\n", cmd.Render("back              "), desc.Render("Press the back button"))
	fmt.Printf("  %s  %s\n", cmd.Render("home              "), desc.Render("Press the home button"))
	fmt.Printf("  %s  %s\n", cmd.Render("open-app <id>     "), desc.Render("Launch app by bundle ID"))
	fmt.Printf("  %s  %s\n", cmd.Render("kill-app [id]     "), desc.Render("Terminate app (current if no ID)"))
	fmt.Println()

	fmt.Printf("  %s\n", teal.Bold(true).Render("SESSION COMMANDS"))
	fmt.Printf("  %s\n", line)
	fmt.Println()

	fmt.Printf("  %s %s      %s %s\n",
		cmd.Render("undo     "), desc.Render("Remove last step"),
		cmd.Render("list  "), desc.Render("Show all steps"))
	fmt.Printf("  %s %s      %s %s\n",
		cmd.Render("save     "), desc.Render("Export to YAML  "),
		cmd.Render("status"), desc.Render("Session info"))
	fmt.Printf("  %s %s      %s %s\n",
		cmd.Render("clear    "), desc.Render("Clear all steps "),
		cmd.Render("run   "), desc.Render("Execute all"))
	fmt.Printf("  %s %s      %s %s\n",
		cmd.Render("replay   "), desc.Render("Re-run a step   "),
		cmd.Render("quit  "), desc.Render("Exit"))
	fmt.Println()

	fmt.Printf("  %s\n", divider.Render("─────────────────────────────────────────────────────────"))
	fmt.Printf("  %s %s\n", example.Render("tip:"), desc.Render("Steps auto-save after each execution. Use natural language!"))
	fmt.Println()
}

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
		typeStr := r.styles.StepType.Render(fmt.Sprintf("[%s]", step.StepType))

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

func (r *REPL) printStepResult(step *StepRecord) {
	durationStr := fmt.Sprintf("%dms", step.Duration)
	if step.Duration >= 1000 {
		durationStr = fmt.Sprintf("%.1fs", float64(step.Duration)/1000)
	}

	fmt.Print("\033[1A\033[K")

	if step.Success != nil && *step.Success {
		fmt.Printf("    %s %s\n",
			r.styles.Success.Render("✓ completed"),
			r.styles.Duration.Render(durationStr))
	} else if step.Success != nil && !*step.Success {
		fmt.Printf("    %s\n", r.styles.Error.Render("✗ failed"))
		fmt.Printf("      %s\n", r.styles.Error.Render(step.Error))
	} else {
		fmt.Printf("    %s\n", r.styles.Dim.Render("○ executed"))
	}

	if len(step.ActionsTaken) > 0 {
		for _, action := range step.ActionsTaken {
			desc := action.Description
			if desc == "" {
				desc = action.Type
			}
			fmt.Printf("      %s %s\n", r.styles.Action.Render("→"), r.styles.Action.Render(desc))
		}
	}
}

func (r *REPL) printStepProgress(msg *api.StepStreamMessage) {
	fmt.Print("\033[1A\033[K")

	var progressInfo string

	actionType := msg.ActionType
	if actionType == "" && msg.Result != nil {
		actionType = msg.Result.ActionType
	}

	if msg.Result != nil && msg.Result.CurrentStep != "" {
		progressInfo = msg.Result.CurrentStep
	} else if actionType != "" {
		progressInfo = actionType
	}

	statusIcon := r.styles.Running.Render("⋯")
	statusText := "executing"

	if msg.Status == "started" {
		statusText = "starting"
	} else if msg.Status == "in_progress" {
		statusText = "in progress"
	}

	if progressInfo != "" {
		fmt.Printf("    %s %s: %s\n", statusIcon, r.styles.Running.Render(statusText), r.styles.Action.Render(progressInfo))
	} else {
		fmt.Printf("    %s %s\n", statusIcon, r.styles.Running.Render(statusText))
	}

	if msg.Result != nil {
		if msg.Result.Reasoning != "" {
			reasoning := msg.Result.Reasoning
			if len(reasoning) > 80 {
				reasoning = reasoning[:77] + "..."
			}
			fmt.Printf("      %s %s\n", r.styles.Dim.Render("💭"), r.styles.Dim.Render(reasoning))
		}

		if msg.Result.ActionDescription != "" {
			fmt.Printf("      %s %s\n", r.styles.Action.Render("→"), r.styles.Action.Render(msg.Result.ActionDescription))
		}
	}
}
