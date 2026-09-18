package main

import (
	"os"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/commandanalytics"
	"github.com/revyl/cli/internal/ui"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:          "revyl-computer",
	Short:        "Access your organization's Revyl computer",
	SilenceUsage: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		if debug {
			log.SetLevel(log.DebugLevel)
		}
		ui.SetDebugMode(debug)
		quiet, _ := cmd.Flags().GetBool("quiet")
		ui.SetQuietMode(quiet)
		api.SetDefaultVersion(version)
		api.SetDebugLogger(ui.PrintDebug)
	},
}

func init() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate("revyl-computer version {{.Version}}\n")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
	rootCmd.PersistentFlags().Bool("dev", false, "Use local development servers")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "Suppress non-essential output")
	_ = rootCmd.PersistentFlags().MarkHidden("dev")
	rootCmd.AddCommand(sshCmd)
}

func main() {
	if analytics.IsTelemetryHelper() {
		analytics.RunTelemetryHelper(os.Stdin)
		return
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		ui.PrintDebug("telemetry export failed: %v", err)
	}))
	commandanalytics.Install(rootCmd, analytics.Config{Version: version, Commit: commit, Date: date})
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
