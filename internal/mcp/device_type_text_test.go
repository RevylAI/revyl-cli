package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

func TestResolveDeviceTypeText(t *testing.T) {
	t.Setenv("TYPE_TEST_INPUT", "synthetic-value")
	t.Setenv("EMPTY_TYPE_TEST_INPUT", "")
	for _, tc := range []struct {
		name, text, envName, want string
		wantErr                   bool
	}{
		{name: "inline", text: "inline", want: "inline"},
		{name: "environment", envName: "TYPE_TEST_INPUT", want: "synthetic-value"},
		{name: "missing input", wantErr: true},
		{name: "exclusive inputs", text: "synthetic-value", envName: "TYPE_TEST_INPUT", wantErr: true},
		{name: "empty variable", envName: "EMPTY_TYPE_TEST_INPUT", wantErr: true},
		{name: "invalid name", envName: "synthetic-value=not-a-name", wantErr: true},
		{name: "oversized name", envName: strings.Repeat("A", 129), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, err := ResolveDeviceTypeText(tc.text, tc.envName)
			if (err != nil) != tc.wantErr || text != tc.want {
				t.Fatalf("resolution failed for case %s", tc.name)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-value") {
				t.Fatal("validation error echoed input")
			}
		})
	}
}

func TestWebDeviceTypeStripsWorkerEchoAndDoesNotRetry(t *testing.T) {
	const typedText = "synthetic-private-type-value"
	t.Setenv("TYPE_TEST_INPUT", typedText)
	for _, status := range []int{200, 400, 503} {
		for _, success := range []bool{true, false} {
			calls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["text"] != typedText {
					t.Error("typing did not deliver the locally resolved value")
				}
				if body["password_only"] != true {
					t.Error("Web environment input omitted password-only guard")
				}
				if _, present := body["text_env"]; present {
					t.Error("input transported the environment variable name")
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": success, "text": typedText, "error": typedText, "target": typedText, "action": typedText})
			}))
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", backend.URL), "")
			manager.sessions[0] = &DeviceSession{Index: 0, SessionID: startIDSessionID, WorkflowRunID: startIDWorkflowRunID, Platform: "web"}
			manager.activeIndex = 0
			server := &Server{sessionMgr: manager}
			x, y := 10, 20
			_, result, err := server.handleDeviceType(context.Background(), nil, DeviceTypeInput{TextEnv: "TYPE_TEST_INPUT", X: &x, Y: &y})
			encoded, marshalErr := json.Marshal(result)
			if marshalErr != nil || err != nil || strings.Contains(string(encoded), typedText) || strings.Contains(string(encoded), "TYPE_TEST_INPUT") || calls != 1 {
				t.Fatalf("unsafe response or retry: calls=%d err=%v", calls, err)
			}
			if result.Success != (status == 200 && success) {
				t.Fatalf("success=%t status=%d workerSuccess=%t", result.Success, status, success)
			}
			manager.StopIdleTimer(0)
			backend.Close()
		}
	}
}

func TestInteractTypeResolvesEnvironmentReferenceLocally(t *testing.T) {
	t.Setenv("TYPE_TEST_INPUT", "synthetic-interact-value")
	seen := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/resolve_target"):
			_, _ = w.Write([]byte(`{"found":true,"x":10,"y":20,"screen_token":"synthetic-runtime-observation"}`))
		case strings.HasSuffix(r.URL.Path, "/input"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["text"] != "synthetic-interact-value" {
				t.Error("interact did not pass the locally resolved value")
			}
			if body["password_only"] != true {
				t.Error("interact omitted password-only guard")
			}
			seen = true
			_, _ = w.Write([]byte(`{"success":true,"text":"synthetic-interact-value"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	manager := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", backend.URL), "")
	manager.sessions[0] = &DeviceSession{Index: 0, SessionID: startIDSessionID, WorkflowRunID: startIDWorkflowRunID, Platform: "web"}
	manager.activeIndex = 0
	t.Cleanup(func() { manager.StopIdleTimer(0) })
	server := &Server{sessionMgr: manager}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"interaction_type":"type","text_env":"TYPE_TEST_INPUT"}`)}}
	_, result, err := server.dispatchInteract(context.Background(), req, InteractInput{InteractionType: "type", Target: "password field", TextEnv: "TYPE_TEST_INPUT"}, "semantic", "")
	encoded, marshalErr := json.Marshal(result)
	if err != nil || marshalErr != nil || !seen || result.Result["success"] != true || strings.Contains(string(encoded), "synthetic-interact-value") || strings.Contains(string(req.Params.Arguments), "synthetic-interact-value") {
		t.Fatalf("interact typing failed without a safe result: %v", err)
	}
}

func TestDeviceTypeRejectsExplicitEmptyTextWithEnvironmentInput(t *testing.T) {
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"text":"","text_env":"TYPE_TEST_INPUT"}`)}}
	server := &Server{}
	_, output, err := server.handleDeviceType(context.Background(), req, DeviceTypeInput{TextEnv: "TYPE_TEST_INPUT"})
	if err != nil || output.Success || output.Error != "text and text_env are mutually exclusive" {
		t.Fatalf("exclusive input validation=%+v err=%v", output, err)
	}
}

func TestDeviceTypePasswordOnlyTransportScope(t *testing.T) {
	const typedText = "synthetic-private-type-value"
	t.Setenv("TYPE_TEST_INPUT", typedText)
	for _, platform := range []string{"web", "ios", "android"} {
		for _, envInput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/env=%t", platform, envInput), func(t *testing.T) {
				calls := 0
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["text"] != typedText {
						t.Error("input did not transport the resolved value")
					}
					passwordOnly, present := body["password_only"]
					if platform == "web" && envInput {
						if passwordOnly != true {
							t.Error("Web environment input omitted password-only guard")
						}
					} else if present {
						t.Error("ordinary Web or phone input changed its request contract")
					}
					if _, present := body["text_env"]; present {
						t.Error("input transported the environment variable name")
					}
					_, _ = w.Write([]byte(`{"success":true}`))
				}))
				t.Cleanup(backend.Close)
				manager := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", backend.URL), "")
				manager.sessions[0] = &DeviceSession{Index: 0, SessionID: startIDSessionID, WorkflowRunID: startIDWorkflowRunID, Platform: platform}
				manager.activeIndex = 0
				t.Cleanup(func() { manager.StopIdleTimer(0) })
				server := &Server{sessionMgr: manager}
				x, y := 10, 20
				input := DeviceTypeInput{Text: typedText, X: &x, Y: &y}
				if envInput {
					input.Text, input.TextEnv = "", "TYPE_TEST_INPUT"
				}
				_, output, err := server.handleDeviceType(context.Background(), nil, input)
				encoded, marshalErr := json.Marshal(output)
				if err != nil || marshalErr != nil || !output.Success || calls != 1 || strings.Contains(string(encoded), typedText) || strings.Contains(string(encoded), "TYPE_TEST_INPUT") {
					t.Fatalf("calls=%d success=%t err=%v", calls, output.Success, err)
				}
			})
		}
	}
}
