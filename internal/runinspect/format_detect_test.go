package runinspect

import "testing"

func TestParsePerf_DetectsFormat(t *testing.T) {
	aggregated := `{"samples":[{"wall_time_s":0.0,"cpu":{"app_percent":10.0},` +
		`"memory_app":{"rss_kb":1024}}],"summary":{"sample_count":1}}`
	jsonl := `{"timestamp":100.0,"cpu":{"app_percent":10.0},"memory_app":{"rss_kb":1024}}
{"timestamp":100.5,"cpu":{"app_percent":20.0},"memory_app":{"rss_kb":2048}}`

	cases := []struct {
		name     string
		data     string
		wantN    int
		wantPeak float64 // expected CPU max, to prove the right parser ran
	}{
		{"aggregated", aggregated, 1, 10.0},
		{"jsonl", jsonl, 2, 20.0},
		// A single live record (no "samples") must parse as JSONL, not be
		// mistaken for an aggregated doc with zero samples.
		{"single-jsonl-record",
			`{"timestamp":5.0,"cpu":{"app_percent":42.0},"memory_app":{"rss_kb":1}}`,
			1, 42.0},
		{"empty", "   ", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap, err := ParsePerf([]byte(tc.data))
			if err != nil {
				t.Fatalf("ParsePerf: %v", err)
			}
			if len(cap.Samples) != tc.wantN {
				t.Fatalf("samples = %d, want %d", len(cap.Samples), tc.wantN)
			}
			if tc.wantN > 0 {
				cpu, _, ok, _ := ComputeMetricStats(cap)
				if !ok || cpu.Max != tc.wantPeak {
					t.Fatalf("cpu max = %v (ok=%v), want %v", cpu.Max, ok, tc.wantPeak)
				}
			}
		})
	}
}

func TestParseNetwork_DetectsFormat(t *testing.T) {
	aggregated := `{"requests":[{"url":"https://a.com/x","status_code":200}],` +
		`"summary":{"total_requests":1}}`
	jsonl := `{"url":"https://a.com/x","status_code":200}
{"url":"https://b.com/y","status_code":500}`

	cases := []struct {
		name       string
		data       string
		wantN      int
		wantFailed int // proves the summary was recomputed from the right parse
	}{
		{"aggregated", aggregated, 1, 0},
		{"jsonl", jsonl, 2, 1},
		{"single-jsonl-record",
			`{"url":"https://a.com/x","status_code":404}`, 1, 1},
		{"empty", "", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap, err := ParseNetwork([]byte(tc.data))
			if err != nil {
				t.Fatalf("ParseNetwork: %v", err)
			}
			if len(cap.Requests) != tc.wantN {
				t.Fatalf("requests = %d, want %d", len(cap.Requests), tc.wantN)
			}
			if tc.name != "aggregated" && cap.Summary.FailedRequests != tc.wantFailed {
				t.Fatalf("failed = %d, want %d",
					cap.Summary.FailedRequests, tc.wantFailed)
			}
		})
	}
}
