package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

const computerToolSessionID = "11111111-1111-4111-8111-111111111111"

func computerToolScreenshotFixture(t *testing.T) api.ComputerScreenshot {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	return api.ComputerScreenshot{
		Width: 3, Height: 2,
		ObservedAt: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
		PNG:        base64.StdEncoding.EncodeToString(encoded.Bytes()),
		Cursor:     &api.ComputerScreenPoint{X: 1, Y: 1},
	}
}

func newComputerToolTestServer(t *testing.T, handler http.HandlerFunc) *Server {
	t.Helper()
	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)
	server := &Server{
		apiClient:            api.NewClientWithBaseURL("fixture-key", backend.URL),
		mcpServer:            mcpsdk.NewServer(&mcpsdk.Implementation{Name: "computer-test", Version: "test"}, nil),
		experimentalComputer: true,
	}
	server.registerComputerTool()
	return server
}

func TestComputerToolMapsEveryAction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arguments map[string]any
		payload   string
		region    string
	}{
		{name: "screenshot"},
		{name: "zoom", arguments: map[string]any{"region": []int{0, 0, 2, 1}}, region: "0,0,2,1"},
		{name: "wait", arguments: map[string]any{"duration": 0.001}},
		{name: "cursor_position"},
		{name: "left_click", arguments: map[string]any{"coordinate": []int{0, 0}}, payload: `{"action":"click","x":0,"y":0,"button":"left","count":1}`},
		{name: "right_click", arguments: map[string]any{"coordinate": []int{1, 1}, "text": "Control+Shift"}, payload: `{"action":"click","x":1,"y":1,"button":"right","count":1,"modifiers":["ctrl","shift"]}`},
		{name: "middle_click", arguments: map[string]any{"coordinate": []int{1, 1}}, payload: `{"action":"click","x":1,"y":1,"button":"middle","count":1}`},
		{name: "double_click", arguments: map[string]any{"coordinate": []int{1, 1}, "text": "command"}, payload: `{"action":"click","x":1,"y":1,"button":"left","count":2,"modifiers":["cmd"]}`},
		{name: "triple_click", arguments: map[string]any{"coordinate": []int{1, 1}}, payload: `{"action":"click","x":1,"y":1,"button":"left","count":3}`},
		{name: "mouse_move", arguments: map[string]any{"coordinate": []int{0, 1}}, payload: `{"action":"move","x":0,"y":1}`},
		{name: "left_click_drag", arguments: map[string]any{"coordinate": []int{2, 1}, "start_coordinate": []int{0, 0}}, payload: `{"action":"drag","start_x":0,"start_y":0,"end_x":2,"end_y":1}`},
		{name: "left_mouse_down", payload: `{"action":"mouse_down","button":"left"}`},
		{name: "left_mouse_up", arguments: map[string]any{"coordinate": []int{0, 1}}, payload: `{"action":"mouse_up","x":0,"y":1,"button":"left"}`},
		{name: "scroll", arguments: map[string]any{"coordinate": []int{1, 1}, "scroll_direction": "down", "scroll_amount": 20, "text": "super,shift"}, payload: `{"action":"scroll","x":1,"y":1,"direction":"down","amount":20,"modifiers":["cmd","shift"]}`},
		{name: "type", arguments: map[string]any{"text": "A\n\t!"}, payload: `{"action":"type","text":"A\n\t!"}`},
		{name: "key", arguments: map[string]any{"text": "cmd+space Return"}, payload: `{"action":"key","keys":"cmd+space Return"}`},
		{name: "hold_key", arguments: map[string]any{"text": "shift+Right", "duration": 5}, payload: `{"action":"key","keys":"shift+Right","hold_seconds":5}`},
		{name: "hold_key", arguments: map[string]any{"text": "ctrl+Right", "duration": 30}, payload: `{"action":"key","keys":"ctrl+Right","hold_seconds":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := computerToolScreenshotFixture(t)
			var calls atomic.Int32
			server := newComputerToolTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Errorf("authorization did not use the shared API client")
				}
				path := "/api/v1/computer-runs/" + computerToolSessionID + "/screen/"
				if tc.payload == "" {
					queryCount := 1
					if tc.region != "" {
						queryCount++
					}
					if r.Method != http.MethodGet || r.URL.Path != path+"screenshot" || r.URL.Query().Get("via") != "mcp" || r.URL.Query().Get("region") != tc.region || len(r.URL.Query()) != queryCount {
						t.Errorf("unexpected screenshot request: %s %s", r.Method, r.URL)
					}
					_ = json.NewEncoder(w).Encode(fixture)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != path+"actions" || r.URL.RawQuery != "" {
					t.Errorf("unexpected action request: %s %s", r.Method, r.URL)
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				var wantAction map[string]any
				if err := json.Unmarshal([]byte(tc.payload), &wantAction); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"action": wantAction, "screenshot": true, "via": "mcp"}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("request = %#v, want %#v", got, want)
				}
				_ = json.NewEncoder(w).Encode(api.ComputerScreenActionResult{Screenshot: &fixture, Cursor: fixture.Cursor})
			})
			arguments := map[string]any{"action": tc.name, "computer_session_id": computerToolSessionID}
			for key, value := range tc.arguments {
				arguments[key] = value
			}
			result := callServerTool(t, server, "computer", arguments)
			if result.IsError {
				t.Fatalf("computer action failed: %+v", result)
			}
			text := requireHybridTextAndImage(t, result)
			output := decodeStructuredToolResult[ComputerToolOutput](t, result)
			if output.Width != fixture.Width || output.Height != fixture.Height || !reflect.DeepEqual(output.Cursor, fixture.Cursor) || output.Error != "" {
				t.Fatalf("output = %+v", output)
			}
			imageContent := result.Content[1].(*mcpsdk.ImageContent)
			wantPNG, err := fixture.DecodePNG()
			if err != nil || !bytes.Equal(imageContent.Data, wantPNG) {
				t.Fatal("native image did not match the backend PNG")
			}
			if strings.Contains(text, fixture.PNG) || strings.Contains(text, `"png"`) || strings.Contains(text, `"keys"`) || strings.Contains(text, `"text"`) || strings.Contains(text, `"observed_at"`) || strings.Contains(text, `"success"`) || strings.Contains(text, `"screenshot"`) || strings.Contains(text, `"action"`) {
				t.Fatalf("output duplicated image or input content: %s", text)
			}
			if calls.Load() != 1 {
				t.Fatalf("backend calls = %d, want exactly one", calls.Load())
			}
		})
	}
}

func TestComputerToolRejectsInvalidInputsWithoutRequests(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arguments map[string]any
	}{
		{"missing session", map[string]any{"action": "screenshot"}},
		{"empty session", map[string]any{"computer_session_id": "", "action": "screenshot"}},
		{"invalid session", map[string]any{"computer_session_id": "not-a-run", "action": "screenshot"}},
		{"zero session", map[string]any{"computer_session_id": "00000000-0000-0000-0000-000000000000", "action": "screenshot"}},
		{"missing action", map[string]any{}},
		{"unsupported action", map[string]any{"action": "exec"}},
		{"missing point", map[string]any{"action": "left_click"}},
		{"empty point", map[string]any{"action": "left_click", "coordinate": []int{}}},
		{"short point", map[string]any{"action": "left_click", "coordinate": []int{1}}},
		{"long point", map[string]any{"action": "left_click", "coordinate": []int{1, 2, 3}}},
		{"negative point", map[string]any{"action": "left_click", "coordinate": []int{-1, 0}}},
		{"oversized point", map[string]any{"action": "left_click", "coordinate": []int{4096, 0}}},
		{"fractional point", map[string]any{"action": "left_click", "coordinate": []float64{0.5, 0}}},
		{"string point", map[string]any{"action": "left_click", "coordinate": []string{"0", "0"}}},
		{"missing drag start", map[string]any{"action": "left_click_drag", "coordinate": []int{0, 0}}},
		{"short drag start", map[string]any{"action": "left_click_drag", "coordinate": []int{0, 0}, "start_coordinate": []int{0}}},
		{"missing zoom region", map[string]any{"action": "zoom"}},
		{"short zoom region", map[string]any{"action": "zoom", "region": []int{0, 0, 2}}},
		{"zero zoom region", map[string]any{"action": "zoom", "region": []int{0, 0, 0, 2}}},
		{"reversed zoom region", map[string]any{"action": "zoom", "region": []int{2, 2, 0, 0}}},
		{"oversized zoom region", map[string]any{"action": "zoom", "region": []int{0, 0, 5000, 2}}},
		{"missing scroll amount", map[string]any{"action": "scroll", "coordinate": []int{0, 0}, "scroll_direction": "down"}},
		{"zero scroll amount", map[string]any{"action": "scroll", "coordinate": []int{0, 0}, "scroll_direction": "down", "scroll_amount": 0}},
		{"large scroll amount", map[string]any{"action": "scroll", "coordinate": []int{0, 0}, "scroll_direction": "down", "scroll_amount": 21}},
		{"missing scroll direction", map[string]any{"action": "scroll", "coordinate": []int{0, 0}, "scroll_amount": 1}},
		{"invalid scroll direction", map[string]any{"action": "scroll", "coordinate": []int{0, 0}, "scroll_amount": 1, "scroll_direction": "diagonal"}},
		{"empty type", map[string]any{"action": "type", "text": ""}},
		{"oversized type", map[string]any{"action": "type", "text": strings.Repeat("x", 501)}},
		{"wrong text type", map[string]any{"action": "type", "text": 4}},
		{"missing key", map[string]any{"action": "key"}},
		{"empty key", map[string]any{"action": "key", "text": " "}},
		{"missing hold duration", map[string]any{"action": "hold_key", "text": "Right"}},
		{"negative hold duration", map[string]any{"action": "hold_key", "text": "Right", "duration": -1}},
		{"missing wait duration", map[string]any{"action": "wait"}},
		{"wrong duration type", map[string]any{"action": "wait", "duration": "1"}},
		{"unsupported modifier", map[string]any{"action": "left_click", "coordinate": []int{0, 0}, "text": "hyper"}},
		{"duplicate modifier", map[string]any{"action": "left_click", "coordinate": []int{0, 0}, "text": "cmd+super"}},
		{"empty modifier separators", map[string]any{"action": "left_click", "coordinate": []int{0, 0}, "text": "+,"}},
		{"unexpected point", map[string]any{"action": "screenshot", "coordinate": []int{0, 0}}},
		{"unexpected start point", map[string]any{"action": "mouse_move", "coordinate": []int{0, 0}, "start_coordinate": []int{0, 0}}},
		{"unexpected region", map[string]any{"action": "screenshot", "region": []int{0, 0, 2, 2}}},
		{"unexpected duration", map[string]any{"action": "key", "text": "Return", "duration": 1}},
		{"unexpected scroll amount", map[string]any{"action": "screenshot", "scroll_amount": 1}},
		{"unexpected text", map[string]any{"action": "mouse_move", "coordinate": []int{0, 0}, "text": "do not leak"}},
		{"unknown field", map[string]any{"action": "screenshot", "session_index": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newComputerToolTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid request reached the backend")
				http.Error(w, "unexpected request", http.StatusInternalServerError)
			})
			if _, ok := tc.arguments["computer_session_id"]; !ok && tc.name != "missing session" {
				tc.arguments["computer_session_id"] = computerToolSessionID
			}
			result, err := callComputerToolRaw(t, server, tc.arguments)
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("invalid request succeeded: %+v", result)
			}
		})
	}
}

func TestComputerToolInvalidInputErrorsDoNotEchoSecretsOrCoordinates(t *testing.T) {
	server := newComputerToolTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid request reached the backend")
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	})
	for _, arguments := range []map[string]any{
		{"action": "type", "text": "password-do-not-echo-7f04c8", "coordinate": []int{1234, 567}, "unknown": "private-value"},
		{"action": "key", "text": map[string]any{"credential": "private-value"}},
		{"action": "left_click", "coordinate": []any{"private-value", 567}},
		{"action": "left_click", "coordinate": []int{1234, 56789}},
	} {
		arguments["computer_session_id"] = computerToolSessionID
		result, err := callComputerToolRaw(t, server, arguments)
		if err != nil || result == nil || !result.IsError {
			t.Fatalf("invalid call result = %#v, err = %v", result, err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"password-do-not-echo-7f04c8", "1234", "567", "private-value"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("invalid input error echoed caller data: %s", encoded)
			}
		}
	}
}

func callComputerToolRaw(t *testing.T, server *Server, arguments map[string]any) (*mcpsdk.CallToolResult, error) {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "computer-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	return clientSession.CallTool(ctx, &mcpsdk.CallToolParams{Name: "computer", Arguments: arguments})
}

func TestComputerToolWaitCancellation(t *testing.T) {
	server := newComputerToolTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("cancelled wait reached the backend")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	duration := 10.0
	started := time.Now()
	result, output, err := server.handleComputer(ctx, nil, ComputerToolInput{ComputerSessionID: computerToolSessionID, Action: "wait", Duration: &duration})
	if err != nil || result == nil || !result.IsError || !strings.Contains(output.Error, "context canceled") || time.Since(started) > time.Second {
		t.Fatalf("cancelled wait = %+v, %+v, %v", result, output, err)
	}
	zero := 0.0
	_, output, _ = server.handleComputer(ctx, nil, ComputerToolInput{ComputerSessionID: computerToolSessionID, Action: "wait", Duration: &zero})
	if !strings.Contains(output.Error, "context canceled") {
		t.Fatalf("cancelled zero wait = %+v", output)
	}
}

func TestComputerToolClampsHoldDuration(t *testing.T) {
	duration := 30.0
	request, err := computerScreenAction(ComputerToolInput{
		Action: "hold_key", Text: "Right", Duration: &duration,
	})
	if err != nil {
		t.Fatalf("computerScreenAction() = %v", err)
	}
	if request.Action.HoldSeconds != computerMaxHoldSeconds {
		t.Fatalf("hold seconds = %v, want %d", request.Action.HoldSeconds, computerMaxHoldSeconds)
	}
	if got := minComputerDuration(duration, computerMaxWaitSeconds); got != computerMaxWaitSeconds {
		t.Fatalf("wait seconds = %v, want %d", got, computerMaxWaitSeconds)
	}
}

func TestComputerToolErrorsAreSafeAndNeverRetried(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		for _, action := range []string{"screenshot", "left_click"} {
			t.Run(http.StatusText(status)+"/"+action, func(t *testing.T) {
				var calls atomic.Int32
				server := newComputerToolTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"detail":"private-provider-response"}`))
				})
				args := map[string]any{"computer_session_id": computerToolSessionID, "action": action}
				if action == "left_click" {
					args["coordinate"] = []int{0, 0}
				}
				result := callServerTool(t, server, "computer", args)
				output := decodeStructuredToolResult[ComputerToolOutput](t, result)
				if !result.IsError || output.Error == "" || strings.Contains(output.Error, "private-provider-response") || calls.Load() != 1 {
					t.Fatalf("failure = %+v, output=%+v, calls=%d", result, output, calls.Load())
				}
			})
		}
	}
}

