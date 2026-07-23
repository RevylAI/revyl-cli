package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var (
	devLogsBuild   bool
	devLogsFollow  bool
	devLogsTimeout int
)

var devLogsJobPollInterval = 500 * time.Millisecond

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

	timeout := devLogsTimeout
	if timeout <= 0 {
		timeout = 300
	}
	jobID, err := resolveDevBuildJobID(
		cmd.Context(),
		cwd,
		ctxName,
		devLogsFollow,
		time.Duration(timeout)*time.Second,
	)
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

type devBuildJobRegistration struct {
	BuildMode  string
	Status     string
	JobID      string
	HasRebuild bool
}

type devBuildStatusReadError struct {
	contextName string
	cause       error
}

// Error describes an unavailable dev status without exposing filesystem details.
func (e *devBuildStatusReadError) Error() string {
	return fmt.Sprintf("no dev status for context %q — is `revyl dev` running here?", e.contextName)
}

// Unwrap exposes the filesystem cause for retry classification.
func (e *devBuildStatusReadError) Unwrap() error {
	return e.cause
}

// resolveDevBuildJobID resolves a registered remote build, waiting when follow is enabled.
//
// Parameters:
//   - ctx: Cancellation context
//   - cwd: Project root
//   - ctxName: Dev context name
//   - follow: Whether to wait for an in-flight registration
//   - timeout: Maximum registration wait
//
// Returns:
//   - string: Registered remote job identifier
//   - error: Invalid context state, cancellation, or timeout
//
// Edge cases:
//   - With follow enabled, a missing status snapshot is retried only while its
//     dev context is running because Windows file replacement can briefly hide it.
func resolveDevBuildJobID(ctx context.Context, cwd, ctxName string, follow bool, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()
	pollTicker := time.NewTicker(devLogsJobPollInterval)
	defer pollTicker.Stop()

	// Retained so a status file we never manage to read is reported as the read
	// failure it is, rather than as a bare timeout.
	var lastReadErr error

	for {
		registration, err := readDevBuildJobRegistration(cwd, ctxName)
		if err != nil {
			if !follow || !shouldRetryDevBuildStatusRead(cwd, ctxName, err) {
				return "", err
			}
			lastReadErr = err
		} else {
			lastReadErr = nil
			if registration.JobID != "" {
				return registration.JobID, nil
			}
			if registration.BuildMode != "remote" {
				return "", fmt.Errorf("no remote build recorded for context %q (local builds have no remote logs)", ctxName)
			}
			if !registration.HasRebuild {
				return "", fmt.Errorf("no remote build recorded for context %q", ctxName)
			}
			if !devCockpitRebuildRunningStatus(registration.Status) {
				return "", fmt.Errorf("remote build for context %q ended with status %q without a job id", ctxName, registration.Status)
			}
			if !follow {
				return "", fmt.Errorf("remote build is starting and its job id is not available yet; retry shortly or use --follow")
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeoutTimer.C:
			if lastReadErr != nil {
				return "", fmt.Errorf("remote build job id not available after %s: %w", timeout, lastReadErr)
			}
			return "", fmt.Errorf("remote build job id not available after %s", timeout)
		case <-pollTicker.C:
		}
	}
}

// shouldRetryDevBuildStatusRead identifies transient status read gaps for a live dev context.
//
// Parameters:
//   - cwd: Project root
//   - ctxName: Dev context name
//   - err: Status read failure
//
// Returns:
//   - bool: Whether the read should be retried
func shouldRetryDevBuildStatusRead(cwd, ctxName string, err error) bool {
	var readErr *devBuildStatusReadError
	if !errors.As(err, &readErr) {
		return false
	}
	// Status publication and context startup can briefly leave the snapshot
	// unavailable. Treat failures for a live context as transient and let the
	// caller's timeout bound the wait; stopped contexts still fail immediately.
	devContext, loadErr := loadDevContext(cwd, ctxName)
	if loadErr != nil {
		return false
	}
	return isDevContextRunning(cwd, devContext)
}

// readDevBuildJobRegistration reads remote build registration state from a dev status file.
//
// Parameters:
//   - cwd: Project root
//   - ctxName: Dev context name
//
// Returns:
//   - devBuildJobRegistration: Current registration state
//   - error: Missing or malformed status file
func readDevBuildJobRegistration(cwd, ctxName string) (devBuildJobRegistration, error) {
	data, err := readDevStatusFile(devCtxStatusPath(cwd, ctxName))
	if err != nil {
		return devBuildJobRegistration{}, &devBuildStatusReadError{
			contextName: ctxName,
			cause:       err,
		}
	}
	var ds devStatus
	if err := json.Unmarshal(data, &ds); err != nil {
		return devBuildJobRegistration{}, fmt.Errorf("could not parse dev status: %w", err)
	}
	registration := devBuildJobRegistration{
		BuildMode: strings.ToLower(strings.TrimSpace(ds.BuildMode)),
	}
	if ds.LastRebuild == nil {
		return registration, nil
	}
	registration.HasRebuild = true
	registration.Status = strings.ToLower(strings.TrimSpace(ds.LastRebuild.Status))
	registration.JobID = strings.TrimSpace(ds.LastRebuild.RemoteJobID)
	return registration, nil
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
	devLogsCmd.Flags().IntVar(&devLogsTimeout, "timeout", 300, "Seconds to wait for a remote build job to register (with --follow)")
	devCmd.AddCommand(devLogsCmd)
}
