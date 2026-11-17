package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

func TestParseDevicePermissionAssignments(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    map[string]string
		wantErr string
	}{
		{"group names", []string{"location=allow", "motion=ALLOW"}, map[string]string{"location": "allow", "motion": "allow"}, ""},
		{"android permission", []string{"android.permission.CAMERA=deny"}, map[string]string{"android.permission.CAMERA": "deny"}, ""},
		{"reset", []string{"photos=unset"}, map[string]string{"photos": "unset"}, ""},
		{"none", nil, nil, "at least one permission"},
		{"missing value", []string{"location"}, nil, "must be name=value"},
		{"empty value", []string{"location="}, nil, "must be name=value"},
		{"unknown name", []string{"gps=allow"}, nil, "unknown permission \"gps\""},
		{"unknown value", []string{"location=always"}, nil, "use allow, deny, unset"},
		{"repeated", []string{"location=allow", "location=deny"}, nil, "more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDevicePermissionAssignments(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for name, value := range tc.want {
				if got[name] != value {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestValidateDevicePermissionsForPlatform(t *testing.T) {
	cases := []struct {
		name        string
		platform    string
		permissions map[string]string
		wantErr     string
	}{
		{"ios grantable", "ios", map[string]string{"location": "allow", "motion": "allow"}, ""},
		{"ios notifications", "ios", map[string]string{"location": "allow", "notifications": "allow"}, "cannot pre-grant notifications"},
		{"ios tracking", "ios", map[string]string{"userTracking": "allow"}, "App Tracking Transparency"},
		{"ios android name", "ios", map[string]string{"android.permission.CAMERA": "allow"}, "is an Android permission"},
		{"ios all", "ios", map[string]string{"all": "allow"}, ""},
		{"ios microphone allow", "ios", map[string]string{"microphone": "allow"}, ""},
		{"ios microphone deny", "ios", map[string]string{"microphone": "deny"}, "cannot set microphone=deny: Revyl's audio injection grants the iOS microphone on every app launch"},
		{"ios microphone unset", "ios", map[string]string{"microphone": "unset"}, "cannot set microphone=unset"},
		{"ios all deny", "ios", map[string]string{"all": "deny"}, "cannot set all=deny"},
		{"ios all unset", "ios", map[string]string{"all": "unset"}, "all includes microphone"},
		{"android microphone deny", "android", map[string]string{"microphone": "deny"}, ""},
		{"ios android-only group", "ios", map[string]string{"bluetooth": "allow"}, "iOS simulators cannot set bluetooth"},
		{"android notifications", "android", map[string]string{"notifications": "allow"}, ""},
		{"android qualified name", "android", map[string]string{"android.permission.CAMERA": "allow", "all": "deny"}, ""},
		{"android ios-only group", "android", map[string]string{"motion": "allow"}, "Android sessions cannot set motion"},
		{"web", "web", map[string]string{"location": "allow"}, "only on iOS and Android"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDevicePermissionsForPlatform(tc.platform, tc.permissions)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

type setPermissionsWorker struct {
	requests []map[string]any
	response string
}

func newSetPermissionsServer(t *testing.T, worker *setPermissionsWorker, platform string) *Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/device-proxy/wf-permissions-1/set_permissions" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode set_permissions request: %v", err)
		}
		worker.requests = append(worker.requests, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(worker.response))
	}))
	t.Cleanup(server.Close)
	now := time.Now()
	return &Server{sessionMgr: &DeviceSessionManager{
		apiClient: api.NewClientWithBaseURL("test-api-key", server.URL),
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "sess-permissions-1",
				WorkflowRunID: "wf-permissions-1",
				WorkerBaseURL: "https://worker.example",
				Platform:      platform,
				StartedAt:     now,
				LastActivity:  now,
				IdleTimeout:   5 * time.Minute,
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}}
}

