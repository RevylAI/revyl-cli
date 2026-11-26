package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func computerPersistentJSON(state string, savedAt any) map[string]any {
	return map[string]any{
		"computer_id": computerValidationID.String(), "state": state,
		"run_session_id": computerValidationID.String(), "saved_at": savedAt, "durable": false, "created_at": "2026-10-10T00:00:00Z",
	}
}

func TestPersistentComputerStopWaitRereadsUntilTheDiskIsSaved(t *testing.T) {
	previous := computerPersistentPollInterval
	computerPersistentPollInterval = time.Millisecond
	t.Cleanup(func() { computerPersistentPollInterval = previous })
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		body := computerValidReportJSON()
		body["persistent"] = computerPersistentJSON("stopping", nil)
		if len(requests) > 2 {
			body["persistent"] = computerPersistentJSON("stopped", "2026-10-10T00:04:00Z")
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)

	report, status, err := client.StopComputerRun(context.Background(), computerValidationID)
	if err != nil || report.ComputerSessionId != computerValidationID || status == nil || status.State != PersistentComputerStatusStateStopping {
		t.Fatalf("stop returned %v, %v, %v", report, status, err)
	}
	status, err = client.AwaitPersistentComputer(context.Background(), status, PersistentComputerStatusStateStopping)

	if err != nil || status.State != PersistentComputerStatusStateStopped || status.SavedAt == nil {
		t.Fatalf("stop wait returned %v, %v", status, err)
	}
	path := "/api/v1/computer-runs/" + computerValidationID.String()
	if want := []string{"POST " + path + "/stop", "GET " + path, "GET " + path}; len(requests) != len(want) || requests[0] != want[0] || requests[1] != want[1] || requests[2] != want[2] {
		t.Fatalf("stop wait sent %v, want %v", requests, want)
	}
}

func TestStopReturnsAPersistentStatusOnlyForThePersistentComputerItNamed(t *testing.T) {
	other := computerPersistentJSON("stopping", nil)
	other["computer_id"] = "99999999-9999-4999-8999-999999999999"
	for name, test := range map[string]struct {
		persistent    any
		wantStatus    bool
		wantRefusalOK bool
	}{
		"ordinary":       {nil, false, true},
		"persistent":     {computerPersistentJSON("stopping", nil), true, true},
		"other_computer": {other, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := computerValidReportJSON()
				if test.persistent != nil {
					body["persistent"] = test.persistent
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			report, status, err := NewClientWithBaseURL("fixture-key", server.URL).StopComputerRun(context.Background(), computerValidationID)
			if !test.wantRefusalOK {
				if err == nil {
					t.Fatalf("stop accepted another computer's status %v", status)
				}
				return
			}
			if err != nil || report == nil || (status != nil) != test.wantStatus {
				t.Fatalf("stop returned %v, %v, %v", report, status, err)
			}
		})
	}
}

func TestPersistentComputerResponsesBindToTheRequestedComputer(t *testing.T) {
	other := computerPersistentJSON("stopped", nil)
	other["computer_id"] = "99999999-9999-4999-8999-999999999999"
	badState := computerPersistentJSON("archived", nil)
	for name, test := range map[string]struct {
		status int
		body   any
		code   string
	}{
		"other_computer":  {http.StatusOK, other, ""},
		"unknown_state":   {http.StatusOK, badState, ""},
		"saved_disk_gone": {http.StatusServiceUnavailable, map[string]any{"detail": "saved_disk_unavailable"}, "saved_disk_unavailable"},
		"too_old":         {http.StatusConflict, map[string]any{"detail": "saved_disk_expired"}, "saved_disk_expired"},
		"not_enabled":     {http.StatusForbidden, map[string]any{"detail": "persistent_computers_not_enabled", "message": "Persistent computers aren't enabled for this organization. Contact support@revyl.ai to turn them on."}, "persistent_computers_not_enabled"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_ = json.NewEncoder(w).Encode(test.body)
			}))
			defer server.Close()
			_, err := NewClientWithBaseURL("fixture-key", server.URL).StartComputer(context.Background(), computerValidationID, PersistentComputerStartRequest{})
			var refusal *APIError
			if err == nil || test.code != "" && (!errors.As(err, &refusal) || refusal.Code != test.code) {
				t.Fatalf("start returned %v, want refusal %q", err, test.code)
			}
		})
	}
}
