package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRuntimeInputBlockedMessageIsShown(t *testing.T) {
	manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
		body := `{"error":"input_blocked","message":"This input was blocked before it reached the page."}`
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	_, err := manager.WorkerRequestOnSession(context.Background(), session, "/input", nil)
	if err == nil || !strings.Contains(err.Error(), "This input was blocked before it reached the page.") {
		t.Fatalf("blocked-input message was lost: %v", err)
	}
}

func TestRuntimeProxyResponseValidationReceiptPreservesUnknownFacts(t *testing.T) {
	for _, path := range []string{"/input", "/navigate", "/resolve_target", "/screenshot"} {
		t.Run(path, func(t *testing.T) {
			calls := 0
			mutating := path == "/input" || path == "/navigate"
			manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
				calls++
				body := fmt.Sprintf(`{"error":"protocol_error","failure_stage":"response_validation","execution_uncertain":%t}`, mutating)
				return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			_, err := manager.WorkerRequestOnSession(context.Background(), session, path, nil)
			var workerErr *WorkerHTTPError
			if !errors.As(err, &workerErr) || calls != 1 {
				t.Fatalf("proxy receipt lost or request replayed: calls=%d err=%v", calls, err)
			}
			if workerErr.Code != "protocol_error" || workerErr.StatusCode != http.StatusBadGateway || workerErr.failureStage != "response_validation" || workerErr.ExecutionUncertain != mutating {
				t.Fatalf("proxy receipt changed: %+v", workerErr)
			}
			if workerErr.Body != "" || workerErr.ActionApplied != nil || workerErr.Recorded != nil || workerErr.InputDispatched != nil {
				t.Fatal("proxy receipt invented action facts or retained its body")
			}
			if !strings.Contains(err.Error(), "failure_stage=response_validation") || strings.Contains(err.Error(), "execution_uncertain:") != mutating || strings.Contains(err.Error(), "do not repeat") != mutating {
				t.Fatalf("printable proxy receipt lost stage or uncertainty: %v", err)
			}
			for _, invented := range []string{"input_dispatched=", "action_applied:", "recorded="} {
				if strings.Contains(err.Error(), invented) {
					t.Fatalf("printable proxy receipt invented %q", invented)
				}
			}
		})
	}
}

func TestRuntimeProxyResponseValidationStageRejectsInvalidReceipts(t *testing.T) {
	for _, body := range []string{
		`{"error":"protocol_error","failure_stage":"input"}`,
		`{"error":"protocol_error","failure_stage":"private https://example.test/?token=private-token"}`,
		`{"error":"navigation_failed","failure_stage":"response_validation"}`,
		`{"error":"request_timeout","failure_stage":"response_validation"}`,
		`{"error":"private-error","failure_stage":"response_validation"}`,
		`{"error":"protocol_error","failure_stage":"response_validation","input_dispatched":false,"action_applied":false,"recorded":"invalid"}`,
		`{"error":"protocol_error","failure_stage":"response_validation","execution_uncertain":"invalid"}`,
		`Internal Server Error`,
	} {
		calls := 0
		manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		_, err := manager.WorkerRequestOnSession(context.Background(), session, "/input", nil)
		var workerErr *WorkerHTTPError
		if !errors.As(err, &workerErr) || calls != 1 || workerErr.failureStage != "" || workerErr.Body != "" {
			t.Fatalf("invalid proxy receipt accepted or replayed: calls=%d err=%v", calls, err)
		}
		if workerErr.ActionApplied != nil || workerErr.Recorded != nil || workerErr.InputDispatched != nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("invalid proxy receipt supplied an action fact or private text: %v", err)
		}
		if workerErr.Code == "protocol_error" && !workerErr.ExecutionUncertain {
			t.Fatalf("malformed input receipt lost uncertainty: %v", err)
		}
	}
}

func TestRuntimeProxyFailedNavigationPreservesKnownActionFacts(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		for _, status := range []int{http.StatusConflict, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("uncertain=%t/status=%d", uncertain, status), func(t *testing.T) {
				calls := 0
				manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
					calls++
					body := fmt.Sprintf(`{"success":false,"action":"navigate","latency_ms":25,"error":"navigation_failed","failure_stage":"input","input_dispatched":true,"action_applied":false,"recorded":false,"execution_uncertain":%t}`, uncertain)
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				_, err := manager.WorkerRequestOnSession(context.Background(), session, "/navigate", nil)
				var workerErr *WorkerHTTPError
				if !errors.As(err, &workerErr) || calls != 1 || workerErr.Code != "navigation_failed" || workerErr.StatusCode != status {
					t.Fatalf("navigation failure changed or replayed: calls=%d err=%v", calls, err)
				}
				if workerErr.ExecutionUncertain != uncertain || workerErr.ActionApplied == nil || *workerErr.ActionApplied || workerErr.Recorded == nil || *workerErr.Recorded {
					t.Fatalf("known navigation facts changed: %+v", workerErr)
				}
				if strings.Contains(err.Error(), "execution_uncertain:") != uncertain || strings.Contains(err.Error(), "input was delivered") || strings.Contains(err.Error(), "action was applied") || strings.Contains(err.Error(), "response_validation") {
					t.Fatalf("navigation failure invented an outcome or proxy stage: %v", err)
				}
			})
		}
	}
}

func TestRuntimeProxyResponseValidationMCPFailureDoesNotInviteReplay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	calls := 0
	manager, session := runtimeDiagnosticManager(t, func(request *http.Request) (*http.Response, error) {
		calls++
		body := `{"error":"protocol_error","failure_stage":"response_validation","execution_uncertain":true,"detail":"private upstream body"}`
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	manager.sessions[0] = session
	session.Index = 0
	manager.activeIndex = 0
	t.Cleanup(func() { manager.StopIdleTimer(0) })
	server := &Server{sessionMgr: manager}
	_, receipt, err := server.handleDeviceNavigate(context.Background(), nil, DeviceNavigateInput{SessionID: session.SessionID, URL: "https://example.test"})
	if err != nil || receipt.Success || calls != 1 {
		t.Fatalf("MCP failure lost or replayed: calls=%d receipt=%+v err=%v", calls, receipt, err)
	}
	for _, marker := range []string{"protocol_error", "failure_stage=response_validation", "execution_uncertain:", "do not repeat"} {
		if !strings.Contains(receipt.Error, marker) {
			t.Fatalf("MCP failure lost %q: %s", marker, receipt.Error)
		}
	}
	if strings.Contains(receipt.Error, "private") || strings.Contains(receipt.Error, "input_dispatched=") {
		t.Fatalf("MCP failure exposed body or invented dispatch: %s", receipt.Error)
	}
	_, compact, err := wrapDevProfileAction("navigate", nil, receipt, nil)
	if err != nil || compact.Outcome.OperationStatus != "failed" || compact.Outcome.Retryable || compact.Outcome.Reason != receipt.Error || compact.Result["error"] != receipt.Error {
		t.Fatalf("compact MCP failure lost no-replay guidance: %+v err=%v", compact, err)
	}
}
