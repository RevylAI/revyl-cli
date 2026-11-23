package main

import (
	"encoding/json"
	"testing"

	mcppkg "github.com/revyl/cli/internal/mcp"
)

func TestLiveStepEngineStatusNamesWhoAnsweredALocalInferenceStep(t *testing.T) {
	for _, tc := range []struct {
		output, want string
	}{
		{output: `{"status":"success","engine":"local","local_outcome":"passed"}`, want: "local"},
		{output: `{"status":"success","engine":"hosted","local_outcome":"uncertain"}`, want: "hosted_after_local_uncertain"},
		{output: `{"status":"success","engine":"hosted","local_outcome":"unavailable"}`, want: "hosted_after_local_unavailable"},
		{output: `{"status":"success","engine":"hosted"}`, want: "hosted"},
		{output: `{"status":"success","engine":"hosted","local_outcome":"made-up"}`, want: "hosted"},
		{output: `{"status":"success","engine":"other"}`, want: ""},
		{output: `{"status":"success"}`, want: ""},
	} {
		got := liveStepEngineStatus(&mcppkg.LiveStepResponse{StepOutput: json.RawMessage(tc.output)})
		if got != tc.want {
			t.Errorf("liveStepEngineStatus(%s) = %q, want %q", tc.output, got, tc.want)
		}
	}
}
