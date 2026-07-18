package runinspect

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revyl/cli/internal/api"
)

// Report is a thin facade over the backend's context-report response —
// just the fields the run-inspector cares about. Other callers can
// continue to use the full “api.ReportContextResponse“.
type Report struct {
	TaskID             string
	ReportID           string // backend report row id; required for the dedicated device-logs endpoint
	SessionID          string
	TestID             string
	TestName           string
	Platform           string
	StartedAt          string
	CompletedAt        string
	Success            *bool
	DurationSeconds    *float64
	TotalSteps         int
	FailedSteps        int
	Steps              []ReportStep
	DeviceStateURL     string
	NetworkRequestsURL string
	HardwareMetricsURL string
	PerfettoTraceURL   string
	// *Partial flags mark a download URL that resolves to an in-progress
	// {session_id}/live/ object (run still executing) rather than the
	// finalized end-of-run artifact. The flag tells the reader to use the
	// live per-record JSONL parser (network/perf) and to label the output
	// partial; logs/state share their format both ways.
	DeviceStatePartial     bool
	NetworkRequestsPartial bool
	HardwareMetricsPartial bool
}

// ReportStep is the subset of a context-step record the inspector
// surfaces to callers. The full record stays accessible via the
// backend's raw envelope when needed.
type ReportStep struct {
	StepID         string
	ExecutionOrder int // 1-indexed within the report
	StepType       string
	Description    string
	Status         string // "passed" | "warning" | "failed" | "running" | "pending"
	StatusReason   string
}

// FetchReport pulls the high-context report for the given task_id (=
// execution_id). Returns “(nil, ErrReportNotFound)“ if the backend
// returned 404 — caller surfaces a friendly "no report yet" message
// rather than treating it as a hard error.
func FetchReport(ctx context.Context, client *api.Client, taskID string) (*Report, error) {
	envelope, err := client.GetReportContextByExecution(ctx, taskID, true, false, false)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrReportNotFound
		}
		return nil, fmt.Errorf("fetch report for task %s: %w", taskID, err)
	}
	if envelope == nil || envelope.Report == nil {
		return nil, fmt.Errorf("backend returned empty report for task %s", taskID)
	}
	src := envelope.Report
	out := &Report{
		TaskID:    taskID,
		ReportID:  src.Id,
		SessionID: strDeref(src.SessionId),
		TestID:    strDeref(src.TestId),
		TestName:  strDeref(src.TestName),
		Platform:  strDeref(src.Platform),
		StartedAt: timeToRFC3339(src.StartedAt),
		Success:   src.Success,
	}
	if src.CompletedAt != nil {
		out.CompletedAt = src.CompletedAt.Format(time.RFC3339)
	}
	if src.TotalSteps != nil {
		out.TotalSteps = *src.TotalSteps
	}
	if src.FailedSteps != nil {
		out.FailedSteps = *src.FailedSteps
	}
	if src.DeviceStateUrl != nil {
		out.DeviceStateURL = *src.DeviceStateUrl
	}
	if src.NetworkRequestsUrl != nil {
		out.NetworkRequestsURL = *src.NetworkRequestsUrl
	}
	if src.HardwareMetricsUrl != nil {
		out.HardwareMetricsURL = *src.HardwareMetricsUrl
	}
	if src.PerfettoTraceUrl != nil {
		out.PerfettoTraceURL = *src.PerfettoTraceUrl
	}
	if src.DeviceStatePartial != nil {
		out.DeviceStatePartial = *src.DeviceStatePartial
	}
	if src.NetworkRequestsPartial != nil {
		out.NetworkRequestsPartial = *src.NetworkRequestsPartial
	}
	if src.HardwareMetricsPartial != nil {
		out.HardwareMetricsPartial = *src.HardwareMetricsPartial
	}
	if out.StartedAt != "" && out.CompletedAt != "" {
		if s, err := time.Parse(time.RFC3339, out.StartedAt); err == nil {
			if c, err := time.Parse(time.RFC3339, out.CompletedAt); err == nil {
				secs := c.Sub(s).Seconds()
				out.DurationSeconds = &secs
			}
		}
	}
	if src.Steps != nil {
		for _, s := range *src.Steps {
			out.Steps = append(out.Steps, ReportStep{
				StepID:         s.Id,
				ExecutionOrder: s.ExecutionOrder,
				StepType:       s.StepType,
				Description:    strDeref(s.StepDescription),
				Status:         strDeref(s.EffectiveStatus),
				StatusReason:   strDeref(s.EffectiveStatusReason),
			})
		}
	}
	return out, nil
}