func TestComputerToolRejectsMissingOrMalformedScreenshot(t *testing.T) {
	for _, payload := range []string{
		`{"screenshot":null,"cursor":null}`,
		`{"screenshot":{"width":3,"height":2,"observed_at":"2026-10-19T12:00:00Z","png":"cG5n","cursor":null},"cursor":null}`,
		`{"screenshot":{"width":3,"height":2,"observed_at":"bad","png":"cG5n","cursor":null},"cursor":null}`,
		`{"cursor":null}`,
	} {
		t.Run(payload, func(t *testing.T) {
			server := newComputerToolTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(payload))
			})
			result := callServerTool(t, server, "computer", map[string]any{"computer_session_id": computerToolSessionID, "action": "left_click", "coordinate": []int{0, 0}})
			if !result.IsError || containsImageContent(result.Content) {
				t.Fatalf("invalid screenshot succeeded: %+v", result)
			}
		})
	}
}

func TestComputerToolProfilesAndAuthenticationRefresh(t *testing.T) {
	for _, profile := range []Profile{"", ProfileCore, ProfileFull, ProfileDev} {
		t.Run(string(profile), func(t *testing.T) {
			prepareServerAuthTest(t)
			t.Setenv("REVYL_API_KEY", "fixture-key")
			server, err := NewServer("test", false, WithProfile(profile), WithExperimentalComputer())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(server.Shutdown)
			tool := serverToolByName(t, listServerTools(t, server), "computer")
			requireConcreteToolSchema(t, tool)
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
				t.Fatal("computer must advertise its input side effects")
			}
			if profile != ProfileDev {
				return
			}
			fixture := computerToolScreenshotFixture(t)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer refreshed-fixture-key" {
					t.Error("dev authentication was not refreshed")
				}
				_ = json.NewEncoder(w).Encode(fixture)
			}))
			t.Cleanup(backend.Close)
			server.apiClient = api.NewClientWithBaseURL("stale-fixture-key", backend.URL)
			t.Setenv("REVYL_API_KEY", "refreshed-fixture-key")
			result := callServerTool(t, server, "computer", map[string]any{"computer_session_id": computerToolSessionID, "action": "screenshot"})
			if result.IsError {
				t.Fatalf("authenticated computer screenshot failed: %+v", result)
			}
		})
	}
}

func TestComputerToolIsAbsentWithoutExperimentalOption(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	for _, profile := range []Profile{ProfileDev, ProfileCore, ProfileFull, ""} {
		server, err := NewServer("test", false, WithProfile(profile))
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range listServerTools(t, server) {
			if tool.Name == "computer" {
				t.Fatalf("profile %q unexpectedly registered computer", profile)
			}
		}
		server.Shutdown()
	}
}
