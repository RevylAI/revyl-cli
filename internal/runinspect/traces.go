package runinspect

// Trace-handle extraction from captured Sentry envelopes.
//
// `DetectTraces` walks the JSONL produced by the iOS sampler, finds
// every `sentry_envelope` change entry, and rolls its items up into
// a deduplicated list of trace records suitable for backend
// correlation. The output is what powers `revyl run traces` and the
// `get_run_traces` MCP tool.
//
// Why this is "traces" and not "trace-hints":
//   - These IDs come from the SDK's own on-device buffer that was
//     about to ship to Sentry's backend.
//   - They literally are the IDs the customer's Sentry org will
//     index them by.
//   - No regex heuristics, no shape guessing — the values arrive
//     here typed (event_id, trace_id, span_id, replay_id).

// TraceVendor names the source SDK for the trace handle.
type TraceVendor string

const (
	TraceVendorSentry TraceVendor = "sentry"
	// Future: TraceVendorDatadog, TraceVendorNewRelic, etc.
)

// TraceKind names which Sentry-side concept the handle pivots to.
type TraceKind string

const (
	TraceKindEvent       TraceKind = "event"       // exception event ID
	TraceKindTransaction TraceKind = "transaction" // perf transaction
	TraceKindTrace       TraceKind = "trace"       // raw trace_id (no event_id co-located)
	TraceKindSpan        TraceKind = "span"        // span_id (paired with a trace)
	TraceKindReplay      TraceKind = "replay"      // session-replay handle
)

// TraceRecord is one backend-pivotable trace handle.
type TraceRecord struct {
	Vendor      TraceVendor `json:"vendor"`
	Kind        TraceKind   `json:"kind"`
	Value       string      `json:"value"` // the ID itself (paste this into Sentry)
	Label       string      `json:"label"` // human label
	Transaction string      `json:"transaction,omitempty"`
	// Related is for "this transaction was on this trace_id with this
	// span_id" so the agent doesn't have to cross-reference records.
	Related        map[string]string `json:"related,omitempty"`
	FirstSeenStep  int               `json:"first_seen_step"`
	LastSeenStep   int               `json:"last_seen_step"`
	FirstTimestamp string            `json:"first_timestamp,omitempty"`
	Source         FieldSource       `json:"source"`
}

// TracesOptions tunes the detector.
type TracesOptions struct {
	// AtStep restricts to lines with step index ≤ AtStep. 0 = all.
	AtStep int
	// Vendors restricts output to specific vendors. Empty = all.
	Vendors []TraceVendor
}

// DetectTraces walks the JSONL lines, extracts every Sentry envelope
// item, and rolls them up into one TraceRecord per unique
// (vendor, kind, value) tuple. Floor heartbeat lines are ignored.
func DetectTraces(
	lines []DeviceStateLine,
	indexer StepIndexer,
	opts TracesOptions,
) []TraceRecord {
	type acc struct {
		rec       TraceRecord
		firstStep int
		lastStep  int
	}
	bucket := make(map[string]*acc)
	keyOf := func(v TraceVendor, k TraceKind, val string) string {
		return string(v) + "|" + string(k) + "|" + val
	}

	ingest := func(rec TraceRecord, stepIdx int) {
		if rec.Value == "" {
			return
		}
		key := keyOf(rec.Vendor, rec.Kind, rec.Value)
		if existing, ok := bucket[key]; ok {
			if stepIdx < existing.firstStep {
				existing.firstStep = stepIdx
				if rec.FirstTimestamp != "" {
					existing.rec.FirstTimestamp = rec.FirstTimestamp
				}
			}
			if stepIdx > existing.lastStep {
				existing.lastStep = stepIdx
			}
			// Merge Related — newer keys can fill in fields the first
			// observation didn't have (e.g. a later event item gives
			// us the trace's transaction name).
			if rec.Transaction != "" && existing.rec.Transaction == "" {
				existing.rec.Transaction = rec.Transaction
			}
			for k, v := range rec.Related {
				if _, ok := existing.rec.Related[k]; !ok {
					if existing.rec.Related == nil {
						existing.rec.Related = make(map[string]string)
					}
					existing.rec.Related[k] = v
				}
			}
			return
		}
		bucket[key] = &acc{rec: rec, firstStep: stepIdx, lastStep: stepIdx}
	}

	for i, line := range lines {
		if line.IsFloor() {
			continue
		}
		stepIdx := indexer.IndexFor(line.StepID, i+1)
		if opts.AtStep > 0 && stepIdx > opts.AtStep {
			continue
		}
		for path, change := range line.Changed {
			if change.Kind != "sentry_envelope" {
				continue
			}
			source := FieldSource{Kind: "sentry_envelope", Path: path}
			for _, item := range change.Items {
				for _, rec := range envelopeItemToRecords(item, source) {
					ingest(rec, stepIdx)
				}
			}
		}
	}

	out := make([]TraceRecord, 0, len(bucket))
	for _, a := range bucket {
		if !vendorAllowed(a.rec.Vendor, opts.Vendors) {
			continue
		}
		rec := a.rec
		rec.FirstSeenStep = a.firstStep
		rec.LastSeenStep = a.lastStep
		out = append(out, rec)
	}
	return out
}

// envelopeItemToRecords expands one Sentry envelope item into 1-N
// TraceRecords. An event with both event_id and trace_id produces
// two records (one per pivot URL); the `Related` field cross-links
// them so the agent can render either independently.
func envelopeItemToRecords(item SentryEnvelopeItem, source FieldSource) []TraceRecord {
	related := map[string]string{}
	if item.TraceID != "" {
		related["trace_id"] = item.TraceID
	}
	if item.SpanID != "" {
		related["span_id"] = item.SpanID
	}
	if item.EventID != "" {
		related["event_id"] = item.EventID
	}
	if item.ReplayID != "" {
		related["replay_id"] = item.ReplayID
	}

	out := []TraceRecord{}
	base := TraceRecord{
		Vendor:         TraceVendorSentry,
		Transaction:    item.Transaction,
		Related:        related,
		FirstTimestamp: item.Timestamp,
		Source:         source,
	}
	switch item.Type {
	case "event":
		if item.EventID != "" {
			r := base
			r.Kind = TraceKindEvent
			r.Value = item.EventID
			r.Label = "Sentry event ID"
			out = append(out, r)
		}
		if item.TraceID != "" {
			r := base
			r.Kind = TraceKindTrace
			r.Value = item.TraceID
			r.Label = "Sentry trace ID"
			out = append(out, r)
		}
	case "transaction":
		if item.EventID != "" {
			r := base
			r.Kind = TraceKindTransaction
			r.Value = item.EventID
			r.Label = "Sentry transaction event ID"
			out = append(out, r)
		}
		if item.TraceID != "" {
			r := base
			r.Kind = TraceKindTrace
			r.Value = item.TraceID
			r.Label = "Sentry trace ID"
			out = append(out, r)
		}
	case "replay_event":
		if item.ReplayID != "" {
			r := base
			r.Kind = TraceKindReplay
			r.Value = item.ReplayID
			r.Label = "Sentry replay ID"
			out = append(out, r)
		}
	}
	return out
}

func vendorAllowed(v TraceVendor, allow []TraceVendor) bool {
	if len(allow) == 0 {
		return true
	}
	for _, a := range allow {
		if a == v {
			return true
		}
	}
	return false
}