// ErrReportNotFound signals "no report exists for this task_id yet" —
// either the run is still in progress, was cancelled before any
// artifact was uploaded, or the caller has the wrong ID.
var ErrReportNotFound = errors.New("report not found")

// ErrNoDeviceStateArtifact signals the report exists but no
// device_state.jsonl.gz was uploaded — typically means the run was on
// Android, or the iOS sampler was disabled, or the run ended before
// the upload phase.
var ErrNoDeviceStateArtifact = errors.New("device state artifact not present on this report")

// FetchDeviceStateLines downloads the presigned device_state.jsonl.gz
// for the report, decompresses it, and returns one parsed line per
// JSONL entry. Malformed lines are skipped silently (matches the
// sampler's own resilience to partial-write corruption).
//
// Cache: writes a copy to “<cacheDir>/<taskID>/device_state.jsonl“
// the first time it's fetched. Subsequent calls within the same
// session re-read from disk. “cacheDir“ is created if missing.
func FetchDeviceStateLines(
	ctx context.Context,
	report *Report,
	httpClient *http.Client,
	cacheDir string,
) ([]DeviceStateLine, error) {
	if report == nil {
		return nil, errors.New("nil report")
	}
	if report.DeviceStateURL == "" {
		return nil, ErrNoDeviceStateArtifact
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	// A still-growing live object must never be read from or written to the
	// taskID-keyed cache, or a later post-run read is served the stale partial
	// (and a mid-run `revyl run summary` would poison it). Enforce this here so
	// every caller is safe regardless of the cacheDir it passes.
	if IsLiveURL(report.DeviceStateURL) {
		cacheDir = ""
	}

	if cached, ok := readCachedJSONL(cacheDir, report.TaskID); ok {
		return parseJSONLLines(cached)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, report.DeviceStateURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"download artifact: HTTP %d (presigned URL may have expired — re-fetch report)",
			resp.StatusCode,
		)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()

	// Hard ceiling on the decompressed artifact to bound RAM use against
	// malicious or runaway sampler output. 256 MiB is well above any
	// observed run (most are <10 MiB) and matches the largest plist+sqlite
	// budget the worker is willing to emit per step × max step count.
	const maxDecompressedBytes = 256 * 1024 * 1024
	limited := io.LimitReader(gz, maxDecompressedBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read decompressed: %w", err)
	}
	if int64(len(raw)) > maxDecompressedBytes {
		return nil, fmt.Errorf(
			"decompressed artifact exceeds %d byte ceiling — likely corrupt or unexpectedly large",
			maxDecompressedBytes,
		)
	}
	writeCachedJSONL(cacheDir, report.TaskID, raw)
	return parseJSONLLines(raw)
}

// parseJSONLLines splits “text“ on newlines and decodes each
// non-empty line. Lines that fail to parse are dropped — the sampler
// writes line-by-line, so a crash mid-line is normal.
func parseJSONLLines(text []byte) ([]DeviceStateLine, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(text)))
	// Plist XML can be sizable — give the scanner a generous line cap.
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var out []DeviceStateLine
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var parsed DeviceStateLine
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			continue
		}
		out = append(out, parsed)
	}
	if err := scanner.Err(); err != nil {
		return out, fmt.Errorf("scan jsonl: %w", err)
	}
	return out, nil
}

func readCachedJSONL(cacheDir, taskID string) ([]byte, bool) {
	if cacheDir == "" {
		return nil, false
	}
	path := filepath.Join(cacheDir, taskID, "device_state.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func writeCachedJSONL(cacheDir, taskID string, data []byte) {
	if cacheDir == "" {
		return
	}
	dir := filepath.Join(cacheDir, taskID)
	// 0o700 / 0o600 — JSONL may contain PII (user emails, org IDs, vendor
	// session keys). Matches the perms already used for
	// .revyl/device-sessions.json.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "device_state.jsonl"), data, 0o600)
}

// DefaultCacheDir returns “~/.revyl/run-cache“ (or empty if HOME
// isn't resolvable, in which case the caller will skip caching).
func DefaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".revyl", "run-cache")
}

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// timeToRFC3339 renders an optional timestamp as an RFC3339 string (the shape
// the report consumers expect), or "" when absent. The backend types
// started_at/completed_at as date-times, so the generated client yields
// *time.Time here rather than *string.
func timeToRFC3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// isNotFound recognises a 404 from the backend so callers can map
// it to “ErrReportNotFound“ for a friendlier message. Prefers the
// typed status code on “*api.APIError“ to avoid false positives from
// transport errors whose messages happen to contain "not found".
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}
