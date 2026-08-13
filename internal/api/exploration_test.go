package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestLaunchExplorationSerializesTypedRequest(t *testing.T) {
	t.Parallel()

	appID := uuid.NewString()
	buildID := uuid.New()
	launchVarID := uuid.New()
	var captured ExplorationLaunchRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/explorations/apps/"+appID {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"run": {
				"id": "run-1",
				"app_id": "app-1",
				"mode": "explore",
				"execution_status": "queued",
				"atlas_status": "not_started",
				"customer_status": "not_ready",
				"created_at": "2026-08-11T20:00:00Z",
				"updated_at": "2026-08-11T20:00:00Z"
			},
			"report_url": "https://app.revyl.ai/explorations/report?runId=run-1"
		}`))
	}))
	defer server.Close()

	platform := "ios"
	lanes := 4
	strategy := "surface_sweep"
	env := map[string]string{"API_HOST": "local"}
	result, err := NewClientWithBaseURL("token", server.URL).LaunchExploration(
		context.Background(),
		appID,
		&ExplorationLaunchRequest{
			BuildId:         &buildID,
			Platform:        &platform,
			LaneCount:       &lanes,
			SwarmStrategy:   &strategy,
			LaunchEnvVarIds: &[]uuid.UUID{launchVarID},
			EnvVars:         &env,
		},
	)
	if err != nil {
		t.Fatalf("LaunchExploration(): %v", err)
	}
	if result.Run.Id != "run-1" {
		t.Fatalf("run id = %q", result.Run.Id)
	}
	if captured.BuildId == nil || *captured.BuildId != buildID {
		t.Fatalf("build id = %v, want %s", captured.BuildId, buildID)
	}
	if captured.LaneCount == nil || *captured.LaneCount != lanes {
		t.Fatalf("lane count = %v, want %d", captured.LaneCount, lanes)
	}
	if captured.LaunchEnvVarIds == nil || len(*captured.LaunchEnvVarIds) != 1 || (*captured.LaunchEnvVarIds)[0] != launchVarID {
		t.Fatalf("launch vars = %#v", captured.LaunchEnvVarIds)
	}
}

func TestLaunchExplorationDoesNotRetry(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, `{"detail":"temporary failure"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClientWithBaseURL("token", server.URL)
	_, err := client.LaunchExploration(
		context.Background(),
		uuid.NewString(),
		&ExplorationLaunchRequest{},
	)
	if err == nil {
		t.Fatal("LaunchExploration() error = nil")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestExplorationReadAndCancelPaths(t *testing.T) {
	t.Parallel()

	runID := uuid.NewString()
	seen := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method+" "+r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/explorations/" + runID:
			_, _ = w.Write([]byte(`{
				"id":"run-1","app_id":"app-1","mode":"explore",
				"execution_status":"running","atlas_status":"processing",
				"customer_status":"processing",
				"created_at":"2026-08-11T20:00:00Z","updated_at":"2026-08-11T20:00:00Z"
			}`))
		case "/api/v1/explorations/" + runID + "/report":
			_, _ = w.Write([]byte(`{
				"run":{"id":"run-1","app_id":"app-1","mode":"explore",
				"execution_status":"running","atlas_status":"processing",
				"customer_status":"processing",
				"created_at":"2026-08-11T20:00:00Z","updated_at":"2026-08-11T20:00:00Z"},
				"total_children":3,"completed_children":1,"progress":0.333
			}`))
		case "/api/v1/explorations/" + runID + "/cancel":
			_, _ = w.Write([]byte(`{"run_id":"` + runID + `","execution_status":"cancelled","hatchet_cancelled":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClientWithBaseURL("token", server.URL)
	if _, err := client.GetExploration(context.Background(), runID); err != nil {
		t.Fatalf("GetExploration(): %v", err)
	}
	if _, err := client.GetExplorationReport(context.Background(), runID); err != nil {
		t.Fatalf("GetExplorationReport(): %v", err)
	}
	if _, err := client.CancelExploration(context.Background(), runID); err != nil {
		t.Fatalf("CancelExploration(): %v", err)
	}
	for _, key := range []string{
		"GET /api/v1/explorations/" + runID,
		"GET /api/v1/explorations/" + runID + "/report",
		"POST /api/v1/explorations/" + runID + "/cancel",
	} {
		if seen[key] != 1 {
			t.Fatalf("%s count = %d", key, seen[key])
		}
	}
}
