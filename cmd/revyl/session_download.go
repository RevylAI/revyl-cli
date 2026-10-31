// Package main provides the session download command.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

const (
	sessionDownloadTimelineFileName   = "timeline.json"
	sessionDownloadVideoFileName      = "video.mp4"
	sessionDownloadClipsDirectoryName = "clips"
	sessionDownloadAnalyticsDomain    = "session_download"
	sessionClipCutTimeout             = 2 * time.Minute
	sessionClipFFmpegWaitDelay        = 5 * time.Second
	sessionClipFramesPerSecond        = 30
	sessionClipFFmpegErrorTailBytes   = 600

	computerSessionVideoTimelineUnsupportedCode = "computer_session_video_timeline_unsupported"
)

var (
	findFFmpegExecutable = func() (string, error) { return exec.LookPath("ffmpeg") }
	cutSessionClip       = runFFmpegSessionClip

	errSessionDownloadInterrupted = analytics.WithSafeDiagnostic(errors.New("session download interrupted; its partial files were removed, so rerun the command to download again"), "session download interrupted")
)

var sessionDownloadCmd = &cobra.Command{
	Use:   "download <session-id|execution-id>",
	Short: "Download a session's action timeline, per-action clips, and video",
	Long: `Download what happened in a device session as files a person or an agent can use.

Writes into the output directory:
  timeline.json   every recorded action (agent steps and manual tap, swipe,
                  navigate, ...) with its type, label, step outcome, and its
                  start and end in seconds into the session video
  clips/          one MP4 per timed action, with up to 1.5s before it starts
                  and after it completes. Nearby actions shrink the padding
                  between them, and the recording boundaries clamp each clip
  video.mp4       the full session video, only with --include-full-video

Accepts a device session ID or a test execution (task) ID; computer sessions
are not supported yet. Clips are cut sequentially on this machine and need
ffmpeg on PATH. Clean machines may not have ffmpeg installed: install it with
"brew install ffmpeg" on macOS, "sudo apt-get install ffmpeg" on Debian or
Ubuntu, or "winget install ffmpeg" on Windows. Use "--no-clips" for a timeline
only, or combine "--no-clips --include-full-video" for the full-video-only
fallback. The command reports recording bytes when the server provides a size,
otherwise it reports downloaded bytes without inventing a percentage.

Examples:
  revyl session download <session-id>
  revyl session download <session-id> --include-full-video
  revyl session download <task-id> --no-clips --json
  revyl session download <session-id> --output-directory ./evidence`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionDownload,
}

func init() {
	sessionDownloadCmd.Flags().String("output-directory", "", "Directory to write into (default: ./revyl-session-<id>)")
	sessionDownloadCmd.Flags().Bool("include-full-video", false, "Also download the full session video as video.mp4")
	sessionDownloadCmd.Flags().Bool("no-clips", false, "Skip per-action clips, so ffmpeg is not needed")
}

// sessionDownloadTimeline is the stable timeline.json contract. It mirrors the
// backend video timeline without the short-lived signed video URL, and
// points each action at the clip written next to it.
type sessionDownloadTimeline struct {
	ReportID           string                          `json:"report_id"`
	SessionID          *string                         `json:"session_id"`
	ExecutionID        *string                         `json:"execution_id"`
	Platform           *string                         `json:"platform"`
	VideoDuration      *float64                        `json:"video_duration"`
	VideoFilePath      *string                         `json:"video_file_path"`
	ClipPaddingSeconds float64                         `json:"clip_padding_seconds"`
	Actions            []sessionDownloadTimelineAction `json:"actions"`
}

