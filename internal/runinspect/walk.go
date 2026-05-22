package runinspect

import "strconv"

// Shared infrastructure for any detector that walks the recorded
// JSONL: source location tagging, step-id → step-index resolution,
// and the recursive plist walker. Kept here so `identity.go` /
// `state.go` / future detectors don't all duplicate it.

// FieldSource pinpoints where a value was read from. Kind is "plist"
// or "sqlite"; Path is container-relative; KeyPath is dotted for
// nested plists, column-qualified for sqlite.
type FieldSource struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	KeyPath string `json:"key_path"`
	Column  string `json:"column,omitempty"` // sqlite only
	RowID   *int64 `json:"row_id,omitempty"` // sqlite only
}

// StepIndexer maps step_ids back to their 1-indexed execution_order.
// Implementations come from the Report (authoritative via
// `IndexerFromReport`) or a positional fallback.
type StepIndexer interface {
	IndexFor(stepID string, fallback int) int
}

// MapIndexer is the concrete StepIndexer keyed by step_id.
type MapIndexer map[string]int

// IndexFor implements StepIndexer.
func (m MapIndexer) IndexFor(stepID string, fallback int) int {
	if idx, ok := m[stepID]; ok {
		return idx
	}
	return fallback
}

// IndexerFromReport builds a MapIndexer from the report's steps.
func IndexerFromReport(r *Report) MapIndexer {
	m := make(MapIndexer, len(r.Steps))
	for _, s := range r.Steps {
		if s.StepID != "" {
			m[s.StepID] = s.ExecutionOrder
		}
	}
	return m
}

// walkPlist recursively visits every leaf value in a plist dict,
// building a dotted key_path for nested structure. Lists are
// indexed positionally (“items.0.id“).
func walkPlist(
	values map[string]interface{},
	prefix string,
	visit func(keyPath string, value interface{}),
) {
	for k, v := range values {
		var path string
		if prefix == "" {
			path = k
		} else {
			path = prefix + "." + k
		}
		switch t := v.(type) {
		case map[string]interface{}:
			walkPlist(t, path, visit)
		case []interface{}:
			for i, item := range t {
				idxPath := path + "." + strconv.Itoa(i)
				if nested, ok := item.(map[string]interface{}); ok {
					walkPlist(nested, idxPath, visit)
				} else {
					visit(idxPath, item)
				}
			}
		default:
			visit(path, v)
		}
	}
}
