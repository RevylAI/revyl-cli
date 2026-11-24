// Package runinspect reads report artifacts for active and completed runs.
package runinspect

// DeviceStateLine is the CLI projection of one device-state sampler record.
type DeviceStateLine struct {
	StepID         string                       `json:"step_id"`
	StepStartedAt  *float64                     `json:"step_started_at,omitempty"`
	ActionSeq      *int                         `json:"action_seq,omitempty"`
	ActionType     *string                      `json:"action_type,omitempty"`
	Type           *string                      `json:"type,omitempty"`        // "step" or "snapshot"
	SnapshotID     *string                      `json:"snapshot_id,omitempty"` // present on type=snapshot
	Ts             *float64                     `json:"ts,omitempty"`
	VideoRelativeS *float64                     `json:"video_relative_s,omitempty"`
	Changed        map[string]DeviceStateChange `json:"changed,omitempty"`
	Removed        []string                     `json:"removed,omitempty"`
	Errors         []string                     `json:"errors,omitempty"`
}

// DeviceStateChange covers plist, sqlite, and sentry_envelope entry
// shapes — they share the same envelope but populate different
// sub-fields. Kept loosely typed because plist `values` can be any
// dict shape.
type DeviceStateChange struct {
	Kind       string                         `json:"kind"` // "plist" | "sqlite" | "sentry_envelope"
	Size       int64                          `json:"size"`
	MtimeNs    int64                          `json:"mtime_ns"`
	Values     map[string]interface{}         `json:"values,omitempty"` // plist
	XML        *string                        `json:"xml,omitempty"`    // plist
	Tables     map[string]SQLiteTableSnapshot `json:"tables,omitempty"` // sqlite
	Companions []map[string]interface{}       `json:"companions,omitempty"`
	Items      []SentryEnvelopeItem           `json:"items,omitempty"`
	Skipped    string                         `json:"skipped,omitempty"` // "size_cap" etc. — sentry envelopes set this when oversized
}

// SentryEnvelopeItem is one identifying item extracted from a Sentry
// envelope on disk. Only event / transaction / replay_event item
// types are surfaced — the rest (sessions, profiles, attachments)
// don't carry a clean "paste into Sentry" handle.
type SentryEnvelopeItem struct {
	Type        string `json:"type"` // "event" | "transaction" | "replay_event"
	EventID     string `json:"event_id,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	SpanID      string `json:"span_id,omitempty"`
	ReplayID    string `json:"replay_id,omitempty"`
	Transaction string `json:"transaction,omitempty"` // for transactions: e.g. "POST /api/whops"
	Timestamp   string `json:"timestamp,omitempty"`
}

// SQLiteTableSnapshot is the per-table payload inside a sqlite change.
// `Rows` contains up to ~20 sampled rows from the helper.
type SQLiteTableSnapshot struct {
	Schema *string         `json:"schema,omitempty"`
	Count  *int            `json:"count,omitempty"`
	Cols   []string        `json:"cols,omitempty"`
	Rows   [][]interface{} `json:"rows,omitempty"`
	Error  *string         `json:"error,omitempty"`
}

// IsFloor reports whether this line was emitted by the live-session
// floor heartbeat (Phase 3.7) rather than a real step / action.
func (l *DeviceStateLine) IsFloor() bool {
	if l.StepID == "__live_floor__" {
		return true
	}
	return l.ActionType != nil && *l.ActionType == "floor"
}

func (l *DeviceStateLine) IsSnapshot() bool {
	if l.Type != nil && *l.Type == "snapshot" {
		return true
	}
	return l.ActionType != nil && *l.ActionType == "snapshot"
}
