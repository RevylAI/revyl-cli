package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRevylAgentRequestAttribution(t *testing.T) {
	t.Setenv("REVYL_AGENT", "revyl")
	t.Setenv("CODEX_THREAD_ID", "outer-agent-session")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Revyl-Agent"); got != "revyl" {
			t.Errorf("agent header = %q, want revyl", got)
		}
		if got := r.Header.Get("X-Revyl-Agent-Session-Id"); got != "" {
			t.Errorf("inherited unrelated agent session: %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Error("agent attribution changed authentication")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewClientWithBaseURL("test-key", server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.doRequest(ctx, http.MethodPost, "/annotation-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
}
