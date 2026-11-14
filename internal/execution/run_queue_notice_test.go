package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/billingstatus"
	"github.com/revyl/cli/internal/sse"
)

const queuedRunMessage = "Queued: all 2 devices are busy. This starts when one is free.\nUpgrade to Pro to run 10 at once ($20 credit, charged when you add a card): https://app.example/settings/billing?add_card=1"

func serveQueuedRun(t *testing.T, executeBody string, statusCalls *int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/execution/api/execute_test_id_async":
			_, _ = w.Write([]byte(executeBody))
		case "/api/v1/tests/get_test_execution_task":
			*statusCalls++
			_, _ = w.Write([]byte(`{"status":"completed","progress":100,"success":true}`))
		case "/api/v1/reports-v3/reports/by-execution/task-queued/context":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")
}

func TestRunTestReportsAQueuedStartBeforeItWaitsAndKeepsTheNoticeInTheResult(t *testing.T) {
	statusCalls := 0
	notice, _ := json.Marshal(map[string]any{"devices": 2, "action": "add_card", "billing_url": "https://app.example/settings/billing?add_card=1", "message": queuedRunMessage})
	serveQueuedRun(t, `{"task_id":"task-queued","queue_notice":`+string(notice)+`}`, &statusCalls)
	var queued *billingstatus.QueueNotice
	statusCallsWhenQueued := -1

	result, err := RunTest(context.Background(), "token", nil, RunTestParams{
		TestNameOrID:   "test-123",
		Timeout:        5,
		MonitoringMode: sse.MonitoringModePolling,
		OnQueued: func(notice *billingstatus.QueueNotice) {
			queued, statusCallsWhenQueued = notice, statusCalls
		},
	})

	if err != nil {
		t.Fatalf("RunTest() error = %v", err)
	}
	if queued == nil || statusCallsWhenQueued != 0 {
		t.Fatalf("OnQueued got %+v after %d status reads, want the notice before the wait", queued, statusCallsWhenQueued)
	}
	if queued.Message != queuedRunMessage || queued.Devices != 2 || queued.Action != "add_card" || !strings.HasPrefix(queued.NextStep, "Agents: this run starts when a device is free.") {
		t.Fatalf("queued = %+v", queued)
	}
	if !result.Success || result.QueueNotice != queued {
		t.Fatalf("result = %+v, want a passed run that kept its queue notice", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"queue_notice":{"devices":2,"action":"add_card","billing_url":"https://app.example/settings/billing?add_card=1","message":`) {
		t.Fatalf("result JSON lost queue_notice:\n%s", encoded)
	}
}

func TestRunTestThatDidNotQueueReportsNoQueueNotice(t *testing.T) {
	statusCalls := 0
	serveQueuedRun(t, `{"task_id":"task-queued"}`, &statusCalls)

	result, err := RunTest(context.Background(), "token", nil, RunTestParams{
		TestNameOrID:   "test-123",
		Timeout:        5,
		MonitoringMode: sse.MonitoringModePolling,
		OnQueued:       func(*billingstatus.QueueNotice) { t.Error("OnQueued called for a start that did not queue") },
	})

	if err != nil {
		t.Fatalf("RunTest() error = %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.QueueNotice != nil || strings.Contains(string(encoded), "queue_notice") {
		t.Fatalf("result names a queue it never joined:\n%s", encoded)
	}
}
