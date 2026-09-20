package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCancelDeviceRequiresConfirmedSettlementAndRelease(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		wantPending bool
		wantError   bool
	}{
		{"released", `{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`, false, false},
		{"accepted", `{"success":true,"request_accepted":true,"session_settled":false,"device_released":false}`, true, true},
		{"settled_without_release", `{"success":true,"request_accepted":true,"session_settled":true,"device_released":false}`, true, true},
		{"legacy_acceptance", `{"success":true,"db_updated":true,"hatchet_cancelled":true}`, true, true},
		{"legacy_rejection", `{"success":false,"message":"not permitted"}`, false, true},
		{"rejected", `{"success":true,"request_accepted":false,"session_settled":false,"device_released":false}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/device/status/cancel/workflow-1" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			response, err := NewClientWithBaseURL("test-key", server.URL).CancelDevice(context.Background(), "workflow-1")
			if (err != nil) != tc.wantError {
				t.Fatalf("CancelDevice error = %v, want error %v", err, tc.wantError)
			}
			var pending *DeviceSessionStopPendingError
			if errors.As(err, &pending) != tc.wantPending {
				t.Fatalf("pending error = %v, want pending %v", err, tc.wantPending)
			}
			if response == nil {
				t.Fatal("missing parsed acknowledgement")
			}
		})
	}
}
