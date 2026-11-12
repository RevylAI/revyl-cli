package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	mcppkg "github.com/revyl/cli/internal/mcp"
)

const (
	stopPendingBody  = `{"success":true,"request_accepted":true,"session_settled":false,"device_released":false}`
	stopSettledBody  = `{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`
	stopRejectedBody = `{"success":false,"request_accepted":false,"message":"stop rejected"}`
)

// useFakeDeviceStopClock replaces the stop settle clock with one that advances
// only when the stop sleeps, and returns the sleeps it was asked for.
func useFakeDeviceStopClock(t *testing.T) *[]time.Duration {
	t.Helper()
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	sleeps := &[]time.Duration{}
	previousNow, previousSleep := deviceStopNow, deviceStopSleep
	deviceStopNow = func() time.Time { return now }
	deviceStopSleep = func(_ context.Context, d time.Duration) error {
		*sleeps = append(*sleeps, d)
		now = now.Add(d)
		return nil
	}
	t.Cleanup(func() { deviceStopNow, deviceStopSleep = previousNow, previousSleep })
	return sleeps
}

func newSettleStopCommand(t *testing.T, all bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "stop"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().Bool("dev", false, "")
	cmd.Flags().Bool("all", all, "")
	registerSessionTargetFlags(cmd)
	return cmd
}

func TestDeviceStopAsksAgainUntilTheSessionSettles(t *testing.T) {
	for _, tc := range []struct {
		name        string
		responses   []string
		wantStopped bool
		wantError   bool
		wantAsks    int
		wantSleeps  []time.Duration
	}{
		{"settles on the third ask", []string{stopPendingBody, stopPendingBody, stopSettledBody}, true, false, 3, []time.Duration{2 * time.Second, 2 * time.Second}},
		{"settles at once", []string{stopSettledBody}, true, false, 1, nil},
		{"rejected is not asked again", []string{stopRejectedBody}, false, true, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, flowSessionID)
			sleeps := useFakeDeviceStopClock(t)
			asks := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/device/sessions/"+flowSessionID+"/stop" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.responses[min(asks, len(tc.responses)-1)]))
				asks++
			}))
			t.Cleanup(server.Close)
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			var stopErr error
			output := captureStdout(t, func() { stopErr = deviceStopCmd.RunE(newSettleStopCommand(t, false), nil) })

			if (stopErr != nil) != tc.wantError {
				t.Fatalf("stop error = %v, want error %v", stopErr, tc.wantError)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(output), &payload); err != nil {
				t.Fatalf("invalid JSON output %q: %v", output, err)
			}
			if payload["stopped"] != tc.wantStopped || asks != tc.wantAsks || len(*sleeps) != len(tc.wantSleeps) {
				t.Fatalf("stopped = %v after %d asks and sleeps %v; want %v after %d asks and sleeps %v", payload["stopped"], asks, *sleeps, tc.wantStopped, tc.wantAsks, tc.wantSleeps)
			}
			for i, want := range tc.wantSleeps {
				if (*sleeps)[i] != want {
					t.Fatalf("sleeps = %v, want %v", *sleeps, tc.wantSleeps)
				}
			}
		})
	}
}

func TestDeviceStopReportsPendingWhenTheSessionNeverSettlesWithinThirtySeconds(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, flowSessionID)
	sleeps := useFakeDeviceStopClock(t)
	asks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asks++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(stopPendingBody))
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	var stopErr error
	output := captureStdout(t, func() { stopErr = deviceStopCmd.RunE(newSettleStopCommand(t, false), nil) })

	if stopErr != nil {
		t.Fatalf("stop error = %v, want an accepted pending stop", stopErr)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("invalid JSON output %q: %v", output, err)
	}
	if payload["stopped"] != false || payload["stop_requested"] != true {
		t.Fatalf("payload = %v, want stopped false with the stop requested", payload)
	}
	var waited time.Duration
	for _, sleep := range *sleeps {
		waited += sleep
	}
	if waited != deviceStopSettleTimeout || asks != 16 {
		t.Fatalf("waited %v over %d asks, want %v over 16 asks", waited, asks, deviceStopSettleTimeout)
	}
}

func TestDeviceStopAllAsksAgainOnlyForPendingSessions(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	sleeps := useFakeDeviceStopClock(t)
	flow := newInventoryStopAllServer(t)
	var mu sync.Mutex
	asksByRun := map[string]int{}
	flow.CancelResponse = func(runID string) string {
		mu.Lock()
		defer mu.Unlock()
		asksByRun[runID]++
		if runID == secondFlowWorkflowRunID && asksByRun[runID] == 1 {
			return stopPendingBody
		}
		return stopSettledBody
	}
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	var stopErr error
	output := captureStdout(t, func() { stopErr = deviceStopCmd.RunE(newSettleStopCommand(t, true), nil) })

	if stopErr != nil {
		t.Fatalf("stop error = %v", stopErr)
	}
	var payload struct {
		StoppedAll bool                             `json:"stopped_all"`
		Results    []mcppkg.DeviceSessionStopResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("invalid JSON output %q: %v", output, err)
	}
	if !payload.StoppedAll || len(payload.Results) != 2 {
		t.Fatalf("payload = %+v, want both sessions reported and stopped", payload)
	}
	for _, result := range payload.Results {
		if !result.SessionSettled || !result.DeviceReleased {
			t.Fatalf("result = %+v, want its latest settled outcome", result)
		}
	}
	if asksByRun[flowWorkflowRunID] != 1 || asksByRun[secondFlowWorkflowRunID] != 2 || len(*sleeps) != 1 {
		t.Fatalf("asks = %v and sleeps = %v, want only the pending session asked again after one sleep", asksByRun, *sleeps)
	}
}

func TestDevStopReportsStoppedFalseWhenItsSessionNeverSettles(t *testing.T) {
	liveStartedAt := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	seedSessionCache(t, dir, liveStartedAt, nil)
	if err := saveDevContext(dir, &DevContext{Name: "android-main", Platform: "android", SessionID: secondFlowSessionID, SessionIndex: 1, SessionOwned: true, State: devContextStateRunning}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	sleeps := useFakeDeviceStopClock(t)
	var asks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com"}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[{"id":"` + secondFlowSessionID + `","org_id":"org-1","platform":"android","source":"cli","status":"running","workflow_run_id":"` + secondFlowWorkflowRunID + `","user_email":"test@example.com","created_at":"` + liveStartedAt.Format(time.RFC3339) + `","started_at":"` + liveStartedAt.Format(time.RFC3339) + `"}]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/execution/device/status/cancel/"):
			asks.Add(1)
			_, _ = w.Write([]byte(stopPendingBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	previousAll := devStopAll
	devStopAll = false
	t.Cleanup(func() { devStopAll = previousAll })
	cmd := &cobra.Command{Use: "stop"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().String("context", "", "")

	var stopErr error
	output := captureStdout(t, func() { stopErr = runDevStop(cmd, []string{"android-main"}) })

	if stopErr != nil {
		t.Fatalf("dev stop error = %v", stopErr)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("invalid JSON output %q: %v", output, err)
	}
	if payload["stopped"] != false || payload["session_id"] != secondFlowSessionID {
		t.Fatalf("payload = %v, want stopped false naming the unsettled session", payload)
	}
	if asks.Load() != 16 || len(*sleeps) != 15 {
		t.Fatalf("asks = %d and sleeps = %d, want the stop asked 16 times within the 30 s bound", asks.Load(), len(*sleeps))
	}
}
