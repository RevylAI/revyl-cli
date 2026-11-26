package mcp

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/privatefs"
)

// ScreenRecordingAnalyticsDomain names on-demand recording outcomes on CLI
// lifecycle and mcp_tool_called events.
const ScreenRecordingAnalyticsDomain = "device_screen_recording"

// ErrScreenRecordingWorkerOutdated reports a device worker released before
// on-demand recording existed.
var ErrScreenRecordingWorkerOutdated = errors.New("this session's device worker does not support on-demand recording yet; the full-session video is still available from 'revyl session download <session-id> --include-full-video' after the session ends")

const screenRecordingStateDir = "recordings"

var screenRecordingStateNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ScreenRecordingStart describes a recording the worker has started.
type ScreenRecordingStart struct {
	RecordingID string    `json:"recording_id"`
	SessionID   string    `json:"session_id"`
	StartedAt   time.Time `json:"started_at"`
	MaxSeconds  int       `json:"max_seconds"`
	MaxBytes    int       `json:"max_bytes"`
}

// ScreenRecordingClip describes a stopped recording saved as an MP4.
type ScreenRecordingClip struct {
	RecordingID     string  `json:"recording_id"`
	SessionID       string  `json:"session_id"`
	Path            string  `json:"path"`
	SizeBytes       int     `json:"size_bytes"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	LimitReached    bool    `json:"limit_reached"`
	Warning         string  `json:"warning,omitempty"`
}

// StartScreenRecording starts the session's on-demand recorder, separate from
// the full-session recording, and remembers its ID so a later stop can omit it.
func (m *DeviceSessionManager) StartScreenRecording(ctx context.Context, session *DeviceSession) (ScreenRecordingStart, error) {
	if session == nil {
		return ScreenRecordingStart{}, fmt.Errorf("no session resolved for screen recording")
	}
	body, err := m.screenRecordingRequest(ctx, session, "/start_screen_recording", map[string]any{})
	if err != nil {
		return ScreenRecordingStart{}, err
	}
	var resp api.DeviceStartScreenRecordingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ScreenRecordingStart{}, fmt.Errorf("failed to parse worker start_screen_recording response: %w", err)
	}
	if !resp.Success || resp.RecordingID == "" {
		reason := strings.TrimSpace(resp.Error)
		if reason == "" {
			reason = "the worker did not return a recording"
		}
		if strings.Contains(reason, "already active") {
			reason += "; stop the active recording before starting another"
		}
		return ScreenRecordingStart{}, fmt.Errorf("screen recording did not start: %s", reason)
	}
	start := ScreenRecordingStart{
		RecordingID: resp.RecordingID,
		SessionID:   session.SessionID,
		StartedAt:   time.Now().UTC(),
		MaxSeconds:  api.ScreenRecordingMaxSeconds,
		MaxBytes:    api.ScreenRecordingMaxBytes,
	}
	if err := m.saveActiveScreenRecording(start); err != nil {
		return start, fmt.Errorf("screen recording %s started, but its ID could not be saved locally (%v); pass it explicitly when you stop", start.RecordingID, err)
	}
	return start, nil
}

// StopScreenRecording finalizes a recording and writes the MP4 to outputPath,
// to a new uniquely named file when outputPath is a directory, or under the work
// directory's .revyl/mcp/recordings when it is empty. The session keeps running. An empty recordingID uses the one StartScreenRecording saved.
func (m *DeviceSessionManager) StopScreenRecording(ctx context.Context, session *DeviceSession, recordingID, outputPath string) (ScreenRecordingClip, error) {
	if session == nil {
		return ScreenRecordingClip{}, fmt.Errorf("no session resolved for screen recording")
	}
	recordingID = strings.TrimSpace(recordingID)
	if recordingID == "" {
		saved, ok := m.loadActiveScreenRecording(session.SessionID)
		if !ok {
			return ScreenRecordingClip{}, fmt.Errorf("no recording started from this directory is active on session %s; pass the recording ID that start returned", session.SessionID)
		}
		recordingID = saved.RecordingID
	}
	if strings.TrimSpace(outputPath) != "" {
		absolutePath, err := filepath.Abs(outputPath)
		if info, statErr := os.Stat(absolutePath); err == nil && (statErr != nil || !info.IsDir()) {
			err = os.MkdirAll(filepath.Dir(absolutePath), 0o750)
		}
		if err != nil {
			return ScreenRecordingClip{}, fmt.Errorf("screen recording %s is still running; choose another output path: %w", recordingID, err)
		}
		outputPath = absolutePath
	}
	body, err := m.screenRecordingRequest(ctx, session, "/stop_screen_recording", api.DeviceStopScreenRecordingRequest{RecordingID: recordingID})
	if err != nil {
		return ScreenRecordingClip{}, fmt.Errorf("screen recording %s was not confirmed stopped: %w", recordingID, err)
	}
	if len(body) < 12 || string(body[4:8]) != "ftyp" {
		err := EnsureWorkerActionSucceeded(body, "stop_screen_recording")
		if err == nil {
			err = errors.New("the worker returned no video")
		}
		if !strings.Contains(err.Error(), "recording retained") {
			m.clearActiveScreenRecording(session.SessionID, recordingID)
		}
		return ScreenRecordingClip{}, fmt.Errorf("screen recording %s was not saved: %w", recordingID, err)
	}
	m.clearActiveScreenRecording(session.SessionID, recordingID)
	warning := ""
	path, err := m.writeScreenRecording(outputPath, recordingID, body)
	if err != nil && outputPath != "" {
		warning = fmt.Sprintf("could not write to %s (%v); saved the recording under .revyl/mcp/recordings instead", outputPath, err)
		path, err = m.writeScreenRecording("", recordingID, body)
	}
	if err != nil {
		return ScreenRecordingClip{}, fmt.Errorf("screen recording %s was received but could not be saved: %w", recordingID, err)
	}
	duration := mp4DurationSeconds(body)
	return ScreenRecordingClip{
		Warning:         warning,
		RecordingID:     recordingID,
		SessionID:       session.SessionID,
		Path:            path,
		SizeBytes:       len(body),
		DurationSeconds: math.Round(duration*10) / 10,
		LimitReached:    duration >= api.ScreenRecordingMaxSeconds-1 || len(body) >= api.ScreenRecordingMaxBytes*85/100,
	}, nil
}

// ScreenRecordingFailureStatus classifies a recording failure for analytics.
func ScreenRecordingFailureStatus(err error) string {
	switch {
	case errors.Is(err, ErrScreenRecordingWorkerOutdated):
		return "worker_outdated"
	case strings.Contains(err.Error(), "unsupported on this device"), strings.Contains(err.Error(), "not available on platform"):
		return "unsupported_device"
	default:
		return "failed"
	}
}

func (m *DeviceSessionManager) screenRecordingRequest(ctx context.Context, session *DeviceSession, path string, body any) ([]byte, error) {
	respBody, err := m.workerRequestForSession(ctx, session, path, body)
	var workerErr *WorkerHTTPError
	if errors.As(err, &workerErr) && workerErr.StatusCode == http.StatusNotFound {
		var notFound struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal([]byte(workerErr.Body), &notFound) == nil && notFound.Detail == "Not Found" {
			return nil, ErrScreenRecordingWorkerOutdated
		}
	}
	if err != nil {
		return nil, m.explainWorkerSessionState(session, err)
	}
	return respBody, nil
}

func (m *DeviceSessionManager) writeScreenRecording(outputPath, recordingID string, video []byte) (string, error) {
	fileName := fmt.Sprintf("revyl-recording-%s-%s.mp4", time.Now().Format("20060102-150405"), recordingID[:min(8, len(recordingID))])
	if outputPath == "" {
		return m.writePNGArtifact(screenRecordingStateDir, fileName, video)
	}
	info, err := os.Stat(outputPath)
	if err != nil || !info.IsDir() {
		return outputPath, os.WriteFile(outputPath, video, 0o600)
	}
	directory, err := os.OpenRoot(outputPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = directory.Close() }()
	file, err := directory.OpenFile(fileName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(video); err != nil {
		return "", errors.Join(err, file.Close(), directory.Remove(fileName))
	}
	return filepath.Join(outputPath, fileName), file.Close()
}

func (m *DeviceSessionManager) saveActiveScreenRecording(start ScreenRecordingStart) error {
	if !screenRecordingStateNamePattern.MatchString(start.SessionID) {
		return fmt.Errorf("the session has no usable ID")
	}
	data, err := json.Marshal(start)
	if err != nil {
		return err
	}
	_, err = m.writePNGArtifact(screenRecordingStateDir, start.SessionID+".json", data)
	return err
}

func (m *DeviceSessionManager) loadActiveScreenRecording(sessionID string) (ScreenRecordingStart, bool) {
	if !screenRecordingStateNamePattern.MatchString(sessionID) {
		return ScreenRecordingStart{}, false
	}
	directory, err := privatefs.OpenDirectory(m.artifactRootDirectory(), filepath.Join(".revyl", "mcp", screenRecordingStateDir))
	if err != nil {
		return ScreenRecordingStart{}, false
	}
	defer func() { _ = directory.Close() }()
	data, err := directory.ReadFile(sessionID + ".json")
	if err != nil || len(data) > 4096 {
		return ScreenRecordingStart{}, false
	}
	var saved ScreenRecordingStart
	if json.Unmarshal(data, &saved) != nil || saved.SessionID != sessionID || saved.RecordingID == "" {
		return ScreenRecordingStart{}, false
	}
	return saved, true
}

func (m *DeviceSessionManager) clearActiveScreenRecording(sessionID, recordingID string) {
	saved, ok := m.loadActiveScreenRecording(sessionID)
	if !ok || saved.RecordingID != recordingID {
		return
	}
	directory, err := privatefs.OpenDirectory(m.artifactRootDirectory(), filepath.Join(".revyl", "mcp", screenRecordingStateDir))
	if err != nil {
		return
	}
	defer func() { _ = directory.Close() }()
	_ = directory.Remove(sessionID + ".json")
}

func mp4DurationSeconds(video []byte) float64 {
	mvhd := mp4ChildBox(mp4ChildBox(video, "moov"), "mvhd")
	var timescale uint32
	var duration uint64
	switch {
	case len(mvhd) >= 20 && mvhd[0] == 0:
		timescale = binary.BigEndian.Uint32(mvhd[12:16])
		duration = uint64(binary.BigEndian.Uint32(mvhd[16:20]))
	case len(mvhd) >= 32 && mvhd[0] == 1:
		timescale = binary.BigEndian.Uint32(mvhd[20:24])
		duration = binary.BigEndian.Uint64(mvhd[24:32])
	}
	if timescale == 0 {
		return 0
	}
	return float64(duration) / float64(timescale)
}

func mp4ChildBox(data []byte, boxType string) []byte {
	for len(data) >= 8 {
		size, header := uint64(binary.BigEndian.Uint32(data[0:4])), uint64(8)
		switch size {
		case 0:
			size = uint64(len(data))
		case 1:
			if len(data) < 16 {
				return nil
			}
			size, header = binary.BigEndian.Uint64(data[8:16]), 16
		}
		if size < header || size > uint64(len(data)) {
			return nil
		}
		if string(data[4:8]) == boxType {
			return data[header:size]
		}
		data = data[size:]
	}
	return nil
}

type DeviceRecordStartInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID; never falls back to the active session. If session_index is also supplied, both must identify the same session."`
}

