package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

var computerValidationID = uuid.MustParse("11111111-1111-4111-8111-111111111111")

func TestComputerOriginalSchemaAcceptsValidOperation(t *testing.T) {
	body := []byte(`{"computer_session_id":"11111111-1111-4111-8111-111111111111","operation_id":"22222222-2222-4222-8222-222222222222","evidence":"recorded","execution":{"outcome":"succeeded","result":{"kind":"exec","exit_code":0,"stdout":"","stderr":""}}}`)
	if err := validateComputerResponseSchema(body, "ComputerCommandResponse"); err != nil {
		t.Fatal(err)
	}
}

func TestComputerResponseSchemaDefaultsAndNestedConstraints(t *testing.T) {
	for _, name := range []string{"full", "defaults", "failed_cleanup", "owner_lost_cleanup", "child_report"} {
		report := computerValidReportJSON()
		switch name {
		case "defaults":
			for _, key := range []string{"kind", "version", "state", "release", "evidence_write_status", "evidence_completeness", "steps", "child_reports"} {
				delete(report, key)
			}
		case "failed_cleanup":
			report["state"], report["release"], report["released_at"], report["evidence_completeness"] = "failed", "confirmed", "2026-10-10T00:00:02Z", "complete"
			delete(report, "execution_started_at")
		case "owner_lost_cleanup":
			report["state"], report["release"], report["released_at"] = "owner_lost", "confirmed", "2026-10-10T00:00:02Z"
		case "child_report":
			report["child_reports"] = []any{map[string]any{"session_id": uuid.New(), "report_id": uuid.New(), "platform": "ios"}}
		}
		body, _ := json.Marshal(report)
		recorder := httptest.NewRecorder()
		recorder.Write(body)
		if _, err := readComputerReport(recorder.Result(), nil, computerValidationID); err != nil {
			t.Fatalf("valid %s report rejected: %v", name, err)
		}
	}
	for _, mutate := range []func(map[string]any){
		func(report map[string]any) { report["created_at"] = "not-a-timestamp" },
		func(report map[string]any) { report["report_id"] = "not-a-uuid" },
		func(report map[string]any) { report["state"] = "private-canary" },
		func(report map[string]any) { report["release"] = true },
		func(report map[string]any) { report["as_of"] = "2026-10-09T00:00:00Z" },
		func(report map[string]any) { report["evidence_completeness"] = "complete" },
		func(report map[string]any) { report["child_reports"] = []any{map[string]any{"platform": "ios"}} },
	} {
		report := computerValidReportJSON()
		mutate(report)
		body, _ := json.Marshal(report)
		recorder := httptest.NewRecorder()
		recorder.Write(body)
		if _, err := readComputerReport(recorder.Result(), nil, computerValidationID); err == nil {
			t.Fatal("invalid report accepted")
		}
	}
}

func TestComputerATIFValidatesNestedContractAndCorrelations(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{
			"session_id": computerValidationID, "trajectory_id": "22222222-2222-4222-8222-222222222222",
			"steps": []any{map[string]any{"step_id": 1, "timestamp": "2026-10-10T00:00:01Z", "source": "agent", "message": "Computer operation accepted", "llm_call_count": 0,
				"tool_calls": []any{map[string]any{"tool_call_id": "33333333-3333-4333-8333-333333333333", "function_name": "computer.exec", "arguments": map[string]any{"kind": "exec"}}},
				"observation": map[string]any{"results": []any{map[string]any{"source_call_id": "33333333-3333-4333-8333-333333333333", "content": "succeeded", "extra": map[string]any{"revyl": map[string]any{"evidence": map[string]any{
					"step_id": "33333333-3333-4333-8333-333333333333", "execution_order": 1, "kind": "exec", "started_at": "2026-10-10T00:00:01Z", "completed_at": "2026-10-10T00:00:02Z", "outcome": "succeeded", "summary": map[string]any{"kind": "exec", "exit_code": 0, "stdout_byte_count": 0, "stderr_byte_count": 0},
				}}}}}},
			}},
			"extra": map[string]any{"revyl": map[string]any{"report_id": "22222222-2222-4222-8222-222222222222", "os": "macos", "as_of": "2026-10-10T00:00:03Z", "expires_at": "2026-10-10T00:05:00Z", "execution_ended_at": nil, "released_at": nil, "state": "active", "release": "pending", "evidence_completeness": "partial", "evidence_write_status": "recorded", "child_reports": []any{}}},
		}
	}
	for _, name := range []string{"valid_defaults", "invalid_source", "wrong_call", "wrong_outcome", "wrong_time", "bad_summary", "nonsequential", "empty_results", "extra_results", "wrong_function", "wrong_trajectory"} {
		t.Run(name, func(t *testing.T) {
			body := valid()
			step := body["steps"].([]any)[0].(map[string]any)
			observation := step["observation"].(map[string]any)
			result := observation["results"].([]any)[0].(map[string]any)
			evidence := result["extra"].(map[string]any)["revyl"].(map[string]any)["evidence"].(map[string]any)
			switch name {
			case "invalid_source":
				step["source"] = "private-canary"
			case "wrong_call":
				result["source_call_id"] = uuid.New()
			case "wrong_outcome":
				result["content"] = "failed"
			case "wrong_time":
				evidence["started_at"] = "2026-10-10T00:00:00Z"
			case "bad_summary":
				evidence["summary"].(map[string]any)["stdout_byte_count"] = -1
			case "nonsequential":
				step["step_id"] = 2
			case "empty_results":
				observation["results"] = []any{}
			case "extra_results":
				observation["results"] = []any{result, result}
			case "wrong_function":
				step["tool_calls"].([]any)[0].(map[string]any)["function_name"] = "computer.file_read"
			case "wrong_trajectory":
				body["trajectory_id"] = uuid.New()
			}
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			_, _ = recorder.Write(data)
			_, err = readComputerATIF(recorder.Result(), nil, computerValidationID)
			if (err == nil) != (name == "valid_defaults") {
				t.Fatalf("unexpected acceptance: %v", err)
			}
		})
	}
}

