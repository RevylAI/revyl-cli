package api

import "time"

// Limits the worker enforces on an on-demand screen recording. The recorder
// stops itself after ScreenRecordingMaxSeconds, or once the file comes within
// a tenth of ScreenRecordingMaxBytes, whichever is first.
const (
	ScreenRecordingMaxSeconds = 180
	ScreenRecordingMaxBytes   = 180 * 1024 * 1024
)

// ScreenRecordingStopTimeout bounds a stop request, which finalizes the
// recording on the device and returns up to ScreenRecordingMaxBytes of MP4.
const ScreenRecordingStopTimeout = 5 * time.Minute

// DeviceStartScreenRecordingResponse is the worker's /start_screen_recording reply.
type DeviceStartScreenRecordingResponse struct {
	Success     bool   `json:"success"`
	Action      string `json:"action"`
	Error       string `json:"error,omitempty"`
	RecordingID string `json:"recording_id,omitempty"`
}

// DeviceStopScreenRecordingRequest names the recording /stop_screen_recording finalizes.
type DeviceStopScreenRecordingRequest struct {
	RecordingID string `json:"recording_id"`
}
