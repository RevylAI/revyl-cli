package runinspect

import (
	"sort"
	"testing"
)

func TestDetectTraces_TransactionItemEmitsTraceAndTransaction(t *testing.T) {
	lines := []DeviceStateLine{
		{
			StepID: "step-1",
			Changed: map[string]DeviceStateChange{
				"Library/Caches/io.sentry/abc/envelopes/x.envelope": {
					Kind: "sentry_envelope",
					Items: []SentryEnvelopeItem{
						{
							Type:        "transaction",
							EventID:     "4f8a7c1d000000000000000000000000",
							TraceID:     "00aabbccddeeff00aabbccddeeff0011",
							SpanID:      "1122334455667788",
							Transaction: "POST /api/whops",
							Timestamp:   "2026-05-13T12:00:00Z",
						},
					},
				},
			},
		},
	}
	idx := MapIndexer{"step-1": 1}
	out := DetectTraces(lines, idx, TracesOptions{})

	kinds := make(map[TraceKind]TraceRecord)
	for _, r := range out {
		kinds[r.Kind] = r
	}
	tx, ok := kinds[TraceKindTransaction]
	if !ok {
		t.Fatalf("expected transaction record; got kinds=%v", kindKeys(kinds))
	}
	if tx.Value != "4f8a7c1d000000000000000000000000" {
		t.Errorf("transaction value = %s, want event_id", tx.Value)
	}
	if tx.Transaction != "POST /api/whops" {
		t.Errorf("transaction = %s, want 'POST /api/whops'", tx.Transaction)
	}
	if tx.Related["trace_id"] != "00aabbccddeeff00aabbccddeeff0011" {
		t.Errorf("trace_id in Related missing; got %+v", tx.Related)
	}

	tr, ok := kinds[TraceKindTrace]
	if !ok {
		t.Fatalf("expected trace record")
	}
	if tr.Value != "00aabbccddeeff00aabbccddeeff0011" {
		t.Errorf("trace value mismatch")
	}
}

func TestDetectTraces_ReplayEvent(t *testing.T) {
	lines := []DeviceStateLine{
		{
			StepID: "step-1",
			Changed: map[string]DeviceStateChange{
				"Library/Caches/io.sentry/abc/envelopes/r.envelope": {
					Kind: "sentry_envelope",
					Items: []SentryEnvelopeItem{
						{
							Type:     "replay_event",
							EventID:  "22222222222222222222222222222222",
							ReplayID: "a3ce929d0e0e4736b8c9d4a3f7e2c891",
						},
					},
				},
			},
		},
	}
	out := DetectTraces(lines, MapIndexer{"step-1": 1}, TracesOptions{})
	if len(out) != 1 {
		t.Fatalf("expected 1 record, got %d", len(out))
	}
	if out[0].Kind != TraceKindReplay {
		t.Errorf("kind = %s, want replay", out[0].Kind)
	}
	if out[0].Value != "a3ce929d0e0e4736b8c9d4a3f7e2c891" {
		t.Errorf("value mismatch: %s", out[0].Value)
	}
}

func TestDetectTraces_DedupAcrossSteps(t *testing.T) {
	lines := []DeviceStateLine{
		{
			StepID: "step-a",
			Changed: map[string]DeviceStateChange{
				"Library/Caches/io.sentry/x.envelope": {
					Kind: "sentry_envelope",
					Items: []SentryEnvelopeItem{
						{
							Type:    "event",
							EventID: "ee" + repeated("0", 30),
							TraceID: "11" + repeated("0", 30),
						},
					},
				},
			},
		},
		{
			StepID: "step-b",
			Changed: map[string]DeviceStateChange{
				"Library/Caches/io.sentry/y.envelope": {
					Kind: "sentry_envelope",
					Items: []SentryEnvelopeItem{
						{
							// Same event repeated from a later envelope
							// (e.g. the SDK retried the upload).
							Type:    "event",
							EventID: "ee" + repeated("0", 30),
							TraceID: "11" + repeated("0", 30),
						},
					},
				},
			},
		},
	}
	idx := MapIndexer{"step-a": 1, "step-b": 2}
	out := DetectTraces(lines, idx, TracesOptions{})
	// Two unique (vendor, kind, value) tuples: one event, one trace.
	if len(out) != 2 {
		t.Fatalf("expected 2 records (event + trace), got %d", len(out))
	}
	for _, r := range out {
		if r.FirstSeenStep != 1 || r.LastSeenStep != 2 {
			t.Errorf("step range = %d..%d, want 1..2 (rec=%+v)", r.FirstSeenStep, r.LastSeenStep, r)
		}
	}
}

func TestDetectTraces_AtStepClips(t *testing.T) {
	lines := []DeviceStateLine{
		{
			StepID: "step-1",
			Changed: map[string]DeviceStateChange{
				"Library/Caches/io.sentry/a.envelope": {
					Kind: "sentry_envelope",
					Items: []SentryEnvelopeItem{
						{Type: "event", EventID: "aa" + repeated("0", 30)},
					},
				},
			},
		},
		{
			StepID: "step-3",
			Changed: map[string]DeviceStateChange{
				"Library/Caches/io.sentry/b.envelope": {
					Kind: "sentry_envelope",
					Items: []SentryEnvelopeItem{
						{Type: "event", EventID: "bb" + repeated("0", 30)},
					},
				},
			},
		},
	}
	idx := MapIndexer{"step-1": 1, "step-3": 3}
	out := DetectTraces(lines, idx, TracesOptions{AtStep: 2})
	if len(out) != 1 {
		t.Fatalf("expected 1 record (AtStep=2 clip), got %d", len(out))
	}
}

func TestDetectTraces_FloorLinesIgnored(t *testing.T) {
	floor := "floor"
	lines := []DeviceStateLine{
		{
			StepID:     "__live_floor__",
			ActionType: &floor,
			Changed: map[string]DeviceStateChange{
				"Library/Caches/io.sentry/x.envelope": {
					Kind: "sentry_envelope",
					Items: []SentryEnvelopeItem{
						{Type: "event", EventID: "aa" + repeated("0", 30)},
					},
				},
			},
		},
	}
	out := DetectTraces(lines, MapIndexer{}, TracesOptions{})
	if len(out) != 0 {
		t.Errorf("expected floor lines to be ignored, got %d records", len(out))
	}
}

func TestDetectTraces_VendorFilter(t *testing.T) {
	lines := []DeviceStateLine{
		{
			StepID: "step-1",
			Changed: map[string]DeviceStateChange{
				"Library/Caches/io.sentry/x.envelope": {
					Kind: "sentry_envelope",
					Items: []SentryEnvelopeItem{
						{Type: "event", EventID: "aa" + repeated("0", 30)},
					},
				},
			},
		},
	}
	idx := MapIndexer{"step-1": 1}
	out := DetectTraces(lines, idx, TracesOptions{Vendors: []TraceVendor{TraceVendorSentry}})
	if len(out) != 1 {
		t.Errorf("expected 1 sentry record, got %d", len(out))
	}
	// A vendor that doesn't exist yet — nothing.
	out = DetectTraces(lines, idx, TracesOptions{Vendors: []TraceVendor{"datadog"}})
	if len(out) != 0 {
		t.Errorf("expected 0 records under datadog filter, got %d", len(out))
	}
}

// helpers

func repeated(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func kindKeys(m map[TraceKind]TraceRecord) []TraceKind {
	out := make([]TraceKind, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