func TestComputerSchemaRejectsNestedNumericRequiredAndEnumViolations(t *testing.T) {
	for _, result := range []string{
		`{"kind":"exec","stdout":"","stderr":""}`,
		`{"kind":"exec","exit_code":0.5}`,
		`{"kind":"file_stat","file_kind":"private-canary","size_bytes":0}`,
		`{"kind":"file_stat","file_kind":"file","size_bytes":9223372036854775808}`,
		`{"kind":"process_list","processes":[{}]}`,
		`{"kind":"file_write","byte_count":-1}`,
	} {
		body := []byte(`{"computer_session_id":"11111111-1111-4111-8111-111111111111","operation_id":"22222222-2222-4222-8222-222222222222","evidence":"recorded","execution":{"outcome":"succeeded","result":` + result + `}}`)
		recorder := httptest.NewRecorder()
		recorder.Write(body)
		if _, err := readComputerOperation(recorder.Result(), nil, computerValidationID, uuid.MustParse("22222222-2222-4222-8222-222222222222"), false); err == nil {
			t.Fatal("invalid nested operation accepted")
		}
	}
}

func TestComputerSubmitBindsResponseToRequestedLimitsAndHandle(t *testing.T) {
	for _, kind := range []string{"read", "list", "inspect"} {
		request := ComputerCommandRequest{ComputerSessionId: computerValidationID, RequestId: uuid.New()}
		limit := 1
		handle := ComputerProcessHandle{ComputerSessionId: computerValidationID, HandleId: uuid.New()}
		var result string
		switch kind {
		case "read":
			_ = request.Command.FromComputerFileRead(ComputerFileRead{Path: "file", MaxBytes: &limit})
			result = `{"kind":"file_read","data":"eHg="}`
		case "list":
			_ = request.Command.FromComputerProcessList(ComputerProcessList{MaxItems: &limit})
			result = `{"kind":"process_list","processes":[{"handle":{"computer_session_id":"11111111-1111-4111-8111-111111111111","handle_id":"33333333-3333-4333-8333-333333333333"},"state":"running"},{"handle":{"computer_session_id":"11111111-1111-4111-8111-111111111111","handle_id":"44444444-4444-4444-8444-444444444444"},"state":"running"}]}`
		case "inspect":
			_ = request.Command.FromComputerProcessInspect(ComputerProcessInspect{Handle: handle})
			result = `{"kind":"process_inspect","process":{"handle":{"computer_session_id":"11111111-1111-4111-8111-111111111111","handle_id":"33333333-3333-4333-8333-333333333333"},"state":"running"}}`
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := map[string]any{"computer_session_id": computerValidationID, "operation_id": ComputerOperationID(computerValidationID, request.RequestId), "evidence": "recorded", "execution": map[string]any{"outcome": "succeeded", "result": json.RawMessage(result)}}
			_ = json.NewEncoder(w).Encode(body)
		}))
		_, err := NewClientWithBaseURL("fixture-key", server.URL).SubmitComputerOperation(context.Background(), request)
		server.Close()
		if err == nil {
			t.Fatalf("mismatched %s response accepted", kind)
		}
	}
}

func TestComputerReportRejectsImpossibleLifecycleAndSummaries(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(report map[string]any) {
			report["release"] = "confirmed"
			report["released_at"] = "2026-10-10T00:00:01Z"
		},
		func(report map[string]any) { report["evidence_completeness"] = "private-canary" },
		func(report map[string]any) {
			report["steps"] = []any{map[string]any{
				"step_id": "33333333-3333-4333-8333-333333333333", "execution_order": 1,
				"kind": "exec", "started_at": "2026-10-10T00:00:01Z", "completed_at": "2026-10-10T00:00:02Z",
				"outcome": "succeeded", "summary": map[string]any{"kind": "exec", "exit_code": 7, "stdout_byte_count": -1, "stderr_byte_count": 999999999},
			}}
		},
	} {
		report := computerValidReportJSON()
		mutate(report)
		body, _ := json.Marshal(report)
		recorder := httptest.NewRecorder()
		recorder.WriteHeader(http.StatusOK)
		_, _ = recorder.Write(body)
		if _, err := readComputerReport(recorder.Result(), nil, computerValidationID); err == nil {
			t.Fatal("malformed report accepted")
		}
	}
}

