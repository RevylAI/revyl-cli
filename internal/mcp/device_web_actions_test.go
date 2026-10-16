package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

func TestParseDeviceKey(t *testing.T) {
	for _, tc := range []struct {
		raw, key  string
		modifiers []string
	}{
		{"enter", "Enter", nil}, {"RETURN", "Enter", nil}, {"backspace", "Backspace", nil},
		{"tab", "Tab", nil}, {"esc", "Escape", nil}, {"escape", "Escape", nil}, {"delete", "Delete", nil},
		{"Space", "Space", nil}, {"ArrowUp", "ArrowUp", nil}, {"ArrowDown", "ArrowDown", nil},
		{"ArrowLeft", "ArrowLeft", nil}, {"ArrowRight", "ArrowRight", nil}, {"Home", "Home", nil},
		{"End", "End", nil}, {"PageUp", "PageUp", nil}, {"PageDown", "PageDown", nil},
		{"Meta+K", "k", []string{"meta"}}, {"CTRL+9", "9", []string{"ctrl"}},
		{"Ctrl + Shift + Tab", "Tab", []string{"ctrl", "shift"}},
		{"ctrl+ALT+Meta+Shift+Return", "Enter", []string{"ctrl", "alt", "meta", "shift"}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParseDeviceKey(tc.raw, "web")
			if err != nil || got.Key != tc.key || !reflect.DeepEqual(got.Modifiers, tc.modifiers) {
				t.Fatalf("ParseDeviceKey() = %+v, %v; want key=%s modifiers=%v", got, err, tc.key, tc.modifiers)
			}
		})
	}
	for _, raw := range []string{"", " ", "a", "3", "F1", "Control+K", "Meta+!", "Meta+é", "Meta+ſ", "ſpace", "Ctrl+ſpace", "Ctrl+Ctrl+K", "Meta++K", "+Tab", "Meta+", "Ctrl+Alt+Meta+Shift+Ctrl+K", "private-invalid-key", strings.Repeat("k", 65)} {
		t.Run("invalid/"+raw, func(t *testing.T) {
			got, err := ParseDeviceKey(raw, "web")
			if err == nil || got != nil {
				t.Fatalf("invalid key accepted: %+v, %v", got, err)
			}
			if strings.Contains(err.Error(), "private-invalid-key") {
				t.Fatalf("validation echoed input: %v", err)
			}
		})
	}
}

