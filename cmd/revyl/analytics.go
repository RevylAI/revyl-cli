package main

import (
	"sync"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/commandanalytics"
	"github.com/revyl/cli/internal/config"
)

var analyticsInstallOnce sync.Once

func installAnalytics(root *cobra.Command) {
	analyticsInstallOnce.Do(func() {
		commandanalytics.Install(root, analytics.Config{Version: version, Commit: commit, Date: date})
	})
}

func runWithAnalytics(cmd *cobra.Command, args []string, run func() error) error {
	return commandanalytics.Run(cmd, args, analytics.Config{
		Version:    version,
		Commit:     commit,
		Date:       date,
		BackendURL: config.GetBackendURL(commandDevMode(cmd)),
	}, run)
}

func commandDevMode(cmd *cobra.Command) bool {
	return commandanalytics.DevMode(cmd)
}