func computerValidReportJSON() map[string]any {
	return map[string]any{
		"kind": "computer", "version": 1, "computer_session_id": computerValidationID.String(),
		"report_id": "22222222-2222-4222-8222-222222222222", "org_id": "44444444-4444-4444-8444-444444444444",
		"os": "macos", "created_at": "2026-10-10T00:00:00Z", "expires_at": "2026-10-10T00:05:00Z",
		"as_of": "2026-10-10T00:00:03Z", "state": "active", "execution_started_at": "2026-10-10T00:00:00Z",
		"release": "pending", "evidence_completeness": "partial", "evidence_write_status": "recorded", "steps": []any{}, "child_reports": []any{},
	}
}

func TestComputerGetCarriesCommandDisplayOnlyOnTheDetailRead(t *testing.T) {
	display := "printenv API_KEY=<redacted>"
	report := computerValidReportJSON()
	report["steps"] = []any{map[string]any{
		"step_id": "33333333-3333-4333-8333-333333333333", "execution_order": 1,
		"kind": "exec", "started_at": "2026-10-10T00:00:01Z", "completed_at": "2026-10-10T00:00:02Z",
		"outcome": "succeeded", "summary": map[string]any{"kind": "exec", "exit_code": 0, "stdout_byte_count": ComputerMaxOutputBytes, "stderr_byte_count": 0},
		"command_display": display,
	}}
	body, _ := json.Marshal(report)
	for path, wantDisplay := range map[string]bool{"/api/v1/computer-runs/" + computerValidationID.String(): true, "/api/v1/computer-runs/" + computerValidationID.String() + "/report": false} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)
		}))
		client := NewClientWithBaseURL("fixture-key", server.URL)
		if wantDisplay {
			detail, err := client.GetComputerRun(context.Background(), computerValidationID)
			if err != nil || detail.Steps == nil || len(*detail.Steps) != 1 || (*detail.Steps)[0].CommandDisplay == nil || *(*detail.Steps)[0].CommandDisplay != display {
				t.Fatalf("detail read dropped the command display: %v", err)
			}
		} else if _, err := client.GetComputerReport(context.Background(), computerValidationID); err == nil {
			t.Fatal("owner report accepted a command display")
		}
		server.Close()
	}
	for key, value := range map[string]any{"outcome": "private-canary", "execution_order": 2} {
		invalid := computerValidReportJSON()
		step := map[string]any{}
		for field, original := range report["steps"].([]any)[0].(map[string]any) {
			step[field] = original
		}
		step[key] = value
		invalid["steps"] = []any{step}
		body, _ := json.Marshal(invalid)
		recorder := httptest.NewRecorder()
		_, _ = recorder.Write(body)
		if _, err := readComputerReportDetail(recorder.Result(), nil, computerValidationID); err == nil {
			t.Fatalf("invalid detail step %s accepted", key)
		}
	}
}

func TestComputerListAcceptsOnlyTheRequestedContractPage(t *testing.T) {
	summary := map[string]any{
		"computer_session_id": computerValidationID.String(), "os": "macos",
		"created_at": "2026-10-10T00:00:00Z", "expires_at": "2026-10-10T01:00:00Z",
		"state": "active", "release": "pending", "released_at": nil,
		"execution_started_at": "2026-10-10T00:00:30Z", "execution_ended_at": nil,
		"evidence_write_status": "recorded", "evidence_completeness": "complete",
	}
	page := func(edit func(map[string]any, map[string]any)) []byte {
		computer := map[string]any{}
		for key, value := range summary {
			computer[key] = value
		}
		list := map[string]any{"computers": []any{computer}, "total": 1, "limit": 5, "offset": 0}
		edit(list, computer)
		body, _ := json.Marshal(list)
		return body
	}
	for name, test := range map[string]struct {
		body []byte
		ok   bool
	}{
		"valid": {page(func(map[string]any, map[string]any) {}), true},
		"unknown_field": {page(func(list map[string]any, computer map[string]any) {
			list["added_later"] = true
			computer["added_later"] = map[string]any{"kind": "new"}
		}), true},
		"invalid_state": {page(func(_ map[string]any, computer map[string]any) { computer["state"] = "running" }), false},
		"other_limit":   {page(func(list map[string]any, _ map[string]any) { list["limit"] = 25 }), false},
		"short_total":   {page(func(list map[string]any, _ map[string]any) { list["total"] = 0 }), false},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/computer-runs" || r.URL.RawQuery != "limit=5" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			list, err := NewClientWithBaseURL("fixture-key", server.URL).ListComputerRuns(context.Background(), 5)
			if test.ok != (err == nil) || test.ok && (len(list.Computers) != 1 || list.Computers[0].ComputerSessionId != computerValidationID) {
				t.Fatalf("list read returned %v", err)
			}
		})
	}
}
