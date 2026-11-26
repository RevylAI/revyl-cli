package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

var deviceRecordCmd = &cobra.Command{
	Use:   "record",
	Short: "Record part of a running iOS or Android session as an MP4",
	Long: `Record a span of a running iOS or Android session without ending it.
Start, do the steps you want on video, then stop: stop saves that span as an
MP4 within seconds and the session keeps running. Run as many start/stop
cycles as you like, one recording at a time per session.

A recording stops on its own after 3 minutes or about 160 MiB, whichever comes
first; the busiest iOS screens reach the size limit after about a minute. Stopping the
session discards a recording that was not stopped. The full-session video is
unchanged and still comes from 'revyl session download' after the session ends.`,
}

var deviceRecordStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start recording the device screen",
	Example: `  revyl device record start
  revyl device record start -s 1 --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionTarget(cmd, mgr)
		if err != nil {
			return err
		}
		start, err := mgr.StartScreenRecording(cmd.Context(), session)
		if err != nil {
			recordScreenRecordingOutcome(cmd, session, mcppkg.ScreenRecordingFailureStatus(err))
			return err
		}
		recordScreenRecordingOutcome(cmd, session, "started")
		jsonOrPrint(cmd, start, fmt.Sprintf(
			"Recording started (%s). Run 'revyl device record stop' to save the MP4; it stops on its own after %d minutes.",
			start.RecordingID, start.MaxSeconds/60,
		))
		return nil
	},
}

var deviceRecordStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop recording and save the MP4",
	Example: `  revyl device record stop
  revyl device record stop --output checkout-bug.mp4
  revyl device record stop --recording-id <recording-id> --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionTarget(cmd, mgr)
		if err != nil {
			return err
		}
		recordingID, _ := cmd.Flags().GetString("recording-id")
		output, _ := cmd.Flags().GetString("output")
		if output == "" {
			output = "."
		}
		clip, err := mgr.StopScreenRecording(cmd.Context(), session, recordingID, output)
		if err != nil {
			recordScreenRecordingOutcome(cmd, session, mcppkg.ScreenRecordingFailureStatus(err))
			return err
		}
		recordScreenRecordingOutcome(cmd, session, mcppkg.ScreenRecordingSavedStatus(clip))
		jsonOrPrint(cmd, clip, fmt.Sprintf("Saved %.1f s recording (%.1f MB) to %s", clip.DurationSeconds, float64(clip.SizeBytes)/1e6, clip.Path))
		if clip.Warning != "" {
			ui.PrintWarning("%s", clip.Warning)
		}
		if clip.LimitReached {
			ui.PrintWarning("The recording reached its 3-minute or size limit and stopped early; record shorter spans to capture everything.")
		}
		return nil
	},
}

func recordScreenRecordingOutcome(cmd *cobra.Command, session *mcppkg.DeviceSession, status string) {
	completion := analytics.CommandCompletion{Domain: mcppkg.ScreenRecordingAnalyticsDomain, DomainStatus: status}
	if session != nil && session.SessionID != "" {
		completion.Properties = map[string]interface{}{"entity_id": session.SessionID}
	}
	analytics.SetCommandCompletion(cmd.Context(), completion)
}

func init() {
	for _, cmd := range []*cobra.Command{deviceRecordStartCmd, deviceRecordStopCmd} {
		cmd.Flags().Bool("json", false, "Output as JSON")
		registerSessionTargetFlags(cmd)
		deviceRecordCmd.AddCommand(cmd)
	}
	deviceRecordStopCmd.Flags().String("recording-id", "", "Recording to stop (default: the one last started from this directory on the session)")
	deviceRecordStopCmd.Flags().StringP("output", "o", "", "File or existing directory to save the MP4 to (default: a new revyl-recording-<timestamp>-<id>.mp4 in the current directory)")
	deviceCmd.AddCommand(deviceRecordCmd)
}
