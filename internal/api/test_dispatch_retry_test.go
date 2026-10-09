package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestExecuteTestDoesNotRetryUncertainDispatch(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/api/execute_test_id_async" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, err := NewClientWithBaseURL("fixture", server.URL).ExecuteTest(context.Background(), &ExecuteTestRequest{TestID: "00000000-0000-4000-8000-000000000001"})
	if err == nil {
		t.Fatal("expected uncertain dispatch error")
	}
	if attempts.Load() != 1 {
		t.Fatalf("dispatched %d times", attempts.Load())
	}
}
