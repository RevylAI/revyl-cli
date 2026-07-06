package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var (
	devLogsBuild  bool
	devLogsFollow bool
)

var devLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Stream logs from the running dev session",
	Long: `Stream logs for the current dev context.

With --build, streams the remote build runner output for the most recent
build (initial or rebuild) without leaving the terminal. Add --follow to keep
streaming until the build reaches a terminal state.`,
	Example: `  revyl dev logs --build
  revyl dev logs --build --follow`,
	RunE: runDevLogs,
}

func runDevLogs(cmd *cobra.Command, args []string) error {
	if !devLogsBuild {
		return fmt.Errorf("specify --build to stream remote build logs (device logs: `revyl device logs`)")
	}

	cwd, err := resolveDevCwd()
	if err != nil {
		return err
	}
	ctxName, err := resolveDevContextName(cwd, getDevContextFlag(cmd))
	if err != nil {
		return err
	}

	jobID, err := currentDevBuildJobID(cwd, ctxName)
	if err != nil {
		return err
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	return streamRemoteBuildLogs(cmd.Context(), client, jobID, devLogsFollow)
}

// currentDevBuildJobID reads the most recent remote build job id from the dev
// context status file.
func currentDevBuildJobID(cwd, ctxName string) (string, error) {
	data, err := os.ReadFile(devCtxStatusPath(cwd, ctxName))
	if err != nil {
		return "", fmt.Errorf("no dev status for context %q — is `revyl dev` running here?", ctxName)
	}
	var ds devStatus
	if err := json.Unmarshal(data, &ds); err != nil {
		return "", fmt.Errorf("could not parse dev status: %w", err)
	}
	if ds.LastRebuild == nil || strings.TrimSpace(ds.LastRebuild.RemoteJobID) == "" {
		return "", fmt.Errorf("no remote build recorded for context %q (local builds have no remote logs)", ctxName)
	}
	return strings.TrimSpace(ds.LastRebuild.RemoteJobID), nil
}

// streamRemoteBuildLogs prints remote build runner logs from the beginning.
// With follow, it keeps polling until the job reaches a terminal state.
func streamRemoteBuildLogs(ctx context.Context, client *api.Client, jobID string, follow bool) error {
	formatter := &remoteBuildLogFormatter{}
	cursor := "0-0"

	printAvailable := func() error {
		for {
			logs, err := client.GetRemoteBuildLogs(ctx, jobID, cursor)
			if err != nil {
				return err
			}
			printed := 0
			if logs.Events != nil {
				for _, event := range *logs.Events {
					formatter.Print(event)
					printed++
				}
			}
			if logs.NextCursor != nil && *logs.NextCursor != "" {
				cursor = *logs.NextCursor
			}
			if printed == 0 {
				return nil
			}
		}
	}

	if err := printAvailable(); err != nil {
		return fmt.Errorf("failed to fetch build logs: %w", err)
	}
	if !follow {
		return nil
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := printAvailable(); err != nil {
				ui.PrintDebug("failed to fetch build logs: %v", err)
				continue
			}
			status, err := client.GetRemoteBuildStatus(ctx, jobID)
			if err != nil {
				ui.PrintDebug("failed to poll build status: %v", err)
				continue
			}
			switch status.Status {
			case "success", "failed", "cancelled":
				_ = printAvailable()
				ui.PrintDim("Build %s (%s)", status.Status, jobID)
				return nil
			}
		}
	}
}

func init() {
	devLogsCmd.Flags().BoolVar(&devLogsBuild, "build", false, "Stream remote build runner logs for the latest build")
	devLogsCmd.Flags().BoolVar(&devLogsFollow, "follow", false, "Keep streaming until the build completes")
	devCmd.AddCommand(devLogsCmd)
}