type sessionDownloadTimelineAction struct {
	TimelineIndex           int      `json:"timeline_index"`
	StepExecutionOrder      int      `json:"step_execution_order"`
	StepType                string   `json:"step_type"`
	StepDescription         *string  `json:"step_description"`
	StepStatus              string   `json:"step_status"`
	ActionIndex             int      `json:"action_index"`
	ActionType              *string  `json:"action_type"`
	ActionLabel             string   `json:"action_label"`
	VideoTimestampStart     *float64 `json:"video_timestamp_start"`
	VideoTimestampEnd       *float64 `json:"video_timestamp_end"`
	ClipVideoTimestampStart *float64 `json:"clip_video_timestamp_start"`
	ClipVideoTimestampEnd   *float64 `json:"clip_video_timestamp_end"`
	ClipFilePath            *string  `json:"clip_file_path"`
}

type sessionDownloadResult struct {
	ReportID         string  `json:"report_id"`
	SessionID        *string `json:"session_id"`
	ExecutionID      *string `json:"execution_id"`
	OutputDirectory  string  `json:"output_directory"`
	TimelineFilePath string  `json:"timeline_file_path"`
	VideoFilePath    *string `json:"video_file_path"`
	ActionCount      int     `json:"action_count"`
	TimedActionCount int     `json:"timed_action_count"`
	ClipCount        int     `json:"clip_count"`
}

