package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

const fixtureSessionTraceID = "1234567890abcdef1234567890abcdef"

type runtimeDiagnosticTransport func(*http.Request) (*http.Response, error)

func (transport runtimeDiagnosticTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func runtimeDiagnosticManager(t *testing.T, transport runtimeDiagnosticTransport) (*DeviceSessionManager, *DeviceSession) {
	t.Helper()
	client := api.NewClientWithBaseURL("fixture-key", "https://fixture.test")
	client.SetHTTPClient(&http.Client{Timeout: api.DefaultTimeout, Transport: transport})
	manager := NewDeviceSessionManager(client, "")
	session := &DeviceSession{Index: UnattachedSessionIndex, SessionID: "fixture-session", WorkflowRunID: "fixture-workflow", Platform: "web", TraceID: fixtureSessionTraceID}
	return manager, session
}

func TestRuntimeProxyDiagnosticsPreserveTypedTransportCause(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		code  string
		class analytics.FailureClass
	}{
		{"deadline", context.DeadlineExceeded, "request_timeout", analytics.FailureClassTimeout},
		{"network timeout", &net.DNSError{IsTimeout: true, Name: "private customer hostname"}, "request_timeout", analytics.FailureClassTimeout},
		{"cancelled", context.Canceled, "request_cancelled", analytics.FailureClassCancelled},
		{"connect", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("private payload Bearer fixture-secret-token")}, "connection_closed", analytics.FailureClassNetwork},
		{"untyped transport", errors.New("private customer text https://fixture.test/?token=fixture-secret-token"), "connection_closed", analytics.FailureClassNetwork},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/resolve_target") {
					t.Fatal("target resolution changed its transport")
				}
				return nil, tc.cause
			})
			_, err := manager.ResolveTargetOnSession(context.Background(), session, "private customer target")
			var workerErr *WorkerHTTPError
			if !errors.As(err, &workerErr) || !errors.Is(err, tc.cause) || workerErr.Code != tc.code || workerErr.ExecutionUncertain || calls != 1 {
				t.Fatalf("lost transport cause or replayed resolution: calls=%d err=%v", calls, err)
			}
			if got := analytics.FailureClassForError(err); got != tc.class {
				t.Fatalf("failure class=%q, want %q", got, tc.class)
			}
			want := fmt.Sprintf("worker %s on /resolve_target (HTTP 0); failure_class=%s; session_trace_id=%s", tc.code, tc.class, fixtureSessionTraceID)
			if err.Error() != want {
				t.Fatalf("safe diagnostic=%q, want %q", err.Error(), want)
			}
		})
	}
}

func TestRuntimeProxyDiagnosticsDefaultHTTPDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
			calls++
			<-request.Context().Done()
			return nil, request.Context().Err()
		})
		started := time.Now()
		_, err := manager.ResolveTargetOnSession(context.Background(), session, "fixture target")
		if time.Since(started) != api.DefaultTimeout || !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
			t.Fatalf("deadline changed or resolution replayed: elapsed=%s calls=%d err=%v", time.Since(started), calls, err)
		}
		if !strings.Contains(err.Error(), "worker request_timeout on /resolve_target") || !strings.Contains(err.Error(), "session_trace_id="+fixtureSessionTraceID) {
			t.Fatalf("deadline diagnosis lost: %v", err)
		}
	})
}

type runtimeDiagnosticBody struct{}

func (runtimeDiagnosticBody) Read([]byte) (int, error) {
	return 0, fmt.Errorf("private response payload: %w", context.DeadlineExceeded)
}

func (runtimeDiagnosticBody) Close() error { return nil }

func TestRuntimeProxyDiagnosticsResponseReadFailureAndPathRedaction(t *testing.T) {
	manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: runtimeDiagnosticBody{}}, nil
	})
	for _, path := range []string{"/resolve_target?token=private-secret-token", "/step_status/private-customer-id", "/private-customer-action"} {
		_, err := manager.proxyWorkerRequestForSession(context.Background(), session, path, nil)
		var workerErr *WorkerHTTPError
		if !errors.As(err, &workerErr) || workerErr.StatusCode != http.StatusOK || !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "private") {
			t.Fatalf("response read failure lost status or leaked content: %v", err)
		}
	}
}

