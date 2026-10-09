// Hardware-metrics parsing + percentile summary for the run-inspector.
//
// Sample shape:
//
//   {
//     "wall_time_s": 0.001,
//     "video_relative_s": -2.047,
//     "cpu": {"app_percent": 65.4},
//     "memory_app": {"rss_kb": 193424, "vsize_kb": 411162672}
//   }
//
// The captured `summary` field already has peak/avg pre-computed by the
// worker, so the CLI mainly adds percentile detail (p50 / p95) the worker
// doesn't pre-compute, and a peak-timestamp lookup.

package runinspect

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PerfSample mirrors one entry in `samples`.
type PerfSample struct {
	WallTimeS      float64  `json:"wall_time_s"`
	VideoRelativeS *float64 `json:"video_relative_s,omitempty"`
	CPU            *struct {
		AppPercent float64 `json:"app_percent"`
	} `json:"cpu,omitempty"`
	MemoryApp *struct {
		RSSKB   int64 `json:"rss_kb"`
		VSizeKB int64 `json:"vsize_kb,omitempty"`
	} `json:"memory_app,omitempty"`
}

// PerfSummary mirrors the captured `summary` object (worker-computed
// aggregates). Fields are pointers so we can detect "field not in
// payload" vs "field is zero".
type PerfSummary struct {
	PeakCPUPercent      *float64 `json:"peak_cpu_percent,omitempty"`
	AvgCPUPercent       *float64 `json:"avg_cpu_percent,omitempty"`
	PeakMemoryRSSKB     *int64   `json:"peak_memory_rss_kb,omitempty"`
	AvgMemoryRSSKB      *float64 `json:"avg_memory_rss_kb,omitempty"`
	SampleCount         *int     `json:"sample_count,omitempty"`
	DurationS           *float64 `json:"duration_s,omitempty"`
	TraceStartTimestamp *float64 `json:"trace_start_timestamp,omitempty"`
	VideoStartTimestamp *float64 `json:"video_start_timestamp,omitempty"`
	AvgFPS              *float64 `json:"avg_fps,omitempty"`
	MinFPS              *float64 `json:"min_fps,omitempty"`
}

// PerfAppEvent mirrors one entry in `app_events` — generic key/value
// marker the worker emits at noteworthy lifecycle points.
type PerfAppEvent struct {
	Type           string  `json:"type"`
	WallTimeS      float64 `json:"wall_time_s"`
	VideoRelativeS float64 `json:"video_relative_s,omitempty"`
	Data           string  `json:"data,omitempty"`
}

// PerfCapture is the parsed top-level document.
type PerfCapture struct {
	Samples   []PerfSample   `json:"samples"`
	Summary   PerfSummary    `json:"summary"`
	AppEvents []PerfAppEvent `json:"app_events,omitempty"`
}

// ParsePerf decodes hardware-metrics bytes, auto-detecting the format:
// the finalized object is a single aggregated JSON document (top-level
// "samples" array + worker-computed "summary"); the in-progress live
// object is per-sample JSONL. Detection is structural (isAggregated), so
// the caller need not — and must not — rely on the report's partial flag
// to choose a parser. Both paths yield the same PerfCapture.
func ParsePerf(data []byte) (*PerfCapture, error) {
	if isAggregated(data, "samples") {
		return ParsePerfCapture(data)
	}
	return ParseLivePerf(data)
}

// ParsePerfCapture decodes the raw JSON blob.
func ParsePerfCapture(data []byte) (*PerfCapture, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return &PerfCapture{}, nil
	}
	var out PerfCapture
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse perf capture: %w", err)
	}
	return &out, nil
}

// MetricStats holds the percentile breakdown for a single series.
type MetricStats struct {
	Label   string // "CPU %", "Mem RSS MiB"
	Unit    string // "%", "MiB"
	Min     float64
	P50     float64
	P95     float64
	Max     float64
	Avg     float64
	Samples int
	// PeakWallTimeS is the wall-clock offset (seconds from trace start)
	// at which Max was first observed.
	PeakWallTimeS float64
}

// ComputeMetricStats returns p50/p95/min/max/avg for the two series
// we currently capture (CPU and memory RSS). Skip is true for any
// series with no captured samples.
func ComputeMetricStats(cap *PerfCapture) (cpu MetricStats, mem MetricStats, cpuOK, memOK bool) {
	cpu.Label, cpu.Unit = "CPU %", "%"
	mem.Label, mem.Unit = "Mem RSS", "MiB"

	cpuValues := make([]float64, 0, len(cap.Samples))
	memValues := make([]float64, 0, len(cap.Samples))
	var cpuPeak, memPeak struct {
		val  float64
		time float64
		set  bool
	}

	for _, s := range cap.Samples {
		if s.CPU != nil {
			v := s.CPU.AppPercent
			cpuValues = append(cpuValues, v)
			if !cpuPeak.set || v > cpuPeak.val {
				cpuPeak.val, cpuPeak.time, cpuPeak.set = v, s.WallTimeS, true
			}
		}
		if s.MemoryApp != nil {
			v := float64(s.MemoryApp.RSSKB) / 1024.0
			memValues = append(memValues, v)
			if !memPeak.set || v > memPeak.val {
				memPeak.val, memPeak.time, memPeak.set = v, s.WallTimeS, true
			}
		}
	}

	if len(cpuValues) > 0 {
		fillStats(&cpu, cpuValues)
		cpu.PeakWallTimeS = cpuPeak.time
		cpuOK = true
	}
	if len(memValues) > 0 {
		fillStats(&mem, memValues)
		mem.PeakWallTimeS = memPeak.time
		memOK = true
	}
	return
}

func fillStats(out *MetricStats, values []float64) {
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	out.Samples = len(sorted)
	out.Min = sorted[0]
	out.Max = sorted[len(sorted)-1]
	out.P50 = percentile(sorted, 0.50)
	out.P95 = percentile(sorted, 0.95)
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	out.Avg = sum / float64(len(sorted))
}

// percentile assumes `sorted` is non-empty ascending. Linear
// interpolation between adjacent ranks (NIST type-7 / Excel
// PERCENTILE.INC). Boundary edge cases handled directly.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p * float64(len(sorted)-1)
	low := int(rank)
	high := low + 1
	if high >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := rank - float64(low)
	return sorted[low]*(1-frac) + sorted[high]*frac
}
