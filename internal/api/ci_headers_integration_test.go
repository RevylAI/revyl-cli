package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExecuteWorkflow_SendsCIHeaders is a wire-level smoke test: it spins up a
// fake backend, runs the real ExecuteWorkflow code path against it with
// faked GitHub Actions env vars, and asserts the X-CI-* headers arrived
// correctly. This is the closest we can get to option (a) without a real
// staging backend / API key.
func TestExecuteWorkflow_SendsCIHeaders(t *testing.T) {
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	prPayload := `{"pull_request":{"html_url":"https://github.com/acme/web/pull/482","number":482}}`
	if err := os.WriteFile(eventPath, []byte(prPayload), 0o600); err != nil {
		t.Fatalf("write event: %v", err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "acme/web")
	t.Setenv("GITHUB_ACTOR", "janedoe")
	t.Setenv("GITHUB_SHA", "9f2c1ab0")
	t.Setenv("GITHUB_REF_NAME", "feat/checkout-rework")
	t.Setenv("GITHUB_RUN_ID", "12345")
	t.Setenv("GITHUB_EVENT_PATH", eventPath)

	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Snapshot headers off the request before responding.
		captured = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"task_id": "abc-123",
			"status":  "queued",
		})
	}))
	defer server.Close()

	client := &Client{
		baseURL:        server.URL,
		apiKey:         "test-key",
		version:        "test",
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		maxRetries:     0,
		retryBaseDelay: time.Millisecond,
		retryMaxDelay:  time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.ExecuteWorkflow(ctx, &ExecuteWorkflowRequest{
		WorkflowID: "wf-abc",
		Retries:    1,
	})
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}
	if resp.TaskID != "abc-123" {
		t.Fatalf("task_id = %q, want abc-123", resp.TaskID)
	}

	want := map[string]string{
		"X-Revyl-Client":  "cli",
		"X-Ci-System":     "github-actions",
		"X-Ci-Commit-Sha": "9f2c1ab0",
		"X-Ci-Branch":     "feat/checkout-rework",
		"X-Ci-Run-Id":     "12345",
		"X-Ci-Repository": "acme/web",
		"X-Ci-Actor":      "janedoe",
		"X-Ci-Actor-Url":  "https://github.com/janedoe",
		"X-Ci-Run-Url":    "https://github.com/acme/web/actions/runs/12345",
		"X-Ci-Pr-Url":     "https://github.com/acme/web/pull/482",
		"X-Ci-Pr-Number":  "482",
	}
	for key, expected := range want {
		got := captured.Get(key)
		if got != expected {
			t.Errorf("header %s = %q, want %q", key, got, expected)
		}
	}

	if got := captured.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
}

// TestExecuteWorkflow_NoCIHeadersOutsideActions confirms manual CLI runs are
// completely unchanged — no X-CI-* headers leak into the request.
func TestExecuteWorkflow_NoCIHeadersOutsideActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")

	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"task_id": "abc-123"})
	}))
	defer server.Close()

	client := &Client{
		baseURL:        server.URL,
		apiKey:         "test-key",
		version:        "test",
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		maxRetries:     0,
		retryBaseDelay: time.Millisecond,
		retryMaxDelay:  time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.ExecuteWorkflow(ctx, &ExecuteWorkflowRequest{WorkflowID: "wf-abc"}); err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}

	for key := range captured {
		if len(key) >= 5 && (key[:5] == "X-Ci-" || key[:5] == "X-CI-") {
			t.Errorf("unexpected CI header outside Actions: %s = %q", key, captured.Get(key))
		}
	}
	// Sanity: X-Revyl-Client always sent.
	if got := captured.Get("X-Revyl-Client"); got != "cli" {
		t.Errorf("X-Revyl-Client = %q, want cli", got)
	}
}
