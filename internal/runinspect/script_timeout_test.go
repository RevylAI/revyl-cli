package runinspect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestRecordedScriptTimeoutSurvivesReportAndSummary(t *testing.T) {
	const raw = `{"id":"report-fixture","steps":[
 {"id":"deadline","step_type":"code_execution","execution_order":1,"type_data":{"timeout_seconds":3,"exit_code":124}},
 {"id":"default","step_type":"code_execution","execution_order":2,"type_data":{"timeout_seconds":60,"exit_code":0}},
 {"id":"legacy","step_type":"code_execution","execution_order":3,"type_data":{"exit_code":0}},
 {"id":"cancelled","step_type":"code_execution","execution_order":4,"type_data":null},
 {"id":"manual","step_type":"manual","execution_order":5,"type_data":{"timeout_seconds":90}}
 ]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	}))
	defer server.Close()
	client := api.NewClientWithBaseURL("fixture", server.URL)
	report, err := FetchReportReference(context.Background(), client, "execution-fixture", "execution")
	if err != nil {
		t.Fatal(err)
	}
	summary := BuildSummary(report, nil)
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Steps []map[string]json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"3", "60", "", "", ""} {
		if got := string(output.Steps[i]["timeout_seconds"]); got != want {
			t.Fatalf("step %d timeout = %s, want %s", i, got, want)
		}
	}
	page, err := InspectReport(json.RawMessage(raw), InvestigationOptions{Limit: 20, Section: "field", StepID: "deadline", Field: "/type_data/timeout_seconds"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Field.Content != "3" {
		t.Fatalf("inspection lost recorded timeout: %+v", page.Field)
	}
}
