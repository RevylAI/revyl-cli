package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/analytics"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

func TestGroundedTapPreservesSafeProxyFailureWithoutInputOrJSONBanner(t *testing.T) {
	const traceID = "1234567890abcdef1234567890abcdef"
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", jsonOutput), func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "fixture-key")
			t.Setenv(sessionIDEnvVar, "")
			resolveCalls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/execution/device-sessions/" + flowSessionID:
					fmt.Fprintf(w, `{"id":%q,"org_id":"fixture-org","platform":"web","viewer_kind":"whep","status":"running","workflow_run_id":%q,"trace_id":%q}`, flowSessionID, flowWorkflowRunID, traceID)
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/resolve_target":
					resolveCalls++
					w.WriteHeader(http.StatusGatewayTimeout)
					fmt.Fprint(w, `{"error":"request_timeout","failure_stage":"grounding","input_dispatched":false,"detail":"private customer payload","trace_id":"private-secret-token"}`)
				default:
					t.Errorf("failed resolution reached input or fallback: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backend.Close)
			t.Setenv("REVYL_BACKEND_URL", backend.URL)
			cmd := newStopTestCommand(t, "-s", flowSessionID)
			cmd.Flags().String("target", "", "")
			cmd.Flags().Int("x", 0, "")
			cmd.Flags().Int("y", 0, "")
			if err := cmd.Flags().Set("target", "private customer target"); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Flags().Set("json", fmt.Sprint(jsonOutput)); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			err := deviceTapCmd.RunE(cmd, nil)
			var workerErr *mcppkg.WorkerHTTPError
			if !errors.As(err, &workerErr) || workerErr.Code != "request_timeout" || workerErr.ExecutionUncertain || resolveCalls != 1 {
				t.Fatalf("failure lost or input replayed: calls=%d err=%v", resolveCalls, err)
			}
			if analytics.FailureClassForError(err) != analytics.FailureClassTimeout || !strings.Contains(err.Error(), "session_trace_id="+traceID) || !strings.Contains(err.Error(), "failure_stage=grounding") || !strings.Contains(err.Error(), "input_dispatched=false") || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe or uncorrelated CLI failure: %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failure polluted stdout: %q", stdout.String())
			}
		})
	}
}

func TestGroundedTapFollowupTimeoutDoesNotRepeatDeliveredInput(t *testing.T) {
	for _, stage := range []string{"settle", "publication"} {
		t.Run(stage, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "fixture-key")
			t.Setenv(sessionIDEnvVar, "")
			resolveCalls, inputCalls := 0, 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/execution/device-sessions/" + flowSessionID:
					fmt.Fprintf(w, `{"id":%q,"org_id":"fixture-org","platform":"web","viewer_kind":"whep","status":"running","workflow_run_id":%q}`, flowSessionID, flowWorkflowRunID)
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/resolve_target":
					resolveCalls++
					fmt.Fprint(w, `{"found":true,"x":1,"y":2,"screen_token":"fixture-observation"}`)
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/tap":
					inputCalls++
					w.WriteHeader(http.StatusGatewayTimeout)
					fmt.Fprintf(w, `{"error":"request_timeout","failure_stage":%q,"input_dispatched":true,"action_applied":true,"recorded":false}`, stage)
				default:
					t.Errorf("timeout reached an unexpected endpoint: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backend.Close)
			t.Setenv("REVYL_BACKEND_URL", backend.URL)
			cmd := newStopTestCommand(t, "-s", flowSessionID)
			cmd.Flags().String("target", "", "")
			cmd.Flags().Int("x", 0, "")
			cmd.Flags().Int("y", 0, "")
			if err := cmd.Flags().Set("target", "fixture target"); err != nil {
				t.Fatal(err)
			}
			err := deviceTapCmd.RunE(cmd, nil)
			var workerErr *mcppkg.WorkerHTTPError
			if !errors.As(err, &workerErr) || workerErr.ExecutionUncertain || workerErr.InputDispatched == nil || !*workerErr.InputDispatched || resolveCalls != 1 || inputCalls != 1 {
				t.Fatalf("delivered input lost or repeated: resolve=%d input=%d err=%v", resolveCalls, inputCalls, err)
			}
			if !strings.Contains(err.Error(), "input was delivered; only "+stage+" timed out; do not repeat it") || !strings.Contains(err.Error(), "input_dispatched=true") {
				t.Fatalf("CLI invited replay after a follow-up timeout: %v", err)
			}
		})
	}
}