func TestHandleDeviceSetPermissions_SendsPermissionsToWorker(t *testing.T) {
	worker := &setPermissionsWorker{response: `{"success":true,"action":"set_permissions","latency_ms":12}`}
	srv := newSetPermissionsServer(t, worker, "ios")

	_, output, err := srv.handleDeviceSetPermissions(context.Background(), nil, DeviceSetPermissionsInput{
		BundleID:    " com.example.app ",
		Permissions: map[string]string{"location": "allow", "motion": "Allow"},
	})

	if err != nil || !output.Success {
		t.Fatalf("output = %+v, err = %v", output, err)
	}
	if len(worker.requests) != 1 {
		t.Fatalf("worker received %d requests, want 1", len(worker.requests))
	}
	request := worker.requests[0]
	if request["bundle_id"] != "com.example.app" {
		t.Fatalf("bundle_id = %v", request["bundle_id"])
	}
	permissions, _ := request["permissions"].(map[string]any)
	if permissions["location"] != "allow" || permissions["motion"] != "allow" || len(permissions) != 2 {
		t.Fatalf("permissions = %v", request["permissions"])
	}
}

func TestHandleDeviceSetPermissions_ReportsWorkerFailure(t *testing.T) {
	worker := &setPermissionsWorker{response: `{"success":false,"action":"set_permissions","latency_ms":3,"error":"simctl privacy grant location failed for com.example.app"}`}
	srv := newSetPermissionsServer(t, worker, "ios")

	_, output, err := srv.handleDeviceSetPermissions(context.Background(), nil, DeviceSetPermissionsInput{
		BundleID:    "com.example.app",
		Permissions: map[string]string{"location": "allow"},
	})

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if output.Success || output.Error != "simctl privacy grant location failed for com.example.app" {
		t.Fatalf("output = %+v, want the worker's failure", output)
	}
}

func TestHandleDeviceSetPermissions_RefusesAlertOnlyIOSPermissionsBeforeTheWorker(t *testing.T) {
	worker := &setPermissionsWorker{response: `{"success":true,"action":"set_permissions","latency_ms":1}`}
	srv := newSetPermissionsServer(t, worker, "ios")

	_, output, err := srv.handleDeviceSetPermissions(context.Background(), nil, DeviceSetPermissionsInput{
		BundleID:    "com.example.app",
		Permissions: map[string]string{"notifications": "allow"},
	})

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if output.Success || !strings.Contains(output.Error, "cannot pre-grant notifications") {
		t.Fatalf("output = %+v, want a clear refusal", output)
	}
	if len(worker.requests) != 0 {
		t.Fatalf("worker received %d requests, want none", len(worker.requests))
	}
}

func TestHandleDeviceSetPermissions_RefusesIOSMicrophoneDenyBeforeTheWorker(t *testing.T) {
	worker := &setPermissionsWorker{response: `{"success":true,"action":"set_permissions","latency_ms":1}`}
	srv := newSetPermissionsServer(t, worker, "ios")

	_, output, err := srv.handleDeviceSetPermissions(context.Background(), nil, DeviceSetPermissionsInput{
		BundleID:    "com.example.app",
		Permissions: map[string]string{"location": "allow", "microphone": "deny"},
	})

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if output.Success || !strings.Contains(output.Error, "audio injection grants the iOS microphone on every app launch") {
		t.Fatalf("output = %+v, want the microphone refusal", output)
	}
	if len(worker.requests) != 0 {
		t.Fatalf("worker received %d requests, want none", len(worker.requests))
	}
}

func TestHandleDeviceSetPermissions_RequiresBundleID(t *testing.T) {
	worker := &setPermissionsWorker{response: `{"success":true,"action":"set_permissions","latency_ms":1}`}
	srv := newSetPermissionsServer(t, worker, "android")

	_, output, _ := srv.handleDeviceSetPermissions(context.Background(), nil, DeviceSetPermissionsInput{
		Permissions: map[string]string{"location": "allow"},
	})

	if output.Success || !strings.Contains(output.Error, "bundle ID is required") {
		t.Fatalf("output = %+v, want a missing bundle ID error", output)
	}
	if len(worker.requests) != 0 {
		t.Fatalf("worker received %d requests, want none", len(worker.requests))
	}
}
