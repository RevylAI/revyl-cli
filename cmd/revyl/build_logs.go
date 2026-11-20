package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var (
	buildLogsFollow bool
	buildLogsJSON   bool
)

var buildLogsCmd = &cobra.Command{
	Use:   "logs <build-job-id>",
	Short: "Print a remote build's full log",
	Long: `Print every log event of a remote build job from the beginning.

Use --follow to keep streaming until the build reaches a terminal state, and
--json to print each event as one JSON object per line.`,
	Example: `  revyl build logs <build-job-id>
  revyl build logs <build-job-id> --follow
  revyl build logs <build-job-id> --json > build-log.ndjson`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runBuildLogs,
}

func runBuildLogs(cmd *cobra.Command, args []string) error {
	jsonOutput := buildLogsJSON
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		jsonOutput = true
	}
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	printEvent := (&remoteBuildLogFormatter{}).Print
	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
		encoder := json.NewEncoder(os.Stdout)
		printEvent = func(event api.RemoteBuildLogEvent) { _ = encoder.Encode(event) }
	}
	return streamRemoteBuildLogs(cmd.Context(), client, strings.TrimSpace(args[0]), buildLogsFollow, printEvent)
}

func init() {
	buildLogsCmd.Flags().BoolVar(&buildLogsFollow, "follow", false, "Keep streaming until the build completes")
	buildLogsCmd.Flags().BoolVar(&buildLogsJSON, "json", false, "Print each log event as a JSON object per line")
	buildCmd.AddCommand(buildLogsCmd)
}
