package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetRunConfigRetriesCopiesTheStoredRunConfig(t *testing.T) {
	storedExecutionMode := map[string]interface{}{"retries": float64(1), "enable_reflection": false}
	stored := map[string]interface{}{"execution_mode": storedExecutionMode, "fail_fast": true}
	workflow := &Workflow{RunConfig: stored, OverrideRunConfig: true}

	workflow.SetRunConfigRetries(4)

	if retries, ok := workflow.RunConfigRetries(); !ok || retries != 4 {
		t.Fatalf("RunConfigRetries() = %d, %v; want 4, true", retries, ok)
	}
	if storedExecutionMode["retries"] != float64(1) {
		t.Fatalf("stored execution_mode was mutated: %#v", storedExecutionMode)
	}
	update := workflow.RunConfigUpdate()
	if !update.OverrideRunConfig || update.RunConfig["fail_fast"] != true {
		t.Fatalf("RunConfigUpdate() = %#v, want the override flag and every stored key", update)
	}
	if update.RunConfig["execution_mode"].(map[string]interface{})["enable_reflection"] != false {
		t.Fatalf("execution_mode lost enable_reflection: %#v", update.RunConfig["execution_mode"])
	}
}

func TestWorkflowRunConfigWriteRetriesAreBounded(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":"request failed"}`))
			}))
			defer server.Close()
			client := NewClientWithBaseURL("test-key", server.URL)
			client.retryBaseDelay = time.Millisecond
			client.retryMaxDelay = time.Millisecond
			err := client.UpdateWorkflowRunConfig(context.Background(), "wf-001", UpdateWorkflowRunConfigRequest{RunConfig: map[string]interface{}{}})
			want := 1
			if status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests {
				want = DefaultMaxRetries + 1
			}
			if err == nil || attempts != want {
				t.Fatalf("err=%v attempts=%d want=%d", err, attempts, want)
			}
		})
	}
}

func TestWorkflowRunConfigWriteRetriesNetworkFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		_, _ = w.Write([]byte(`{"message":"saved"}`))
	}))
	defer server.Close()
	client := NewClientWithBaseURL("test-key", server.URL)
	client.retryBaseDelay = time.Millisecond
	err := client.UpdateWorkflowRunConfig(context.Background(), "wf-001", UpdateWorkflowRunConfigRequest{RunConfig: map[string]interface{}{}})
	if err != nil || attempts != 2 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestRunConfigUpdateSendsAnEmptyObjectWhenUnset(t *testing.T) {
	update := (&Workflow{}).RunConfigUpdate()
	if update.RunConfig == nil || len(update.RunConfig) != 0 {
		t.Fatalf("RunConfigUpdate().RunConfig = %#v, want an empty object", update.RunConfig)
	}
}

func TestUpdateWorkflowRunConfigRejectsNullRunConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	err := NewClientWithBaseURL("test-key", server.URL).UpdateWorkflowRunConfig(
		context.Background(),
		"wf-uuid-001",
		UpdateWorkflowRunConfigRequest{OverrideRunConfig: true},
	)
	if err == nil {
		t.Fatal("expected an error for a null run_config")
	}
}