func runSessionDownload(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	outputDirectory, _ := cmd.Flags().GetString("output-directory")
	includeFullVideo, _ := cmd.Flags().GetBool("include-full-video")
	noClips, _ := cmd.Flags().GetBool("no-clips")

	sessionOrExecutionID := strings.TrimSpace(args[0])
	if !looksLikeUUID(sessionOrExecutionID) {
		return fmt.Errorf("invalid session or execution ID %q: expected a UUID", sessionOrExecutionID)
	}
	outputDirectory = strings.TrimSpace(outputDirectory)
	if outputDirectory == "" {
		outputDirectory = "revyl-session-" + sessionOrExecutionID
	}
	outputDirectory, err := filepath.Abs(outputDirectory)
	if err != nil {
		return fmt.Errorf("could not resolve output directory: %w", err)
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	ctx, stopInterruptHandling := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopInterruptHandling()

	ui.StartSpinner("Loading session timeline...")
	timeline, err := fetchSessionVideoTimeline(ctx, client, sessionOrExecutionID)
	ui.StopSpinner()
	if err != nil {
		recordSessionDownloadCompletion(cmd.Context(), "timeline_unavailable", sessionOrExecutionID, nil, sessionDownloadTimeline{}, 0)
		return err
	}

	download := newSessionDownloadTimeline(timeline)
	clipWindowCount := 0
	for _, action := range download.Actions {
		if action.ClipVideoTimestampStart != nil {
			clipWindowCount++
		}
	}
	entityID := sessionOrExecutionID
	if download.SessionID != nil {
		entityID = *download.SessionID
	}

	if err := os.MkdirAll(outputDirectory, 0o750); err != nil {
		return fmt.Errorf("could not create output directory %s: %w", outputDirectory, err)
	}
	finish := func(domainStatus string, clipCount int, failure error) error {
		recordSessionDownloadCompletion(cmd.Context(), domainStatus, entityID, timeline.Platform, download, clipCount)
		if writeErr := writeSessionDownloadTimeline(outputDirectory, download); writeErr != nil {
			return writeErr
		}
		return failure
	}

	cutClips := !noClips && clipWindowCount > 0
	ffmpegPath := ""
	if cutClips {
		ffmpegPath, err = findFFmpegExecutable()
		if err != nil {
			return finish("ffmpeg_missing", 0, analytics.WithSafeDiagnostic(fmt.Errorf(
				"wrote %s, but cutting clips needs ffmpeg, which is not on PATH. Install it (macOS: brew install ffmpeg; Debian/Ubuntu: sudo apt-get install ffmpeg; Windows: winget install ffmpeg) and rerun, or rerun with --no-clips",
				filepath.Join(outputDirectory, sessionDownloadTimelineFileName),
			), "ffmpeg not found on PATH"))
		}
	}

	if !cutClips && !includeFullVideo {
		if err := finish("downloaded", 0, nil); err != nil {
			return err
		}
		return printSessionDownloadResult(jsonOutput, outputDirectory, download, 0)
	}
	if timeline.VideoUrl == nil || strings.TrimSpace(*timeline.VideoUrl) == "" {
		return finish("video_unavailable", 0, analytics.WithSafeDiagnostic(fmt.Errorf(
			"wrote %s, but this session has no video yet; it may still be processing, so retry in a minute",
			filepath.Join(outputDirectory, sessionDownloadTimelineFileName),
		), "session video unavailable"))
	}

	videoPath, removeVideo, err := downloadSessionVideo(ctx, client, *timeline.VideoUrl, outputDirectory, includeFullVideo, jsonOutput)
	if err != nil {
		if ctx.Err() != nil {
			return finish("interrupted", 0, errSessionDownloadInterrupted)
		}
		var requestErr *url.Error
		if errors.As(err, &requestErr) {
			err = requestErr.Err
		}
		return finish("video_download_failed", 0, analytics.WithSafeDiagnostic(
			fmt.Errorf("could not download the session video: %w", err), "session video download failed"))
	}
	defer removeVideo()
	if includeFullVideo {
		videoFilePath := sessionDownloadVideoFileName
		download.VideoFilePath = &videoFilePath
	}

	clipCount := 0
	if cutClips {
		if err := os.MkdirAll(filepath.Join(outputDirectory, sessionDownloadClipsDirectoryName), 0o750); err != nil {
			return finish("clip_failed", 0, fmt.Errorf("could not create clips directory: %w", err))
		}
		ui.StartSpinner(fmt.Sprintf("Cutting %d clips...", clipWindowCount))
		for index := range download.Actions {
			action := &download.Actions[index]
			if action.ClipVideoTimestampStart == nil || action.ClipVideoTimestampEnd == nil {
				continue
			}
			clipFilePath := filepath.ToSlash(filepath.Join(sessionDownloadClipsDirectoryName, sessionClipFileName(*action)))
			if !jsonOutput {
				ui.PrintDim("Cutting clip %d/%d (%s)", clipCount+1, clipWindowCount, action.ActionLabel)
			}
			if err := cutSessionClip(ctx, ffmpegPath, videoPath, filepath.Join(outputDirectory, filepath.FromSlash(clipFilePath)), *action.ClipVideoTimestampStart, *action.ClipVideoTimestampEnd); err != nil {
				ui.StopSpinner()
				if ctx.Err() != nil {
					return finish("interrupted", clipCount, errSessionDownloadInterrupted)
				}
				return finish("clip_failed", clipCount, analytics.WithSafeDiagnostic(
					fmt.Errorf("could not cut the clip for action %d (%s): %w", action.TimelineIndex, action.ActionLabel, err), "ffmpeg clip cut failed"))
			}
			action.ClipFilePath = &clipFilePath
			clipCount++
		}
		ui.StopSpinner()
	}

	if err := finish("downloaded", clipCount, nil); err != nil {
		return err
	}
	return printSessionDownloadResult(jsonOutput, outputDirectory, download, clipCount)
}

// fetchSessionVideoTimeline resolves the ID as a device session first and as a
// test execution second, so both IDs a customer sees work.
func fetchSessionVideoTimeline(ctx context.Context, client *api.Client, sessionOrExecutionID string) (*api.ReportVideoTimelineResponse, error) {
	timeline, err := client.GetReportVideoTimelineBySession(ctx, sessionOrExecutionID)
	var apiErr *api.APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		return timeline, sessionTimelineFetchError(err)
	}
	if apiErr.DetailString("code") == computerSessionVideoTimelineUnsupportedCode {
		return nil, fmt.Errorf("%s is a computer session, and revyl session download does not support computer sessions yet; run revyl computer report %s for its report", sessionOrExecutionID, sessionOrExecutionID)
	}
	if isReportContextRouteMissing(apiErr) {
		return nil, fmt.Errorf("this Revyl backend does not serve session timelines yet; update the CLI target or retry once the release reaches it")
	}
	timeline, err = client.GetReportVideoTimelineByExecution(ctx, sessionOrExecutionID)
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no device session or test execution %s with a report was found in your organization", sessionOrExecutionID)
	}
	return timeline, sessionTimelineFetchError(err)
}