func TestPhoneKeyWireCompatibility(t *testing.T) {
	for _, platform := range []string{"ios", "android"} {
		for _, tc := range []struct{ raw, key string }{{"enter", "ENTER"}, {" RETURN ", "ENTER"}, {"Backspace", "BACKSPACE"}, {"DELETE", "BACKSPACE"}} {
			t.Run(platform+"/"+tc.raw, func(t *testing.T) {
				calls := 0
				manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if !strings.HasSuffix(r.URL.Path, "/key") {
						t.Errorf("unexpected path %s", r.URL.Path)
					}
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"key":"`+tc.key+`"}` {
						t.Errorf("phone body changed: %s", body)
					}
					_, _ = w.Write([]byte(`{"Success":true,"action":"legacy-key"}`))
				})
				session.Platform = platform
				server := &Server{sessionMgr: manager}
				_, output, err := server.handleDeviceKey(context.Background(), nil, DeviceKeyInput{Key: tc.raw})
				if err != nil || !output.Success || output.Key != tc.key || calls != 1 {
					t.Fatalf("phone key = %+v, %v, calls=%d", output, err, calls)
				}
			})
		}
	}
}

func TestWebActionRequestMapping(t *testing.T) {
	zero, maxX, maxY := 0, 11520, 6480
	for _, tc := range []struct {
		name, action, want string
		invoke             func(*DeviceSessionManager, *DeviceSession) ([]byte, error)
	}{
		{"key", "key", `{"key":"k","modifiers":["meta"]}`, func(m *DeviceSessionManager, s *DeviceSession) ([]byte, error) {
			return m.PressKeyOnSession(context.Background(), s, "Meta+K")
		}},
		{"delete", "key", `{"key":"Delete"}`, func(m *DeviceSessionManager, s *DeviceSession) ([]byte, error) {
			return m.PressKeyOnSession(context.Background(), s, "Delete")
		}},
		{"down viewport", "scroll", `{"dx":0,"dy":600}`, func(m *DeviceSessionManager, s *DeviceSession) ([]byte, error) {
			return m.ScrollOnSession(context.Background(), s, "down", 600, "", nil, nil, "")
		}},
		{"up point", "scroll", `{"x":0,"y":0,"dx":0,"dy":-16384}`, func(m *DeviceSessionManager, s *DeviceSession) ([]byte, error) {
			return m.ScrollOnSession(context.Background(), s, "up", 16384, "", &zero, &zero, "")
		}},
		{"left viewport", "scroll", `{"dx":-1,"dy":0}`, func(m *DeviceSessionManager, s *DeviceSession) ([]byte, error) {
			return m.ScrollOnSession(context.Background(), s, "left", 1, "", nil, nil, "")
		}},
		{"right point", "scroll", `{"x":11520,"y":6480,"dx":12,"dy":0}`, func(m *DeviceSessionManager, s *DeviceSession) ([]byte, error) {
			return m.ScrollOnSession(context.Background(), s, "right", 12, "", &maxX, &maxY, "")
		}},
		{"select", "select", `{"x":0,"y":0,"option_label":"日本語"}`, func(m *DeviceSessionManager, s *DeviceSession) ([]byte, error) {
			return m.SelectOnSession(context.Background(), s, "日本語", "", &zero, &zero, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !strings.HasSuffix(r.URL.Path, "/"+tc.action) {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				got, _ := io.ReadAll(r.Body)
				if string(got) != tc.want {
					t.Errorf("body=%s, want %s", got, tc.want)
				}
				_, _ = w.Write([]byte(`{"success":true,"action":"` + tc.action + `"}`))
			})
			if _, err := tc.invoke(manager, session); err != nil || calls != 1 {
				t.Fatalf("action error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestWebActionsRefuseInvalidInputBeforeRequests(t *testing.T) {
	zero, negative, tooWide, tooTall := 0, -1, 11521, 6481
	for _, action := range []string{"scroll", "select"} {
		for _, tc := range []struct {
			name, platform, target, label, direction string
			amount                                   int
			x, y                                     *int
		}{
			{name: "ios", platform: "ios", target: "private-target", label: "Valid", direction: "down", amount: 1},
			{name: "android", platform: "android", target: "private-target", label: "Valid", direction: "down", amount: 1},
			{name: "only x", platform: "web", label: "Valid", direction: "down", amount: 1, x: &zero},
			{name: "only y", platform: "web", label: "Valid", direction: "down", amount: 1, y: &zero},
			{name: "both modes", platform: "web", target: "private-target", label: "Valid", direction: "down", amount: 1, x: &zero, y: &zero},
			{name: "negative", platform: "web", label: "Valid", direction: "down", amount: 1, x: &negative, y: &zero},
			{name: "too wide", platform: "web", label: "Valid", direction: "down", amount: 1, x: &tooWide, y: &zero},
			{name: "too tall", platform: "web", label: "Valid", direction: "down", amount: 1, x: &zero, y: &tooTall},
			{name: "whitespace target", platform: "web", target: " ", label: "Valid", direction: "down", amount: 1},
			{name: "too long target", platform: "web", target: strings.Repeat("x", 1001), label: "Valid", direction: "down", amount: 1},
			{name: "invalid target unicode", platform: "web", target: "\xff", label: "Valid", direction: "down", amount: 1},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				calls := 0
				manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("invalid action made a request") })
				session.Platform = tc.platform
				var err error
				if action == "scroll" {
					_, err = manager.ScrollOnSession(context.Background(), session, tc.direction, tc.amount, tc.target, tc.x, tc.y, "")
				} else {
					_, err = manager.SelectOnSession(context.Background(), session, tc.label, tc.target, tc.x, tc.y, "")
				}
				if err == nil || calls != 0 || strings.Contains(err.Error(), "private-target") {
					t.Fatalf("invalid input error=%v calls=%d", err, calls)
				}
				if tc.platform != "web" && !strings.Contains(err.Error(), action+" is not supported on this platform; use a Web session") {
					t.Fatalf("phone refusal=%v", err)
				}
			})
		}
	}
	for _, platform := range []string{"ios", "android", "web"} {
		manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid key made a request") })
		session.Platform = platform
		for _, raw := range []string{"private-invalid-key", "Meta+private-invalid-key"} {
			if _, err := manager.PressKeyOnSession(context.Background(), session, raw); err == nil || strings.Contains(err.Error(), raw) {
				t.Fatalf("invalid key error=%v", err)
			}
		}
	}
	manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid action made a request") })
	for _, amount := range []int{-1, 0, 16385} {
		if _, err := manager.ScrollOnSession(context.Background(), session, "down", amount, "", nil, nil, ""); err == nil {
			t.Errorf("accepted amount %d", amount)
		}
	}
	if _, err := manager.ScrollOnSession(context.Background(), session, "private-invalid-direction", 1, "", nil, nil, ""); err == nil || strings.Contains(err.Error(), "private-invalid-direction") {
		t.Errorf("invalid direction error=%v", err)
	}
	for _, label := range []string{"", strings.Repeat("界", 257), "\xff"} {
		if _, err := manager.SelectOnSession(context.Background(), session, label, "", &zero, &zero, ""); err == nil {
			t.Error("accepted invalid label")
		}
	}
	if _, err := manager.SelectOnSession(context.Background(), session, "Valid", "", nil, nil, ""); err == nil {
		t.Error("accepted select without target or point")
	}
}

func TestWebActionsGroundingKeepsSessionAndScreenToken(t *testing.T) {
	for _, action := range []string{"scroll", "select"} {
		for _, anchored := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/fresh", true: "/anchored"}[anchored], func(t *testing.T) {
				calls := 0
				var manager *DeviceSessionManager
				manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if !strings.Contains(r.URL.Path, "fixture-workflow") {
						t.Errorf("action switched session: %s", r.URL.Path)
					}
					if strings.HasSuffix(r.URL.Path, "/resolve_target") {
						var request api.WorkerResolveTargetRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Target != "fixture target" || request.SessionID != "fixture-session" {
							t.Errorf("unexpected grounding request: %+v, %v", request, err)
						}
						wantToken := ""
						if anchored {
							wantToken = "anchored-runtime-token"
						}
						if request.ScreenToken != wantToken {
							t.Errorf("grounding token=%s, want %s", request.ScreenToken, wantToken)
						}
						manager.mu.Lock()
						manager.activeIndex = 1
						manager.mu.Unlock()
						_, _ = w.Write([]byte(`{"found":true,"x":12,"y":34,"screen_token":"fresh-runtime-token"}`))
						return
					}
					var request map[string]any
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["x"] != float64(12) || request["y"] != float64(34) || request["screen_token"] != "fresh-runtime-token" {
						t.Errorf("action lost resolved point/token: %+v, %v", request, err)
					}
					if _, ok := request["target"]; ok {
						t.Error("action resent target instead of resolved point")
					}
					_, _ = w.Write([]byte(`{"success":true,"action":"` + action + `"}`))
				})
				manager.sessions[1] = &DeviceSession{Index: 1, SessionID: "another-session", WorkflowRunID: "another-workflow", Platform: "web"}
				token := ""
				if anchored {
					token = "screen-0-1"
					manager.screenAnchors[0] = &screenAnchorState{Token: token, RuntimeScreenToken: "anchored-runtime-token", SessionID: session.SessionID}
				}
				var err error
				if action == "scroll" {
					_, err = manager.ScrollOnSession(context.Background(), session, "down", 600, "fixture target", nil, nil, token)
				} else {
					_, err = manager.SelectOnSession(context.Background(), session, strings.Repeat("界", 256), "fixture target", nil, nil, token)
				}
				if err != nil || calls != 2 {
					t.Fatalf("grounded action error=%v calls=%d", err, calls)
				}
			})
		}
	}
}

func TestWebActionsNeverRetryFailures(t *testing.T) {
	zero := 0
	for _, action := range []string{"key", "scroll", "select"} {
		for _, tc := range []struct {
			name, response string
			status         int
		}{
			{"unavailable", `{"error":"private-payload"}`, 503},
			{"invalid", `{"error":"private-payload"}`, 400},
			{"false success", `{"success":false,"error":"private-payload"}`, 200},
			{"missing success", `{"action":"` + action + `"}`, 200},
			{"wrong action", `{"success":true,"action":"private-payload"}`, 200},
			{"malformed", `private-payload`, 200},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				calls := 0
				manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.response))
				})
				var err error
				switch action {
				case "key":
					_, err = manager.PressKeyOnSession(context.Background(), session, "Enter")
				case "scroll":
					_, err = manager.ScrollOnSession(context.Background(), session, "down", 1, "", nil, nil, "")
				case "select":
					_, err = manager.SelectOnSession(context.Background(), session, "Valid", "", &zero, &zero, "")
				}
				if err == nil || calls != 1 || strings.Contains(err.Error(), "private-payload") {
					t.Fatalf("failure error=%v calls=%d", err, calls)
				}
				if tc.name == "missing success" || tc.name == "wrong action" || tc.name == "malformed" {
					var workerErr *WorkerHTTPError
					if !errors.As(err, &workerErr) || !workerErr.ExecutionUncertain {
						t.Errorf("protocol failure lost uncertainty: %v", err)
					}
				}
			})
		}
	}
}

func TestWebActionGroundingFailureDoesNotDispatch(t *testing.T) {
	for _, action := range []string{"scroll", "select"} {
		calls := 0
		manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if !strings.HasSuffix(r.URL.Path, "/resolve_target") {
				t.Error("dispatched action after failed grounding")
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"grounding_failed"}`))
		})
		var err error
		if action == "scroll" {
			_, err = manager.ScrollOnSession(context.Background(), session, "down", 1, "fixture target", nil, nil, "")
		} else {
			_, err = manager.SelectOnSession(context.Background(), session, "Valid", "fixture target", nil, nil, "")
		}
		if err == nil || calls != 1 {
			t.Fatalf("grounding failure error=%v calls=%d", err, calls)
		}
	}
}

