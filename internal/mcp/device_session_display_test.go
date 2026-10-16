package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

func TestSessionDisplayLabel(t *testing.T) {
	for _, test := range []struct {
		name, platform, appName, want string
	}{
		{name: "ios unchanged", platform: "ios", want: "ios"},
		{name: "android unchanged", platform: "android", want: "android"},
		{name: "unknown preserved", platform: "linux", want: "linux"},
		{name: "missing explicit", want: "Unknown"},
		{name: "web with app", platform: "web", appName: "Example App", want: "Web · Chrome · Example App"},
		{name: "web without app", platform: "web", want: "Web · Chrome"},
		{name: "web blank app", platform: "web", appName: "  ", want: "Web · Chrome"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := &DeviceSession{SessionID: "fixture-session", Platform: test.platform, AppName: test.appName}
			if got := SessionDisplayLabel(session); got != test.want {
				t.Fatalf("label = %q, want %q", got, test.want)
			}
			if session.Platform != test.platform || session.AppName != test.appName {
				t.Fatal("display mutated the session")
			}
		})
	}
}

func TestListDeviceSessionsDisplayPreservesJSON(t *testing.T) {
	for _, platform := range []string{"ios", "android", "web", "linux", ""} {
		t.Run(platform, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/entity/users/get_user_uuid":
					_, _ = w.Write([]byte(`{"org_id":"fixture-org","email":"fixture@example.test"}`))
				case "/api/v1/execution/device-sessions/active":
					_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{{"id": "fixture-session", "platform": platform, "app_name": "Example App"}}})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backend.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), "")
			manager.orgID = "fixture-org"
			manager.sessions[0] = &DeviceSession{Index: 0, SessionID: "fixture-session", Platform: platform, StartedAt: time.Now(), LastActivity: time.Now(), IdleTimeout: time.Minute}
			manager.activeIndex = 0
			srv := &Server{sessionMgr: manager}
			server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "display-test", Version: "test"}, nil)
			mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "list_device_sessions"}, srv.handleListDeviceSessions)
			serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			serverSession, err := server.Connect(ctx, serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()
			client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "display-test", Version: "test"}, nil)
			clientSession, err := client.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer clientSession.Close()
			result, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{Name: "list_device_sessions", Arguments: map[string]any{}})
			if err != nil || result.IsError {
				t.Fatalf("list result = %+v, %v", result, err)
			}
			var textJSON map[string]any
			if err := json.Unmarshal([]byte(result.Content[0].(*mcpsdk.TextContent).Text), &textJSON); err != nil {
				t.Fatalf("first text content stopped being JSON: %v", err)
			}
			if !reflect.DeepEqual(textJSON, result.StructuredContent) {
				t.Fatal("JSON text differs from structured output")
			}
			item := textJSON["sessions"].([]any)[0].(map[string]any)
			if item["platform"] != platform || len(item) != 8 || len(textJSON) != 3 {
				t.Fatalf("JSON contract changed: %+v", textJSON)
			}
			wantLabel := ""
			if platform == "web" {
				wantLabel = "Session 0: Web · Chrome · Example App"
			} else if platform == "" {
				wantLabel = "Session 0: Unknown"
			}
			if wantLabel == "" {
				if len(result.Content) != 1 {
					t.Fatal("existing phone or unknown-platform content changed")
				}
			} else if len(result.Content) != 2 || result.Content[1].(*mcpsdk.TextContent).Text != wantLabel {
				t.Fatalf("missing display label %q: %+v", wantLabel, result.Content)
			}
			if strings.Contains(result.Content[0].(*mcpsdk.TextContent).Text, "Example App") {
				t.Fatal("app display name leaked into JSON")
			}
		})
	}
}

func TestSyncSessionsAppNameIsRefreshedWithoutPersistence(t *testing.T) {
	var name *string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"org_id":"fixture-org","email":"fixture@example.test"}`))
		case "/api/v1/execution/device-sessions/active":
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{{"id": "fixture-session", "platform": "web", "app_name": name}}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	dir := t.TempDir()
	manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), dir)
	manager.sessions[0] = &DeviceSession{Index: 0, SessionID: "fixture-session", Platform: "web", StartedAt: time.Now(), LastActivity: time.Now()}
	manager.activeIndex = 0
	for _, appName := range []string{"Example App", "Renamed App", ""} {
		name = nil
		if appName != "" {
			name = &appName
		}
		if err := manager.SyncSessions(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := manager.GetSession(0).AppName; got != appName {
			t.Fatalf("synced app name = %q, want %q", got, appName)
		}
		data, err := os.ReadFile(filepath.Join(dir, ".revyl", "device-sessions.json"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "app_name") || (appName != "" && strings.Contains(string(data), appName)) {
			t.Fatal("display-only app name entered persisted session JSON")
		}
	}
}

func TestSyncSessionsDiscoversWebAppName(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"org_id":"fixture-org","email":"fixture@example.test"}`))
		case "/api/v1/execution/device-sessions/active":
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{{"id": startIDSessionID, "platform": "web", "app_name": "Example App"}}})
		case "/api/v1/execution/device-sessions/" + startIDSessionID:
			_ = json.NewEncoder(w).Encode(runtimeProjectedSessionDetail(startIDSessionID, "running"))
		case "/api/v1/execution/device-proxy/" + startIDWorkflowRunID + "/screenshot":
			w.Header().Set("X-Revyl-Screen-Token", "fixture-token")
			_, _ = w.Write(runtimeReadyFixturePNG(t))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
	if err := manager.SyncSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions := manager.ListSessions()
	if len(sessions) != 1 || SessionDisplayLabel(sessions[0]) != "Web · Chrome · Example App" {
		t.Fatalf("discovered sessions = %+v, want a named Web session", sessions)
	}
}
