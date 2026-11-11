package commandanalytics

import (
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var (
	lastCommandID       string
	lastFailedCommandID string
	commandBodyStarted  bool
)

// CommandBodyStarted reports whether any command body ran in this process.
// When it is false, cobra rejected the invocation while parsing flags,
// arguments, or the command path.
func CommandBodyStarted() bool {
	return commandBodyStarted
}

// LastFailedCommandID returns the analytics command ID of the most recent
// invocation in this process that returned an error, or "" when telemetry was
// disabled or no command failed.
func LastFailedCommandID() string {
	return lastFailedCommandID
}

// LastCommandID returns the analytics command ID of the most recent invocation
// in this process, or "" when telemetry was disabled or no command body ran.
func LastCommandID() string {
	return lastCommandID
}

func Install(root *cobra.Command, cfg analytics.Config) {
	wrapCommandAnalytics(root, cfg)
}

func wrapCommandAnalytics(cmd *cobra.Command, cfg analytics.Config) {
	if cmd == nil {
		return
	}

	if cmd.RunE != nil {
		original := cmd.RunE
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			runConfig := cfg
			runConfig.BackendURL = config.GetBackendURL(DevMode(cmd))
			return Run(cmd, args, runConfig, func() error {
				return original(cmd, args)
			})
		}
	} else if cmd.Run != nil {
		original := cmd.Run
		cmd.Run = func(cmd *cobra.Command, args []string) {
			runConfig := cfg
			runConfig.BackendURL = config.GetBackendURL(DevMode(cmd))
			_ = Run(cmd, args, runConfig, func() error {
				original(cmd, args)
				return nil
			})
		}
	}

	for _, child := range cmd.Commands() {
		wrapCommandAnalytics(child, cfg)
	}
}

func Run(cmd *cobra.Command, args []string, cfg analytics.Config, run func() error) (err error) {
	commandBodyStarted = true
	rec := analytics.NewFromEnv(cfg)

	commandRun := rec.StartCommand(cmd, args)
	if commandRun != nil {
		originalContext := cmd.Context()
		cmd.SetContext(analytics.ContextWithCommandRun(originalContext, commandRun))
		ui.SetOutputObserver(commandRun.ObserveOutput)
		defer func() {
			ui.SetOutputObserver(nil)
			cmd.SetContext(originalContext)
		}()
		defer func() {
			panicValue := recover()
			completeCommandAnalytics(commandRun, err, panicValue != nil)
			if panicValue != nil {
				panic(panicValue)
			}
		}()
	}

	return run()
}

func completeCommandAnalytics(commandRun *analytics.CommandRun, err error, panicked bool) {
	if commandRun == nil {
		return
	}
	lastCommandID = commandRun.CommandID()
	if err != nil || panicked {
		lastFailedCommandID = commandRun.CommandID()
	}
	if panicked {
		// Never capture a raw panic value: it may contain customer input. The
		// original panic is rethrown immediately after this best-effort fact.
		commandRun.Complete(analytics.ErrCommandPanicked)
	} else {
		commandRun.Complete(err)
	}
	commandRun.Flush()
}

func DevMode(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	devFlag := cmd.Flag("dev")
	return devFlag != nil && devFlag.Value.String() == "true"
}
