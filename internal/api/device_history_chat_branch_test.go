package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDeviceHistoryPreservesChatFieldsAndBranchFilters(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	filters := url.Values{"limit": {"7"}, "offset": {"14"}, "app_id": {appID}, "branch_id": {branchID}, "build_id": {buildID}, "source": {"explore"}, "status": {"all"}, "created_from": {"2026-10-01T00:00:00Z"}, "created_to": {"2026-10-08T00:00:00Z"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/execution/device-sessions/history" || r.URL.Query().Encode() != filters.Encode() {
			t.Errorf("wrong request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"limit": 7, "offset": 14, "total": 20, "sessions": []map[string]interface{}{{"id": "session", "app_id": appID, "build_id": buildID, "branch_id": branchID, "branch_name": "release", "app_name": "Fixture", "build_version": "1.0", "source": "explore", "report_id": "report", "test_execution_id": "execution", "test_name": "Smoke", "started_at": "start", "ended_at": "end", "step_count": 4}}})
	}))
	defer server.Close()
	result, err := NewClientWithBaseURL("fixture", server.URL).QueryDeviceSessionHistory(context.Background(), filters)
	if err != nil {
		t.Fatal(err)
	}
	if result.Limit != 7 || result.Offset != 14 || result.Total != 20 || len(result.Sessions) != 1 {
		t.Fatalf("pagination lost: %+v", result)
	}
	row := result.Sessions[0]
	if row.BranchID != branchID || row.BranchName != "release" || row.StepCount != 4 {
		t.Fatalf("branch or step count lost: %+v", row)
	}
	for key, value := range map[string]*string{"app_id": row.AppID, "build_id": row.BuildID, "app_name": row.AppName, "build_version": row.BuildVersion, "source": row.Source, "report_id": row.ReportID, "test_execution_id": row.TestExecutionID, "test_name": row.TestName, "started_at": row.StartedAt, "ended_at": row.EndedAt} {
		if value == nil || *value == "" {
			t.Errorf("chat field %s lost", key)
		}
	}
}