func sessionTimelineFetchError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("could not load the session timeline: %w", err)
}

func newSessionDownloadTimeline(timeline *api.ReportVideoTimelineResponse) sessionDownloadTimeline {
	download := sessionDownloadTimeline{
		ReportID:           timeline.ReportId,
		SessionID:          timeline.SessionId,
		ExecutionID:        timeline.ExecutionId,
		Platform:           timeline.Platform,
		VideoDuration:      roundedSeconds(timeline.VideoDuration),
		ClipPaddingSeconds: math.Round(float64(timeline.ClipPaddingSeconds)*1000) / 1000,
		Actions:            []sessionDownloadTimelineAction{},
	}
	if timeline.Actions == nil {
		return download
	}
	for _, action := range *timeline.Actions {
		download.Actions = append(download.Actions, sessionDownloadTimelineAction{
			TimelineIndex:           action.TimelineIndex,
			StepExecutionOrder:      action.StepExecutionOrder,
			StepType:                action.StepType,
			StepDescription:         action.StepDescription,
			StepStatus:              string(action.StepStatus),
			ActionIndex:             action.ActionIndex,
			ActionType:              action.ActionType,
			ActionLabel:             action.ActionLabel,
			VideoTimestampStart:     roundedSeconds(action.VideoTimestampStart),
			VideoTimestampEnd:       roundedSeconds(action.VideoTimestampEnd),
			ClipVideoTimestampStart: roundedSeconds(action.ClipVideoTimestampStart),
			ClipVideoTimestampEnd:   roundedSeconds(action.ClipVideoTimestampEnd),
		})
	}
	return download
}

func roundedSeconds(seconds *float32) *float64 {
	if seconds == nil {
		return nil
	}
	rounded := math.Round(float64(*seconds)*1000) / 1000
	return &rounded
}

func writeSessionDownloadTimeline(outputDirectory string, download sessionDownloadTimeline) error {
	encoded, err := json.MarshalIndent(download, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode %s: %w", sessionDownloadTimelineFileName, err)
	}
	timelinePath := filepath.Join(outputDirectory, sessionDownloadTimelineFileName)
	if err := os.WriteFile(timelinePath, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("could not write %s: %w", timelinePath, err)
	}
	return nil
}

// downloadSessionVideo saves the session video as video.mp4 when the full
// video was requested, otherwise to a hidden file removed after clipping.
func downloadSessionVideo(ctx context.Context, client *api.Client, videoURL, outputDirectory string, keepFullVideo, jsonOutput bool) (string, func(), error) {
	progress := newSessionVideoDownloadProgress(jsonOutput)
	if keepFullVideo {
		videoPath := filepath.Join(outputDirectory, sessionDownloadVideoFileName)
		ui.PrintInfo("Downloading session video...")
		err := client.DownloadFileFromURLWithProgress(ctx, videoURL, videoPath, progress.Report)
		return videoPath, func() {}, err
	}
	scratch, err := os.CreateTemp(outputDirectory, ".revyl-session-video-*.mp4")
	if err != nil {
		return "", func() {}, err
	}
	videoPath := scratch.Name()
	scratch.Close()
	removeVideo := func() { _ = os.Remove(videoPath) }
	ui.PrintInfo("Downloading session video...")
	err = client.DownloadFileFromURLWithProgress(ctx, videoURL, videoPath, progress.Report)
	if err != nil {
		removeVideo()
		return "", func() {}, err
	}
	return videoPath, removeVideo, nil
}

type sessionVideoDownloadProgress struct {
	jsonOutput   bool
	lastReportAt time.Time
}

