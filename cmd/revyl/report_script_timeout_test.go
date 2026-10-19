package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/runinspect"
	"github.com/spf13/cobra"
)

func TestReportJSONPreservesRecordedScriptTimeout(t *testing.T) {
	output, err := buildUserFacingReportJSON([]byte(`{"steps":[{"step_type":"code_execution","type_data":{"timeout_seconds":3,"exit_code":124,"stdout":"partial"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Steps []struct {
			TypeData map[string]json.RawMessage `json:"type_data"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if got := string(result.Steps[0].TypeData["timeout_seconds"]); got != "3" {
		t.Fatalf("timeout lost from report JSON: %s", output)
	}
}

func TestSummaryTextShowsRecordedScriptTimeout(t *testing.T) {
	seconds := 3
	output := new(bytes.Buffer)
	command := &cobra.Command{}
	command.SetOut(output)
	summary := runinspect.Summary{Steps: []runinspect.SummaryStep{
		{StepType: "code_execution", TimeoutSeconds: &seconds},
		{StepType: "code_execution"},
	}}
	if err := printSummary(command, summary); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Timeout: 3s", "Timeout: not recorded"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output)
		}
	}
}
