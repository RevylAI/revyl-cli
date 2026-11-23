package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

const localInferenceTestComputerID = "11111111-2222-4333-8444-555555555555"

func TestLocalInferenceStartsOnlyOnABackendThatAcceptsIt(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capabilities string
		wantError    string
	}{
		{name: "older backend", capabilities: `{"capabilities":["session_profiles"]}`, wantError: "--inference local needs a newer Revyl backend"},
		{name: "current backend", capabilities: `{"capabilities":["session_profiles","device_local_inference"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var started map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/backend-capabilities":
					_, _ = w.Write([]byte(tc.capabilities))
				case "/api/v1/execution/start_device":
					body, _ := io.ReadAll(r.Body)
					if err := json.Unmarshal(body, &started); err != nil {
						t.Errorf("start body: %v", err)
					}
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"detail":{"error":"computer_local_inference_unavailable","platform":"ios","message":"This computer has no vision model of its own"}}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())

			_, _, err := mgr.StartSession(context.Background(), StartSessionOptions{
				Platform:                   "ios",
				ComputerSessionID:          localInferenceTestComputerID,
				Inference:                  "local",
				DisableInheritedLaunchVars: true,
			})

			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || started != nil {
					t.Fatalf("err = %v, started = %v; want %q before any start", err, started, tc.wantError)
				}
				return
			}
			if started["inference"] != "local" || started["computer_session_id"] != localInferenceTestComputerID {
				t.Fatalf("start body = %v, want inference local inside the computer", started)
			}
			if err == nil || !strings.Contains(err.Error(), "no vision model") {
				t.Fatalf("err = %v, want the backend's refusal", err)
			}
		})
	}
}

func TestLocalInferenceIsRefusedOutsideAnIOSOrWebSessionInAComputer(t *testing.T) {
	for _, tc := range []struct {
		name, platform, computerID, inference string
	}{
		{name: "standalone device", platform: "ios", inference: "local"},
		{name: "standalone web", platform: "web", inference: "local"},
		{name: "android in a computer", platform: "android", computerID: localInferenceTestComputerID, inference: "local"},
		{name: "unknown engine", platform: "ios", computerID: localInferenceTestComputerID, inference: "remote"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateComputerSessionStart(StartSessionOptions{Platform: tc.platform, Inference: tc.inference}, tc.platform, tc.computerID)
			if err == nil || !strings.Contains(err.Error(), "--inference local runs only for iOS and Web sessions inside a computer") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	for _, platform := range []string{"ios", "web"} {
		if err := ValidateComputerSessionStart(StartSessionOptions{Platform: platform, Inference: "local"}, platform, localInferenceTestComputerID); err != nil {
			t.Fatalf("%s inside a computer refused: %v", platform, err)
		}
	}
}