func newSessionVideoDownloadProgress(jsonOutput bool) *sessionVideoDownloadProgress {
	return &sessionVideoDownloadProgress{jsonOutput: jsonOutput}
}

func (p *sessionVideoDownloadProgress) Report(downloadedBytes, totalBytes int64) {
	if p.jsonOutput || (downloadedBytes != totalBytes && time.Since(p.lastReportAt) < time.Second) {
		return
	}
	p.lastReportAt = time.Now()
	if totalBytes > 0 {
		percentage := int(downloadedBytes * 100 / totalBytes)
		if percentage > 100 {
			percentage = 100
		}
		ui.PrintInfo("Downloading session video: %s / %s (%d%%)", formatSessionDownloadBytes(downloadedBytes), formatSessionDownloadBytes(totalBytes), percentage)
		return
	}
	ui.PrintDim("Downloading session video (%s; total size unavailable)", formatSessionDownloadBytes(downloadedBytes))
}

func formatSessionDownloadBytes(bytes int64) string {
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(bytes)/(1024*1024))
}

func sessionClipFileName(action sessionDownloadTimelineAction) string {
	actionType := "action"
	if action.ActionType != nil && strings.TrimSpace(*action.ActionType) != "" {
		actionType = *action.ActionType
	}
	var slug strings.Builder
	for _, character := range strings.ToLower(actionType) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' {
			slug.WriteRune(character)
		} else {
			slug.WriteRune('_')
		}
	}
	return fmt.Sprintf("%03d-%s.mp4", action.TimelineIndex, slug.String())
}

// sessionClipFFmpegArguments cuts the exact window from a session video.
// Device recorders write frames only when the screen changes, so a static
// screen can leave seconds without a frame. Decoding therefore starts at the
// keyframe before the window with original timestamps, and the fps filter
// repeats the frame on screen through each gap before trim cuts the window;
// an accurate seek alone would drop that frame and start the clip late.
// -start_at_zero makes those timestamps count from the recording's start, as
// the timeline offsets and -ss do, even when its first timestamp is not zero.
func sessionClipFFmpegArguments(videoPath, clipPath string, clipStartSeconds, clipEndSeconds float64) []string {
	clipStart := strconv.FormatFloat(clipStartSeconds, 'f', 3, 64)
	clipEnd := strconv.FormatFloat(clipEndSeconds, 'f', 3, 64)
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-progress", "pipe:1", "-y",
		"-noaccurate_seek", "-copyts", "-start_at_zero", "-ss", clipStart,
		"-i", videoPath,
		"-map", "0:v:0", "-an",
		"-vf", fmt.Sprintf("fps=%d,trim=start=%s:end=%s,setpts=PTS-STARTPTS,scale=trunc(iw/2)*2:trunc(ih/2)*2", sessionClipFramesPerSecond, clipStart, clipEnd),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		clipPath,
	}
}

func runFFmpegSessionClip(ctx context.Context, ffmpegPath, videoPath, clipPath string, clipStartSeconds, clipEndSeconds float64) error {
	cutTimeout := sessionClipCutTimeout + time.Duration(math.Max(0, clipEndSeconds-clipStartSeconds)*2*float64(time.Second))
	cutContext, cancel := context.WithTimeout(ctx, cutTimeout)
	defer cancel()
	var progress, diagnostics bytes.Buffer
	// #nosec G204 -- ffmpegPath comes from exec.LookPath("ffmpeg") and sessionClipFFmpegArguments builds a fixed argv, run without a shell.
	command := exec.CommandContext(cutContext, ffmpegPath, sessionClipFFmpegArguments(videoPath, clipPath, clipStartSeconds, clipEndSeconds)...)
	command.Stdout = &progress
	command.Stderr = &diagnostics
	command.WaitDelay = sessionClipFFmpegWaitDelay
	err := command.Run()
	if err == nil && sessionClipEncodedFrameCount(progress.String()) > 0 {
		return nil
	}
	_ = os.Remove(clipPath)
	if err == nil {
		return fmt.Errorf("the recording has no video frames between %.3fs and %.3fs; it may end before this action", clipStartSeconds, clipEndSeconds)
	}
	if errors.Is(cutContext.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("ffmpeg did not finish within %s", cutTimeout)
	}
	detail := strings.TrimSpace(diagnostics.String())
	if len(detail) > sessionClipFFmpegErrorTailBytes {
		detail = detail[len(detail)-sessionClipFFmpegErrorTailBytes:]
	}
	if detail == "" {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}
	return fmt.Errorf("ffmpeg failed: %w: %s", err, detail)
}