func TestRuntimeProxyDiagnosticsStatusAndTraceRedaction(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		class  analytics.FailureClass
	}{
		{401, "unauthorized", analytics.FailureClassAuth},
		{429, "capacity_exhausted", analytics.FailureClassQuota},
		{409, "stale_target", analytics.FailureClassUsage},
		{504, "request_timeout", analytics.FailureClassTimeout},
		{502, "connection_closed", analytics.FailureClassServer},
		{503, "private customer text", analytics.FailureClassServer},
	} {
		for _, traceID := range []string{fixtureSessionTraceID, strings.ToUpper(fixtureSessionTraceID), "", strings.Repeat("0", 32), "private-secret-token", fixtureSessionTraceID + "?token=private-secret-token", strings.Repeat("x", 10000)} {
			t.Run(fmt.Sprintf("%d/trace-length-%d", tc.status, len(traceID)), func(t *testing.T) {
				calls := 0
				manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
					calls++
					body, _ := json.Marshal(map[string]any{"error": tc.code, "detail": "private customer payload", "trace_id": "private-secret-token", "url": "https://fixture.test/?token=private-secret-token"})
					return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})
				session.TraceID = traceID
				_, err := manager.ResolveTargetOnSession(context.Background(), session, "private customer target")
				var workerErr *WorkerHTTPError
				if !errors.As(err, &workerErr) || workerErr.StatusCode != tc.status || workerErr.Body != "" || calls != 1 || analytics.FailureClassForError(err) != tc.class {
					t.Fatalf("status diagnosis or redaction failed: calls=%d err=%v", calls, err)
				}
				if tc.code != "private customer text" && workerErr.Code != tc.code {
					t.Fatalf("worker code=%q, want %q", workerErr.Code, tc.code)
				}
				for _, forbidden := range []string{"private", "https://", "token=", "customer"} {
					if strings.Contains(err.Error(), forbidden) {
						t.Fatalf("diagnostic leaked %q", forbidden)
					}
				}
				validTrace := traceID == fixtureSessionTraceID || traceID == strings.ToUpper(fixtureSessionTraceID)
				if strings.Contains(err.Error(), "session_trace_id="+fixtureSessionTraceID) != validTrace || len(err.Error()) > 500 {
					t.Fatalf("invalid or unbounded trace diagnostic: %q", err.Error())
				}
			})
		}
	}
}

func TestRuntimeProxyDiagnosticsUncertainInputAndPhoneCompatibility(t *testing.T) {
	for _, platform := range []string{"web", "ios", "android"} {
		t.Run(platform, func(t *testing.T) {
			calls := 0
			manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
				calls++
				return nil, context.Canceled
			})
			session.Platform = platform
			_, err := manager.proxyWorkerRequestForSession(context.Background(), session, "/tap", map[string]any{"x": 1, "y": 2})
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("cancelled input replayed or lost cause: calls=%d err=%v", calls, err)
			}
			var workerErr *WorkerHTTPError
			if platform == "web" {
				if !errors.As(err, &workerErr) || !workerErr.ExecutionUncertain || !strings.Contains(err.Error(), "do not repeat") || !strings.Contains(err.Error(), "failure_class=cancelled") || !strings.Contains(err.Error(), fixtureSessionTraceID) {
					t.Fatalf("uncertain input diagnosis lost: %v", err)
				}
			} else if errors.As(err, &workerErr) || strings.Contains(err.Error(), "session_trace_id=") {
				t.Fatal("phone transport error contract changed")
			}
		})
	}
}

func TestRuntimeProxyDiagnosticsAllowlistedTimeoutStagesOnly(t *testing.T) {
	for _, code := range []string{"request_timeout", "connection_closed", "grounding_failed"} {
		for _, stage := range []string{"grounding", "capture", "guard", "input", "settle", "publication", "", "private customer text", "https://fixture.test/?token=private-secret-token", strings.Repeat("x", 10000)} {
			t.Run(fmt.Sprintf("%s/stage-length-%d", code, len(stage)), func(t *testing.T) {
				manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
					body, _ := json.Marshal(api.RuntimeWorkerFailure{Error: code, FailureStage: stage})
					return &http.Response{StatusCode: http.StatusGatewayTimeout, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})
				_, err := manager.ResolveTargetOnSession(context.Background(), session, "fixture target")
				var workerErr *WorkerHTTPError
				if !errors.As(err, &workerErr) {
					t.Fatalf("timeout lost typed worker error: %v", err)
				}
				validStage := code == "request_timeout" && (stage == "grounding" || stage == "capture" || stage == "guard" || stage == "input" || stage == "settle" || stage == "publication")
				if strings.Contains(err.Error(), "failure_stage=") != validStage || validStage && !strings.Contains(err.Error(), "failure_stage="+stage) || len(err.Error()) > 500 {
					t.Fatalf("stage was inferred or not allowlisted: %q", err.Error())
				}
			})
		}
	}
}

