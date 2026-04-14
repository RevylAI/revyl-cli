package hotreload

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestCheckRelayConnectivity_UsesHealthCheckEndpoint(t *testing.T) {
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		if r.URL.Path != "/health_check" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("", srv.URL)

	if err := CheckRelayConnectivity(context.Background(), client); err != nil {
		t.Fatalf("CheckRelayConnectivity() error = %v", err)
	}
	if requestedPath != "/health_check" {
		t.Fatalf("requested path = %q, want /health_check", requestedPath)
	}
}

func TestCheckRelayConnectivity_FailsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("", srv.URL)

	if err := CheckRelayConnectivity(context.Background(), client); err == nil {
		t.Fatal("CheckRelayConnectivity() expected error on 502 response")
	}
}

func TestCreateRelaySessionUsesBackendControlPlane(t *testing.T) {
	var authorization string
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hotreload/relays" || r.Method != http.MethodPost {
			t.Fatalf("unexpected backend request: %s %s", r.Method, r.URL.Path)
		}
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"relay_id":"a-123abc",
			"public_url":"https://hr-a-123abc-public.relay-a.revyl.ai",
			"connect_url":"wss://relay-a.revyl.ai/api/v1/hotreload/relays/a-123abc/connect",
			"connect_token":"connect-token",
			"transport":"relay",
			"expires_at":"2026-04-10T12:00:00Z"
		}`)
	}))
	defer backendServer.Close()

	backendClient := api.NewClientWithBaseURL("backend-token", backendServer.URL)
	relayBackend := &RelayTunnelBackend{
		client:      backendClient,
		provider:    "expo",
		disconnects: make(chan error, 1),
	}

	session, err := relayBackend.createRelaySession(context.Background())
	if err != nil {
		t.Fatalf("createRelaySession() error = %v", err)
	}
	if session.RelayID != "a-123abc" {
		t.Fatalf("RelayID = %q, want a-123abc", session.RelayID)
	}
	if authorization != "Bearer backend-token" {
		t.Fatalf("Authorization header = %q, want Bearer backend-token", authorization)
	}
}
