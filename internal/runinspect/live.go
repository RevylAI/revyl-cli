package runinspect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revyl/cli/internal/api"
	statusutil "github.com/revyl/cli/internal/status"
)

// IsLiveURL reports whether a presigned artifact URL points at an in-progress
// {session_id}/live/ object rather than a finalized end-of-run artifact. It is
// the single source of truth for the live/finalized distinction: gating the
// on-disk cache (a still-growing object must never be cached under the
// finalized key) and the --download refusal key on this.
func IsLiveURL(url string) bool {
	return strings.Contains(url, "/live/")
}

// RunLiveness frames missing-artifact messages; parser selection uses the bytes.
type RunLiveness struct {
	Live     bool
	Platform string
	Status   string
}

// ResolveRunLiveness inspects the device session backing a report to
// decide whether the run is still executing. A run with no resolvable
// session is treated as not-live.
func ResolveRunLiveness(ctx context.Context, client *api.Client, report *Report) (RunLiveness, error) {
	if report == nil || report.SessionID == "" {
		return RunLiveness{}, nil
	}
	detail, err := client.GetDeviceSessionByID(ctx, report.SessionID)
	if err != nil {
		return RunLiveness{}, err
	}
	out := RunLiveness{
		Status:   strings.ToLower(strings.TrimSpace(detail.Status)),
		Platform: report.Platform,
	}
	if detail.Platform != nil && *detail.Platform != "" {
		out.Platform = *detail.Platform
	}
	out.Platform = strings.ToLower(out.Platform)
	// "Still capturing" = any non-terminal session phase. Use the shared
	// status set (queued/starting/running/verifying/stopping/setup) rather
	// than a local allowlist, which previously omitted "setup" and reported
	// a booting run as not-live — making the CLI print "no X uploaded" instead
	// of "not available yet".
	out.Live = statusutil.IsActive(out.Status)
	return out, nil
}

// isAggregated checks the first object's top-level keys without decoding the
// aggregate arrays into application data.
func isAggregated(data []byte, aggregateKey string) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return false
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return false
		}
		if key, ok := keyTok.(string); ok && key == aggregateKey {
			return true
		}
		if err := dec.Decode(&json.RawMessage{}); err != nil {
			return false
		}
	}
	return false
}

// scanLiveJSONL feeds a still-growing live JSONL body to fn one line at a
// time. A torn final line (the file is appended to as the run executes)
// fails to decode and is skipped — matching the sampler's own resilience
// to partial-write corruption.
func scanLiveJSONL(data []byte, fn func(line []byte)) error {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	// JSONL lines can be sizable (device-state plist XML); give the scanner
	// a generous cap, consistent with parseJSONLLines.
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fn([]byte(line))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan live jsonl: %w", err)
	}
	return nil
}

type liveNetworkItem struct {
	URL              string   `json:"url"`
	Method           string   `json:"method"`
	StatusCode       int      `json:"status_code"`
	StartTimeS       float64  `json:"start_time_s"`
	VideoRelativeS   *float64 `json:"video_relative_s"`
	DurationMs       float64  `json:"duration_ms"`
	RequestBodySize  int64    `json:"request_body_size"`
	ResponseBodySize int64    `json:"response_body_size"`
	Error            *string  `json:"error"`
	IsAuth           bool     `json:"is_auth"`
}

// ParseLiveNetwork parses the per-request JSONL body of the in-progress
// {session_id}/live/network_requests.jsonl.gz object (already gunzipped
// by the caller) into a NetworkCapture whose recomputed summary matches
// the post-run parsed shape. Live capture carries no header/body
// previews, so --body has nothing to show and --grep matches URLs only.
func ParseLiveNetwork(data []byte) (*NetworkCapture, error) {
	out := &NetworkCapture{}
	err := scanLiveJSONL(data, func(line []byte) {
		var it liveNetworkItem
		if err := json.Unmarshal(line, &it); err != nil {
			return
		}
		req := NetworkRequest{
			URL:              it.URL,
			Method:           it.Method,
			StatusCode:       it.StatusCode,
			StartTimeS:       it.StartTimeS,
			DurationMs:       it.DurationMs,
			RequestBodySize:  it.RequestBodySize,
			ResponseBodySize: it.ResponseBodySize,
			Error:            it.Error,
			IsAuth:           it.IsAuth,
		}
		if it.VideoRelativeS != nil {
			req.VideoRelativeS = *it.VideoRelativeS
		}
		out.Requests = append(out.Requests, req)
	})
	if err != nil {
		return nil, err
	}
	for _, r := range out.Requests {
		out.Summary.TotalRequests++
		out.Summary.TotalDurationMs += r.DurationMs
		out.Summary.TotalResponseBytes += r.ResponseBodySize
		if r.Error != nil || r.StatusCode >= 400 {
			out.Summary.FailedRequests++
		}
	}
	return out, nil
}

type livePerfItem struct {
	Timestamp float64                `json:"timestamp"`
	CPU       map[string]interface{} `json:"cpu"`
	MemoryApp map[string]interface{} `json:"memory_app"`
	FPS       *float64               `json:"fps"`
}

// ParseLivePerf parses the per-sample JSONL body of the in-progress
// {session_id}/live/hardware_metrics.jsonl.gz object (already gunzipped
// by the caller) into a PerfCapture whose Samples feed the shared
// ComputeMetricStats. Worker timestamps are absolute epoch seconds, so we
// rebase WallTimeS to the first sample to keep "peak at t=Ns" meaningful.
// The summary header fields (sample count, duration, FPS) are derived here
// since the live file carries no aggregated summary.
func ParseLivePerf(data []byte) (*PerfCapture, error) {
	out := &PerfCapture{}
	var raw []livePerfItem
	err := scanLiveJSONL(data, func(line []byte) {
		var it livePerfItem
		if err := json.Unmarshal(line, &it); err != nil {
			return
		}
		raw = append(raw, it)
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return out, nil
	}

	base := raw[0].Timestamp
	var fpsSum, fpsMin float64
	fpsCount := 0
	for _, it := range raw {
		s := PerfSample{WallTimeS: it.Timestamp - base}
		if v, ok := mapFloat(it.CPU, "app_percent"); ok {
			s.CPU = &struct {
				AppPercent float64 `json:"app_percent"`
			}{AppPercent: v}
		}
		if v, ok := mapFloat(it.MemoryApp, "rss_kb"); ok {
			s.MemoryApp = &struct {
				RSSKB   int64 `json:"rss_kb"`
				VSizeKB int64 `json:"vsize_kb,omitempty"`
			}{RSSKB: int64(v)}
		}
		out.Samples = append(out.Samples, s)
		if it.FPS != nil {
			if fpsCount == 0 || *it.FPS < fpsMin {
				fpsMin = *it.FPS
			}
			fpsSum += *it.FPS
			fpsCount++
		}
	}

	count := len(out.Samples)
	out.Summary.SampleCount = &count
	dur := raw[len(raw)-1].Timestamp - base
	out.Summary.DurationS = &dur
	if fpsCount > 0 {
		avg := fpsSum / float64(fpsCount)
		out.Summary.AvgFPS = &avg
		out.Summary.MinFPS = &fpsMin
	}
	return out, nil
}

func mapFloat(m map[string]interface{}, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	switch t := m[key].(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	}
	return 0, false
}