func TestRuntimeProxyDiagnosticsTimeoutDispatchFactsAndNoReplayReceipts(t *testing.T) {
	for _, tc := range []struct {
		name               string
		path               string
		failure            api.RuntimeWorkerFailure
		wantUncertain      bool
		wantDelivered      bool
		wantDispatchMarker string
	}{
		{name: "grounding zero dispatch", failure: api.RuntimeWorkerFailure{Error: "request_timeout", FailureStage: "grounding", InputDispatched: boolPtr(false)}, wantDispatchMarker: "input_dispatched=false"},
		{name: "publication after delivered input", failure: api.RuntimeWorkerFailure{Error: "request_timeout", FailureStage: "publication", InputDispatched: boolPtr(true), ActionApplied: boolPtr(true), Recorded: boolPtr(false)}, wantDelivered: true, wantDispatchMarker: "input_dispatched=true"},
		{name: "settle after delivered input", failure: api.RuntimeWorkerFailure{Error: "request_timeout", FailureStage: "settle", InputDispatched: boolPtr(true), ActionApplied: boolPtr(true)}, wantDelivered: true, wantDispatchMarker: "input_dispatched=true"},
		{name: "mid-input uncertain", path: "/input", failure: api.RuntimeWorkerFailure{Error: "request_timeout", FailureStage: "input", InputDispatched: boolPtr(true), ExecutionUncertain: true}, wantUncertain: true, wantDispatchMarker: "input_dispatched=true"},
		{name: "dispatched without applied fact", failure: api.RuntimeWorkerFailure{Error: "request_timeout", FailureStage: "input", InputDispatched: boolPtr(true)}, wantUncertain: true, wantDispatchMarker: "input_dispatched=true"},
		{name: "explicit uncertainty stays", failure: api.RuntimeWorkerFailure{Error: "request_timeout", FailureStage: "input", InputDispatched: boolPtr(true), ActionApplied: boolPtr(true), ExecutionUncertain: true}, wantUncertain: true, wantDispatchMarker: "input_dispatched=true"},
		{name: "contradictory zero dispatch", failure: api.RuntimeWorkerFailure{Error: "request_timeout", FailureStage: "input", InputDispatched: boolPtr(false), ActionApplied: boolPtr(true)}, wantUncertain: true, wantDispatchMarker: "input_dispatched=false"},
		{name: "old server missing dispatch", failure: api.RuntimeWorkerFailure{Error: "request_timeout", FailureStage: "publication"}, wantUncertain: true},
		{name: "non-timeout ignores dispatch", failure: api.RuntimeWorkerFailure{Error: "connection_closed", FailureStage: "publication", InputDispatched: boolPtr(true), ActionApplied: boolPtr(true)}, wantUncertain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			path := tc.path
			if path == "" {
				path = "/tap_target"
			}
			manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if !strings.HasSuffix(request.URL.Path, path) {
					t.Fatal("timeout triggered a fallback input")
				}
				body, err := json.Marshal(tc.failure)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: http.StatusGatewayTimeout, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})
			_, err := manager.WorkerRequestOnSession(context.Background(), session, path, map[string]any{"target": "fixture target"})
			var workerErr *WorkerHTTPError
			var safeDiagnostic *analytics.SafeDiagnosticError
			if !errors.As(err, &workerErr) || !errors.As(err, &safeDiagnostic) || workerErr.ExecutionUncertain != tc.wantUncertain || calls != 1 {
				t.Fatalf("dispatch uncertainty lost or timeout replayed: calls=%d err=%v", calls, err)
			}
			message := err.Error()
			var statusCause *api.APIError
			if !errors.As(err, &statusCause) || statusCause.StatusCode != http.StatusGatewayTimeout || !strings.Contains(message, "session_trace_id="+fixtureSessionTraceID) {
				t.Fatalf("JSON-only timeout lost its status cause or trace: %q", message)
			}
			if tc.failure.Error == "request_timeout" && (!strings.Contains(message, "failure_stage="+tc.failure.FailureStage) || !strings.Contains(message, "failure_class=timeout")) {
				t.Fatalf("JSON-only timeout lost attribution: %q", message)
			}
			if strings.Contains(message, "input was delivered") != tc.wantDelivered || strings.Contains(message, "execution_uncertain:") != tc.wantUncertain {
				t.Fatalf("timeout overstated input outcome: %q", message)
			}
			if tc.wantDispatchMarker == "" && strings.Contains(message, "input_dispatched=") || tc.wantDispatchMarker != "" && !strings.Contains(message, tc.wantDispatchMarker) {
				t.Fatalf("missing or invented dispatch fact: %q", message)
			}
			if tc.wantDelivered && !strings.Contains(message, "only "+tc.failure.FailureStage+" timed out") {
				t.Fatalf("delivered input was reported as a failed action: %q", message)
			}
			if (tc.wantDelivered || tc.wantUncertain) && !strings.Contains(message, "do not repeat") {
				t.Fatalf("timeout invited a replay: %q", message)
			}
			_, receipt, receiptErr := wrapDevProfileAction("tap", nil, DeviceTapOutput{Success: false, Error: message}, nil)
			if receiptErr != nil || receipt.Outcome.Retryable || receipt.Outcome.Reason != message || receipt.Result["error"] != message {
				t.Fatalf("receipt lost no-replay outcome: %+v err=%v", receipt, receiptErr)
			}
		})
	}
}