type DeviceRecordStartOutput struct {
	Success     bool       `json:"success"`
	RecordingID string     `json:"recording_id,omitempty"`
	SessionID   string     `json:"session_id,omitempty"`
	MaxSeconds  int        `json:"max_seconds,omitempty"`
	MaxBytes    int        `json:"max_bytes,omitempty"`
	Error       string     `json:"error,omitempty"`
	NextSteps   []NextStep `json:"next_steps,omitempty"`
}

type DeviceRecordStopInput struct {
	RecordingID  string `json:"recording_id,omitempty" jsonschema:"ID that device_record_start returned. Omit to stop the recording last started from this directory on the session."`
	OutputPath   string `json:"output_path,omitempty" jsonschema:"File or existing directory to write the MP4 to. Omit to save it under .revyl/mcp/recordings in the working directory."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID; never falls back to the active session. If session_index is also supplied, both must identify the same session."`
}

type DeviceRecordStopOutput struct {
	Success         bool       `json:"success"`
	RecordingID     string     `json:"recording_id,omitempty"`
	SessionID       string     `json:"session_id,omitempty"`
	Path            string     `json:"path,omitempty"`
	SizeBytes       int        `json:"size_bytes,omitempty"`
	DurationSeconds float64    `json:"duration_seconds,omitempty"`
	LimitReached    bool       `json:"limit_reached,omitempty"`
	Warning         string     `json:"warning,omitempty"`
	Error           string     `json:"error,omitempty"`
	NextSteps       []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) registerDeviceRecordTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_record_start",
		Description: "Start an on-demand screen recording of a running iOS or Android session, separate from the full-session recording, which is unchanged. One recording at a time per session; it stops on its own after 180 seconds or about 160 MiB (the busiest iOS screens reach the size limit after about a minute). Call device_record_stop to save the MP4; stopping the session first discards it. Not available on Web sessions.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Start Screen Recording",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceRecordStart)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_record_stop",
		Description: "Stop the on-demand screen recording that device_record_start began and save just that span as an MP4 on this machine within seconds. The session keeps running, so you can start another recording. Returns the file path, size, duration and limit_reached (true when the recording hit its time or size limit and stopped early).",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Stop Screen Recording",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceRecordStop)
}

