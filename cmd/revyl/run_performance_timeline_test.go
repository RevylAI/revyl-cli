package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/runinspect"
)

func TestPerformanceTimelinePreservesPeaksAndMissingClock(t *testing.T) {
	rows := make([]map[string]any, 2000)
	for i := range rows {
		cpu, memory := 1, 2048
		if i == 7 {
			cpu = 95
		}
		if i == 9 {
			memory = 8192
		}
		rows[i] = map[string]any{"wall_time_s": 100 + i, "cpu": map[string]any{"app_percent": cpu}, "memory_app": map[string]any{"rss_kb": memory}}
	}
	raw, err := json.Marshal(map[string]any{"samples": rows})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := runinspect.ParsePerf(raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := performanceTimeline(context.Background(), nil, "unused", "session", capture)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) > 302 || result.TotalSamples != 2000 || result.TimeReference != "capture" || len(result.Captures) != 0 {
		t.Fatalf("incorrect bounds or clock: %+v", result)
	}
	cpuPeak, memoryPeak := false, false
	for _, point := range result.Points {
		cpuPeak = cpuPeak || point.CPUPercent != nil && *point.CPUPercent == 95 && point.TimeSeconds == 7
		memoryPeak = memoryPeak || point.MemoryMiB != nil && *point.MemoryMiB == 8 && point.TimeSeconds == 9
	}
	if !cpuPeak || !memoryPeak {
		t.Fatal("sampling dropped an extreme")
	}
}

func TestPerformanceTimelineRecordingClockAndHistoricalMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, samples, clock string
		notice               bool
	}{
		{"zero_is_valid", `[{"wall_time_s":20,"video_relative_s":0},{"wall_time_s":21,"video_relative_s":1}]`, "video", false},
		{"historical_clock_mismatch", `[{"wall_time_s":280,"video_relative_s":279},{"wall_time_s":680,"video_relative_s":679}]`, "capture", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/reports-v3/reports/by-session/session-fixture/context" {
					t.Errorf("wrong identity: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"report-fixture","started_at":"2026-01-01T00:00:00Z","completed_at":"2026-01-01T00:06:40Z","steps":[{"step_description":"Home","effective_status":"passed","actions":[{"video_timestamp_start":0,"screenshot_before_url":"https://example.invalid/screen.png"}]}]}`))
			}))
			defer server.Close()
			capture, err := runinspect.ParsePerf([]byte(`{"samples":` + tc.samples + `}`))
			if err != nil {
				t.Fatal(err)
			}
			result, err := performanceTimeline(context.Background(), api.NewClientWithBaseURL("fixture", server.URL), "session-fixture", "session", capture)
			if err != nil {
				t.Fatal(err)
			}
			if result.TimeReference != tc.clock || (result.AlignmentNotice != "") != tc.notice || result.Points[0].TimeSeconds != 0 || len(result.Captures) != 1 {
				t.Fatalf("incorrect evidence alignment: %+v", result)
			}
		})
	}
}