func TestRuntimeProxyDiagnosticsUncertainErrorWithoutTransportCauseKeepsAttribution(t *testing.T) {
	err := &WorkerHTTPError{StatusCode: http.StatusGatewayTimeout, Path: "/tap", Code: "request_timeout", ExecutionUncertain: true, InputDispatched: boolPtr(true), failureStage: "publication", sessionTraceID: fixtureSessionTraceID}
	for _, fact := range []string{"execution_uncertain:", "failure_stage=publication", "session_trace_id=" + fixtureSessionTraceID, "input_dispatched=true", "do not repeat"} {
		if !strings.Contains(err.Error(), fact) {
			t.Fatalf("uncertain timeout lost %q: %v", fact, err)
		}
	}
	phoneErr := &WorkerHTTPError{StatusCode: http.StatusGatewayTimeout, Path: "/tap", ExecutionUncertain: true}
	if phoneErr.Error() != "execution_uncertain: the action may have been applied; do not repeat it automatically. Inspect the session report before deciding what to do next" {
		t.Fatalf("phone uncertainty text changed: %v", phoneErr)
	}
}

func TestRuntimeProxyDiagnosticsMalformedFailureCannotConfirmInput(t *testing.T) {
	for _, body := range []string{
		`{"error":"request_timeout","input_dispatched":false,"action_applied":false,"recorded":"invalid"}`,
		`{"error":"request_timeout","input_dispatched":true,"action_applied":true,"execution_uncertain":"invalid"}`,
		`{"error":{}}`,
		`{"error":"private failure text"}`,
		`{`,
	} {
		calls := 0
		manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusGatewayTimeout, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		_, err := manager.WorkerRequestOnSession(context.Background(), session, "/tap", map[string]any{"x": 1, "y": 2})
		var workerErr *WorkerHTTPError
		if !errors.As(err, &workerErr) || !workerErr.ExecutionUncertain || calls != 1 {
			t.Fatalf("malformed failure lost uncertainty: calls=%d err=%v", calls, err)
		}
		if workerErr.ActionApplied != nil || workerErr.InputDispatched != nil || !strings.Contains(err.Error(), "do not repeat") || strings.Contains(err.Error(), "private") {
			t.Fatalf("malformed failure supplied an input fact or leaked content: %v", err)
		}
	}
}

func TestRuntimeProxyDiagnosticsMCPReceiptAndFailureAnalytics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	t.Setenv("REVYL_ANALYTICS", "1")
	calls := 0
	manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("private customer text https://fixture.test/?token=private-secret-token: %w", context.DeadlineExceeded)
	})
	manager.sessions[0] = session
	session.Index = 0
	manager.activeIndex = 0
	manager.StopIdleTimer(0)
	server := &Server{sessionMgr: manager}
	_, receipt, err := server.handleDeviceTap(context.Background(), nil, DeviceTapInput{SessionID: session.SessionID, Target: "private customer target"})
	if err != nil || receipt.Success || calls != 1 {
		t.Fatalf("failed target resolution was replayed or lost: calls=%d receipt=%+v err=%v", calls, receipt, err)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "failure_class=timeout") || !strings.Contains(string(encoded), "session_trace_id="+fixtureSessionTraceID) || strings.Contains(string(encoded), "private") {
		t.Fatalf("unsafe or uncorrelated MCP receipt: %s", encoded)
	}
	_, compactReceipt, err := wrapDevProfileAction("tap", nil, receipt, nil)
	if err != nil || compactReceipt.Outcome.OperationStatus != "failed" || compactReceipt.Result["error"] != receipt.Error {
		t.Fatalf("compact receipt lost the safe failure: %+v err=%v", compactReceipt, err)
	}
	_, failure := manager.ResolveTargetOnSession(context.Background(), session, "private customer target")
	var payload analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(value analytics.TelemetryPayload) { payload.Events = append(payload.Events, value.Events...) })
	run := recorder.StartCommand(&cobra.Command{Use: "tap"}, nil)
	run.ObserveOutput("error", "private customer output")
	run.Complete(failure)
	recorder.Flush()
	for _, event := range payload.Events {
		if event.Event != analytics.CliCommandFailedEvent {
			continue
		}
		if event.Properties["failure_class"] != "timeout" || event.Properties["error_message"] != failure.Error() {
			t.Fatalf("failure analytics lost bounded diagnosis: %#v", event.Properties)
		}
		if _, exists := event.Properties["output_tail"]; exists {
			t.Fatal("failure analytics retained customer output")
		}
		return
	}
	t.Fatal("missing terminal failure analytics event")
}