// sessionClipEncodedFrameCount reads the last frame count ffmpeg reported on
// -progress. ffmpeg exits 0 even when the window holds no frame, writing an
// unplayable clip, so only this count shows that a cut produced footage.
func sessionClipEncodedFrameCount(progress string) int {
	frameCount := 0
	for _, line := range strings.Split(progress, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "frame="); found {
			frameCount, _ = strconv.Atoi(value)
		}
	}
	return frameCount
}

func recordSessionDownloadCompletion(ctx context.Context, domainStatus, entityID string, platform *string, download sessionDownloadTimeline, clipCount int) {
	properties := map[string]interface{}{
		"session_download_action_count": len(download.Actions),
		"session_download_clip_count":   clipCount,
	}
	if looksLikeUUID(entityID) {
		properties["entity_id"] = entityID
	}
	if platform != nil && strings.TrimSpace(*platform) != "" {
		properties["session_download_platform"] = strings.TrimSpace(*platform)
	}
	analytics.SetCommandCompletion(ctx, analytics.CommandCompletion{
		Domain:       sessionDownloadAnalyticsDomain,
		DomainStatus: domainStatus,
		Properties:   properties,
	})
}

func printSessionDownloadResult(jsonOutput bool, outputDirectory string, download sessionDownloadTimeline, clipCount int) error {
	timedActionCount := 0
	timedActionWithoutClipWindowCount := 0
	for _, action := range download.Actions {
		if action.VideoTimestampStart == nil {
			continue
		}
		timedActionCount++
		if action.ClipVideoTimestampStart == nil {
			timedActionWithoutClipWindowCount++
		}
	}
	result := sessionDownloadResult{
		ReportID:         download.ReportID,
		SessionID:        download.SessionID,
		ExecutionID:      download.ExecutionID,
		OutputDirectory:  outputDirectory,
		TimelineFilePath: filepath.Join(outputDirectory, sessionDownloadTimelineFileName),
		ActionCount:      len(download.Actions),
		TimedActionCount: timedActionCount,
		ClipCount:        clipCount,
	}
	if download.VideoFilePath != nil {
		videoFilePath := filepath.Join(outputDirectory, *download.VideoFilePath)
		result.VideoFilePath = &videoFilePath
	}

	if jsonOutput {
		encoded, err := marshalPrettyJSON(result)
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Downloaded session timeline to %s", outputDirectory)
	ui.PrintKeyValue("Timeline", fmt.Sprintf("%s (%d actions, %d timed)", sessionDownloadTimelineFileName, result.ActionCount, timedActionCount))
	if clipCount > 0 {
		ui.PrintKeyValue("Clips", fmt.Sprintf("%s/ (%d)", sessionDownloadClipsDirectoryName, clipCount))
	}
	if download.VideoFilePath != nil {
		ui.PrintKeyValue("Video", *download.VideoFilePath)
	}
	if timedActionCount < result.ActionCount {
		ui.PrintDim("No video timestamps, so no clip: %d of %d actions", result.ActionCount-timedActionCount, result.ActionCount)
	}
	if timedActionWithoutClipWindowCount > 0 {
		ui.PrintDim("Timed, but outside the video, so no clip: %d of %d actions", timedActionWithoutClipWindowCount, result.ActionCount)
	}
	return nil
}
