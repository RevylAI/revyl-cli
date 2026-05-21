package main

import (
	"sync"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var analyticsInstallOnce sync.Once

func installAnalytics(root *cobra.Command) {
	analyticsInstallOnce.Do(func() {
		wrapCommandAnalytics(root)
	})
}

func wrapCommandAnalytics(cmd *cobra.Command) {
	if cmd == nil {
		return
	}

	if cmd.RunE != nil {
		original := cmd.RunE
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return runWithAnalytics(cmd, args, func() error {
				return original(cmd, args)
			})
		}
	} else if cmd.Run != nil {
		original := cmd.Run
		cmd.Run = func(cmd *cobra.Command, args []string) {
			_ = runWithAnalytics(cmd, args, func() error {
				original(cmd, args)
				return nil
			})
		}
	}

	for _, child := range cmd.Commands() {
		wrapCommandAnalytics(child)
	}
}

func runWithAnalytics(cmd *cobra.Command, args []string, run func() error) (err error) {
	rec := analytics.NewFromEnv(analytics.Config{
		Version:    version,
		Commit:     commit,
		Date:       date,
		BackendURL: config.GetBackendURL(commandDevMode(cmd)),
	})

	commandRun := rec.StartCommand(cmd, args)
	if commandRun != nil {
		ui.SetOutputObserver(commandRun.ObserveOutput)
		defer ui.SetOutputObserver(nil)
		defer func() {
			commandRun.Complete(err)
			commandRun.Flush()
		}()
	}

	return run()
}

func commandDevMode(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	return devMode
}