func TestRuntimeInputTimingMetadataPreservesOnlyValidatedFields(t *testing.T) {
	for _, tc := range []struct {
		name, timingFields, expectedFields string
	}{
		{name: "absent"},
		{name: "fractional timings", timingFields: `,"grounding_bundle_duration_ms":12.125,"input_duration_ms":0,"settle_duration_ms":9.875`, expectedFields: `,"grounding_bundle_duration_ms":12.125,"input_duration_ms":0,"settle_duration_ms":9.875`},
		{name: "precision timings", timingFields: `,"grounding_bundle_duration_ms":114.690123456789012345,"input_duration_ms":8.937e+1,"settle_duration_ms":0`, expectedFields: `,"grounding_bundle_duration_ms":114.690123456789012345,"input_duration_ms":8.937e+1,"settle_duration_ms":0`},
		{name: "negative zero", timingFields: `,"grounding_bundle_duration_ms":-0,"input_duration_ms":-0.00,"settle_duration_ms":-0e5`, expectedFields: `,"grounding_bundle_duration_ms":-0,"input_duration_ms":-0.00,"settle_duration_ms":-0e5`},
		{name: "invalid metadata with valid sibling", timingFields: `,"grounding_bundle_duration_ms":"12.125","input_duration_ms":9.875,"settle_duration_ms":true`, expectedFields: `,"input_duration_ms":9.875`},
		{name: "negative null and overflow", timingFields: `,"grounding_bundle_duration_ms":-1,"input_duration_ms":null,"settle_duration_ms":1e309`},
		{name: "nonfinite strings and objects", timingFields: `,"grounding_bundle_duration_ms":"NaN","input_duration_ms":{},"settle_duration_ms":[]`},
		{name: "negative underflow and overlong metadata", timingFields: `,"grounding_bundle_duration_ms":-1e-999,"input_duration_ms":1.` + strings.Repeat("2", 64) + `,"settle_duration_ms":false`},
	} {
		for _, success := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/success=%t", tc.name, success), func(t *testing.T) {
				calls := 0
				response := fmt.Sprintf(`{"success":%t,"latency_ms":6.25,"x":12,"y":34%s,"text":"synthetic-private-text","target":"synthetic-private-target","screen_token":"synthetic-private-token","payload":{"secret":"synthetic-private-secret"},"other_duration_ms":1}`, success, tc.timingFields)
				manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
					calls++
					if !strings.HasSuffix(request.URL.Path, "/input") {
						t.Fatal("input metadata changed the action route")
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
				})
				actual, err := manager.WorkerRequestOnSession(context.Background(), session, "/input", map[string]any{"text": "synthetic-input"})
				if err != nil || calls != 1 {
					t.Fatalf("metadata changed action execution: calls=%d err=%v", calls, err)
				}
				want := fmt.Sprintf(`{"success":%t,"latency_ms":6.25,"x":12,"y":34%s}`, success, tc.expectedFields)
				if string(actual) != want {
					t.Fatalf("input metadata/redaction contract=%s, want %s", actual, want)
				}
			})
		}
	}
}

func TestRuntimeInputTimingPreservationDoesNotChangePhoneBytes(t *testing.T) {
	const response = " {\n  \"success\": false, \"latency_ms\": 6.25, \"x\": 12, \"y\": 34\n } "
	for _, platform := range []string{"ios", "android"} {
		t.Run(platform, func(t *testing.T) {
			calls := 0
			manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			session.Platform = platform
			actual, err := manager.WorkerRequestOnSession(context.Background(), session, "/input", map[string]any{"text": "synthetic-input"})
			if err != nil || calls != 1 || string(actual) != response {
				t.Fatalf("phone input body changed: calls=%d response=%q err=%v", calls, actual, err)
			}
		})
	}
}
