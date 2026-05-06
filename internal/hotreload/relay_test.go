package hotreload

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

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

func TestRelayRuntimeExportsLocalMetroSpanFromEnvelopeTraceparent(t *testing.T) {
	spanCh := make(chan *tracepb.TracesData, 1)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/telemetry/cli-spans" {
			t.Fatalf("unexpected telemetry path: %s", r.URL.Path)
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read telemetry body: %v", err)
		}
		export := &tracepb.TracesData{}
		if err := proto.Unmarshal(payload, export); err != nil {
			t.Fatalf("decode telemetry body: %v", err)
		}
		spanCh <- export
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(apiServer.Close)

	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.bundle" {
			t.Fatalf("local path = %q, want /index.bundle", r.URL.Path)
		}
		_, _ = w.Write([]byte("console.log('ok');"))
	}))
	t.Cleanup(localServer.Close)
	localURL, err := http.NewRequest(http.MethodGet, localServer.URL, nil)
	if err != nil {
		t.Fatalf("parse local URL: %v", err)
	}
	_, port, err := net.SplitHostPort(localURL.URL.Host)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	serverConnCh := make(chan *websocket.Conn, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("websocket upgrade: %v", err)
		}
		serverConnCh <- conn
	}))
	t.Cleanup(wsServer.Close)
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })
	serverConn := <-serverConnCh
	t.Cleanup(func() { _ = serverConn.Close() })

	portInt := 0
	if _, err := fmt.Sscanf(port, "%d", &portInt); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}
	traceClient := api.NewClientWithBaseURL("api-key", apiServer.URL)
	runtime := newRelayRuntime(context.Background(), portInt, clientConn, traceClient, nil, nil)
	t.Cleanup(runtime.stop)

	streamID := "stream-1"
	runtime.handleHTTPRequestStart(relayEnvelope{
		Kind:         "http.request.start",
		StreamID:     streamID,
		Method:       http.MethodGet,
		Path:         "/index.bundle",
		Query:        "platform=ios",
		Traceparent:  "00-1234567890abcdef1234567890abcdef-1111111111111111-01",
		RequestClass: "bundle",
	})
	runtime.handleHTTPRequestEnd(relayEnvelope{Kind: "http.request.end", StreamID: streamID})

	for {
		_, payload, err := serverConn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage() error = %v", err)
		}
		var env relayEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatalf("decode relay envelope: %v", err)
		}
		if env.Kind == "http.response.end" {
			break
		}
	}

	select {
	case export := <-spanCh:
		span := export.ResourceSpans[0].ScopeSpans[0].Spans[0]
		if span.Name != "CLI: hotreload.local_metro_request" {
			t.Fatalf("span name = %q", span.Name)
		}
		if got := fmt.Sprintf("%x", span.TraceId); got != "1234567890abcdef1234567890abcdef" {
			t.Fatalf("trace ID = %q", got)
		}
		if got := fmt.Sprintf("%x", span.ParentSpanId); got != "1111111111111111" {
			t.Fatalf("parent span ID = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for local Metro span export")
	}
}
