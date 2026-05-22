package runinspect

import "strconv"

// State inspection: list the captured paths in a run, or pull the
// latest captured snapshot for a single path. No detection, no
// classification — this is the "let me peek directly" escape hatch.

// CapturedPath is one row of the `revyl run state` listing.
type CapturedPath struct {
	Path          string `json:"path"`
	Kind          string `json:"kind"`                  // "plist" or "sqlite"
	KeyCount      int    `json:"key_count,omitempty"`   // plist
	TableCount    int    `json:"table_count,omitempty"` // sqlite
	FirstSeenStep int    `json:"first_seen_step"`
	LastSeenStep  int    `json:"last_seen_step"`
	Rotated       bool   `json:"rotated"`
}

// PathSnapshot is the latest captured shape for a single path.
type PathSnapshot struct {
	Kind          string                  `json:"kind"` // "plist" or "sqlite"
	Size          int64                   `json:"size"`
	FirstSeenStep int                     `json:"first_seen_step"`
	LastSeenStep  int                     `json:"last_seen_step"`
	Rotated       bool                    `json:"rotated"`
	Values        map[string]interface{}  `json:"values,omitempty"` // plist
	Tables        map[string]TableSummary `json:"tables,omitempty"` // sqlite
}

// TableSummary is the simplified per-table view inside PathSnapshot.
// Drops the sampled-rows payload (often hundreds of KB) and surfaces
// just the schema + counts an agent needs to decide whether to drill
// deeper.
type TableSummary struct {
	Schema      string `json:"schema,omitempty"`
	RowCount    int    `json:"row_count"`
	ColumnCount int    `json:"column_count"`
}

// ListCapturedPaths walks the JSONL and returns one row per unique
// captured path, with step range and a rotation signal.
//
// When “atStep > 0“, only lines whose step index is ≤ atStep
// contribute — useful for "what state existed as of step N".
func ListCapturedPaths(
	lines []DeviceStateLine,
	indexer StepIndexer,
	atStep int,
) []CapturedPath {
	type rollup struct {
		kind          string
		keyCount      int
		tableCount    int
		firstStep     int
		lastStep      int
		firstKeyCount int
		lastKeyCount  int
		firstTables   int
		lastTables    int
	}
	bucket := make(map[string]*rollup)
	order := make([]string, 0)

	for i, line := range lines {
		if line.IsFloor() {
			continue
		}
		stepIdx := indexer.IndexFor(line.StepID, i+1)
		if atStep > 0 && stepIdx > atStep {
			continue
		}
		for path, change := range line.Changed {
			r, exists := bucket[path]
			if !exists {
				r = &rollup{kind: change.Kind, firstStep: stepIdx, lastStep: stepIdx}
				bucket[path] = r
				order = append(order, path)
			}
			r.kind = change.Kind
			if stepIdx < r.firstStep {
				r.firstStep = stepIdx
			}
			if stepIdx > r.lastStep {
				r.lastStep = stepIdx
			}
			switch change.Kind {
			case "plist":
				kc := len(change.Values)
				if r.firstKeyCount == 0 && !exists {
					r.firstKeyCount = kc
				}
				r.lastKeyCount = kc
				r.keyCount = kc
			case "sqlite":
				tc := len(change.Tables)
				if r.firstTables == 0 && !exists {
					r.firstTables = tc
				}
				r.lastTables = tc
				r.tableCount = tc
			}
		}
	}

	out := make([]CapturedPath, 0, len(order))
	for _, path := range order {
		r := bucket[path]
		rotated := false
		switch r.kind {
		case "plist":
			rotated = r.firstKeyCount != 0 && r.firstKeyCount != r.lastKeyCount
		case "sqlite":
			rotated = r.firstTables != 0 && r.firstTables != r.lastTables
		}
		out = append(out, CapturedPath{
			Path:          path,
			Kind:          r.kind,
			KeyCount:      r.keyCount,
			TableCount:    r.tableCount,
			FirstSeenStep: r.firstStep,
			LastSeenStep:  r.lastStep,
			Rotated:       rotated,
		})
	}
	return out
}

// LatestStateForPath returns the most recent (per step index)
// captured snapshot for the given path, or nil if the path never
// appeared. When “atStep > 0“, the most-recent snapshot with step
// index ≤ atStep is returned.
func LatestStateForPath(
	lines []DeviceStateLine,
	indexer StepIndexer,
	path string,
	atStep int,
) *PathSnapshot {
	var firstStep, lastStep int
	var latest *DeviceStateChange
	var firstSig string

	for i, line := range lines {
		if line.IsFloor() {
			continue
		}
		stepIdx := indexer.IndexFor(line.StepID, i+1)
		if atStep > 0 && stepIdx > atStep {
			continue
		}
		change, ok := line.Changed[path]
		if !ok {
			continue
		}
		if firstStep == 0 || stepIdx < firstStep {
			firstStep = stepIdx
			firstSig = changeSignature(change)
		}
		if stepIdx >= lastStep {
			lastStep = stepIdx
			c := change
			latest = &c
		}
	}
	if latest == nil {
		return nil
	}
	snap := &PathSnapshot{
		Kind:          latest.Kind,
		Size:          latest.Size,
		FirstSeenStep: firstStep,
		LastSeenStep:  lastStep,
		Rotated:       firstSig != changeSignature(*latest),
	}
	switch latest.Kind {
	case "plist":
		snap.Values = latest.Values
	case "sqlite":
		snap.Tables = make(map[string]TableSummary, len(latest.Tables))
		for name, t := range latest.Tables {
			ts := TableSummary{ColumnCount: len(t.Cols)}
			if t.Schema != nil {
				ts.Schema = *t.Schema
			}
			if t.Count != nil {
				ts.RowCount = *t.Count
			}
			snap.Tables[name] = ts
		}
	}
	return snap
}

// changeSignature collapses a change into a cheap "did it move"
// signal for rotation detection. We use key/table count + size; a
// real structural diff is overkill here.
func changeSignature(c DeviceStateChange) string {
	switch c.Kind {
	case "plist":
		return c.Kind + "|" + strconv.Itoa(len(c.Values)) + "|" + strconv.Itoa(int(c.Size))
	case "sqlite":
		return c.Kind + "|" + strconv.Itoa(len(c.Tables)) + "|" + strconv.Itoa(int(c.Size))
	}
	return c.Kind + "|" + strconv.Itoa(int(c.Size))
}