func (s *Server) handleDeviceRecordStart(ctx context.Context, req *mcp.CallToolRequest, input DeviceRecordStartInput) (*mcp.CallToolResult, DeviceRecordStartOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID, "start_screen_recording")
	var start ScreenRecordingStart
	if err == nil {
		start, err = s.sessionMgr.StartScreenRecording(ctx, session)
	}
	if err != nil {
		recordScreenRecordingToolOutcome(ctx, session, ScreenRecordingFailureStatus(err))
		return nil, DeviceRecordStartOutput{Error: err.Error(), RecordingID: start.RecordingID, NextSteps: errorNextSteps(err)}, nil
	}
	recordScreenRecordingToolOutcome(ctx, session, "started")
	return nil, DeviceRecordStartOutput{
		Success: true, RecordingID: start.RecordingID, SessionID: start.SessionID,
		MaxSeconds: start.MaxSeconds, MaxBytes: start.MaxBytes,
		NextSteps: []NextStep{{Tool: "device_record_stop", Params: fmt.Sprintf("recording_id=%q", start.RecordingID), Reason: "Save the recording as an MP4 once the steps you want on video are done"}},
	}, nil
}

func (s *Server) handleDeviceRecordStop(ctx context.Context, req *mcp.CallToolRequest, input DeviceRecordStopInput) (*mcp.CallToolResult, DeviceRecordStopOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID, "stop_screen_recording")
	var clip ScreenRecordingClip
	if err == nil {
		clip, err = s.sessionMgr.StopScreenRecording(ctx, session, input.RecordingID, input.OutputPath)
	}
	if err != nil {
		recordScreenRecordingToolOutcome(ctx, session, ScreenRecordingFailureStatus(err))
		return nil, DeviceRecordStopOutput{Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	recordScreenRecordingToolOutcome(ctx, session, ScreenRecordingSavedStatus(clip))
	return nil, DeviceRecordStopOutput{
		Success: true, RecordingID: clip.RecordingID, SessionID: clip.SessionID, Path: clip.Path,
		SizeBytes: clip.SizeBytes, DurationSeconds: clip.DurationSeconds, LimitReached: clip.LimitReached, Warning: clip.Warning,
	}, nil
}

// ScreenRecordingSavedStatus is the analytics outcome of a saved recording.
func ScreenRecordingSavedStatus(clip ScreenRecordingClip) string {
	if clip.LimitReached {
		return "saved_at_limit"
	}
	return "saved"
}

func recordScreenRecordingToolOutcome(ctx context.Context, session *DeviceSession, status string) {
	properties := map[string]interface{}{"domain": ScreenRecordingAnalyticsDomain, "domain_status": status}
	if session != nil && session.SessionID != "" {
		properties["entity_id"] = session.SessionID
	}
	analytics.SetMCPToolCallProperties(ctx, properties)
}
