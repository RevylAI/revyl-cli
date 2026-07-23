package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientForwardsCloudAgentConversationContext(t *testing.T) {
	t.Setenv("CURSOR_CONVERSATION_ID", "bc_123-abc")

	var receivedProvider string
	var receivedConversationID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		receivedProvider = request.Header.Get("X-Revyl-Cloud-Agent-Provider")
		receivedConversationID = request.Header.Get(
			"X-Revyl-Cloud-Agent-Provider-Conversation-Id",
		)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	response, err := client.doRequestOnce(
		context.Background(),
		http.MethodGet,
		"/api/v1/apps",
		nil,
	)
	if err != nil {
		t.Fatalf("backend request error = %v", err)
	}
	response.Body.Close()

	if receivedProvider != "cursor_cloud" {
		t.Fatalf(
			"X-Revyl-Cloud-Agent-Provider = %q, want %q",
			receivedProvider,
			"cursor_cloud",
		)
	}
	if receivedConversationID != "bc_123-abc" {
		t.Fatalf(
			"X-Revyl-Cloud-Agent-Provider-Conversation-Id = %q, want %q",
			receivedConversationID,
			"bc_123-abc",
		)
	}
}