func TestWebActionRawPointsUseScreenshotAnchor(t *testing.T) {
	zero := 0
	for _, action := range []string{"scroll", "select"} {
		calls := 0
		manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["screen_token"] != "fixture-runtime-token" {
				t.Errorf("raw point lost screenshot anchor: %+v, %v", body, err)
			}
			_, _ = w.Write([]byte(`{"success":true,"action":"` + action + `"}`))
		})
		manager.screenAnchors[0] = &screenAnchorState{Token: "screen-0-1", RuntimeScreenToken: "fixture-runtime-token", SessionID: session.SessionID}
		var err error
		if action == "scroll" {
			_, err = manager.ScrollOnSession(context.Background(), session, "down", 1, "", &zero, &zero, "screen-0-1")
		} else {
			_, err = manager.SelectOnSession(context.Background(), session, "Valid", "", &zero, &zero, "screen-0-1")
		}
		if err != nil || calls != 1 {
			t.Fatalf("raw point error=%v calls=%d", err, calls)
		}
	}
}

func TestWebActionMCPPhoneRefusal(t *testing.T) {
	for _, platform := range []string{"ios", "android"} {
		manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("phone refusal sent an action or grounding request")
		})
		session.Platform = platform
		server := &Server{sessionMgr: manager}
		_, scroll, err := server.handleDeviceScroll(context.Background(), nil, DeviceScrollInput{Direction: "down", Target: "fixture target"})
		if err != nil || scroll.Success || !strings.Contains(scroll.Error, "scroll is not supported on this platform") {
			t.Fatalf("phone scroll=%+v, %v", scroll, err)
		}
		_, selected, err := server.handleDeviceSelect(context.Background(), nil, DeviceSelectInput{OptionLabel: "Valid", Target: "fixture target"})
		if err != nil || selected.Success || !strings.Contains(selected.Error, "select is not supported on this platform") {
			t.Fatalf("phone select=%+v, %v", selected, err)
		}
	}
}

