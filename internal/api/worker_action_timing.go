package api

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

type WorkerActionTiming struct {
	GroundingBundleDurationMs json.Number `json:"grounding_bundle_duration_ms,omitempty"`
	InputDurationMs           json.Number `json:"input_duration_ms,omitempty"`
	SettleDurationMs          json.Number `json:"settle_duration_ms,omitempty"`
}

func ParseWorkerActionTiming(body []byte) WorkerActionTiming {
	var fields struct {
		GroundingBundleDurationMs json.RawMessage `json:"grounding_bundle_duration_ms"`
		InputDurationMs           json.RawMessage `json:"input_duration_ms"`
		SettleDurationMs          json.RawMessage `json:"settle_duration_ms"`
	}
	if json.Unmarshal(body, &fields) != nil {
		return WorkerActionTiming{}
	}
	return WorkerActionTiming{
		GroundingBundleDurationMs: parseWorkerDurationMs(fields.GroundingBundleDurationMs),
		InputDurationMs:           parseWorkerDurationMs(fields.InputDurationMs),
		SettleDurationMs:          parseWorkerDurationMs(fields.SettleDurationMs),
	}
}

func parseWorkerDurationMs(raw json.RawMessage) json.Number {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 64 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return ""
	}
	var durationMs float64
	if json.Unmarshal(raw, &durationMs) != nil || durationMs < 0 || math.IsInf(durationMs, 0) || math.IsNaN(durationMs) {
		return ""
	}
	number := string(raw)
	if math.Signbit(durationMs) && strings.ContainsAny(strings.SplitN(strings.ToLower(number), "e", 2)[0], "123456789") {
		return ""
	}
	return json.Number(number)
}