func TestWebActionsRequireSession(t *testing.T) {
	manager, _ := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) { t.Error("nil session sent a request") })
	if _, err := manager.PressKeyOnSession(context.Background(), nil, "Enter"); err == nil {
		t.Error("key accepted nil session")
	}
	if _, err := manager.ScrollOnSession(context.Background(), nil, "down", 1, "", nil, nil, ""); err == nil {
		t.Error("scroll accepted nil session")
	}
	if _, err := manager.SelectOnSession(context.Background(), nil, "Valid", "fixture target", nil, nil, ""); err == nil {
		t.Error("select accepted nil session")
	}
}

func TestWebViewportScrollPreservesScreenshotAnchor(t *testing.T) {
	calls := 0
	manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body api.DeviceScrollRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.X != nil || body.Y != nil || body.ScreenToken != "fixture-runtime-token" || body.DY != 600 {
			t.Errorf("viewport scroll lost screenshot anchor: %+v", body)
		}
		_, _ = w.Write([]byte(`{"success":true,"action":"scroll"}`))
	})
	manager.screenAnchors[session.Index] = &screenAnchorState{
		Token: "screen-0-1", RuntimeScreenToken: "fixture-runtime-token", SessionID: session.SessionID,
	}
	server := &Server{sessionMgr: manager}
	_, result, err := server.handleDeviceScroll(context.Background(), nil, DeviceScrollInput{
		Direction: "down", ScreenToken: "screen-0-1", SessionID: session.SessionID,
	})
	if err != nil || !result.Success || calls != 1 {
		t.Fatalf("anchored viewport scroll = %+v, %v, calls=%d", result, err, calls)
	}
}

func TestWebActionMCPHandlers(t *testing.T) {
	zero := 0
	manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		action := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if action == "scroll" && request["dy"] != float64(600) {
			t.Errorf("default scroll=%+v", request)
		}
		_, _ = w.Write([]byte(`{"success":true,"action":"` + action + `"}`))
	})
	server := &Server{sessionMgr: manager}
	_, key, err := server.handleDeviceKey(context.Background(), nil, DeviceKeyInput{Key: "Meta+K", SessionID: session.SessionID})
	if err != nil || !key.Success || key.Key != "meta+k" {
		t.Fatalf("key handler=%+v, %v", key, err)
	}
	_, scroll, err := server.handleDeviceScroll(context.Background(), nil, DeviceScrollInput{Direction: "down", SessionID: session.SessionID})
	if err != nil || !scroll.Success {
		t.Fatalf("scroll handler=%+v, %v", scroll, err)
	}
	_, selected, err := server.handleDeviceSelect(context.Background(), nil, DeviceSelectInput{OptionLabel: "Valid", X: &zero, Y: &zero, SessionID: session.SessionID})
	if err != nil || !selected.Success {
		t.Fatalf("select handler=%+v, %v", selected, err)
	}
	_, scroll, err = server.handleDeviceScroll(context.Background(), nil, DeviceScrollInput{Direction: "down", Amount: &zero})
	if err != nil || scroll.Success {
		t.Fatalf("zero scroll handler=%+v, %v", scroll, err)
	}
}

func TestWebActionToolsRegisteredWithBoundedSchemas(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	for _, profile := range []Profile{ProfileCore, ProfileFull} {
		t.Run(string(profile), func(t *testing.T) {
			server, err := NewServer("test", false, WithProfile(profile))
			if err != nil {
				t.Fatal(err)
			}
			tools := listServerTools(t, server)
			for _, action := range []string{"key", "scroll", "select"} {
				tool := serverToolByName(t, tools, "device_"+action)
				data, err := json.Marshal(tool.InputSchema)
				if err != nil {
					t.Fatal(err)
				}
				var schema struct {
					Properties map[string]map[string]any `json:"properties"`
					Required   []string                  `json:"required"`
					OneOf      []any                     `json:"oneOf"`
				}
				if err := json.Unmarshal(data, &schema); err != nil {
					t.Fatal(err)
				}
				if schema.Properties["session_id"] == nil || schema.Properties["session_index"] == nil {
					t.Error("missing session selectors")
				}
				if action == "key" {
					if schema.Properties["key"]["maxLength"] != float64(64) || !reflect.DeepEqual(schema.Required, []string{"key"}) {
						t.Errorf("key schema=%s", data)
					}
					continue
				}
				if schema.Properties["x"]["type"] != "integer" || schema.Properties["x"]["maximum"] != float64(11520) || schema.Properties["y"]["maximum"] != float64(6480) {
					t.Errorf("coordinate schema=%s", data)
				}
				if action == "scroll" {
					if schema.Properties["amount"]["minimum"] != float64(1) || schema.Properties["amount"]["maximum"] != float64(16384) || schema.Properties["amount"]["default"] != float64(600) || len(schema.OneOf) != 3 {
						t.Errorf("scroll schema=%s", data)
					}
					if !reflect.DeepEqual(schema.Properties["direction"]["enum"], []any{"up", "down", "left", "right"}) {
						t.Error("missing direction enum")
					}
				} else if schema.Properties["option_label"]["minLength"] != float64(1) || schema.Properties["option_label"]["maxLength"] != float64(256) || len(schema.OneOf) != 2 {
					t.Errorf("select schema=%s", data)
				}
			}
		})
	}
}

func TestWebActionMCPSchemaRejectsInvalidArguments(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	server, err := NewServer("test", false, WithProfile(ProfileFull))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "schema-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	for _, tc := range []struct{ name, arguments string }{
		{"scroll", `{"direction":"down","amount":0}`},
		{"scroll", `{"direction":"diagonal"}`},
		{"scroll", `{"direction":"down","amount":1.5}`},
		{"scroll", `{"direction":"down","x":0}`},
		{"scroll", `{"direction":"down","x":0,"y":0,"target":"fixture target"}`},
		{"select", `{"option_label":"Valid"}`},
		{"select", `{"option_label":"","x":0,"y":0}`},
		{"select", `{"option_label":"Valid","x":11521,"y":0}`},
		{"key", `{"key":1}`},
	} {
		result, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{Name: "device_" + tc.name, Arguments: json.RawMessage(tc.arguments)})
		if err == nil && !result.IsError {
			t.Errorf("schema accepted %s %s", tc.name, tc.arguments)
		}
	}
}
