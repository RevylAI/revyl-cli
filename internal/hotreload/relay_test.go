package hotreload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestRelayTunnelBackendReacquireCreatesReplacementSession(t *testing.T) {
	var createCalls int32
	var connectCalls int32
	var revokedOld int32
	connectObserved := make(chan struct{}, 1)

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	var backendServer *httptest.Server
	backendServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/hotreload/relays" && r.Method == http.MethodPost:
			call := atomic.AddInt32(&createCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"relay_id":"a-new-%d",
				"public_url":"https://hr-a-new-%d.relay.example",
				"connect_url":"%s/api/v1/hotreload/relays/a-new-%d/connect",
				"connect_token":"connect-token-%d",
				"transport":"relay",
				"expires_at":"2026-04-10T12:00:00Z"
			}`,
				call,
				call,
				"ws"+strings.TrimPrefix(backendServer.URL, "http"),
				call,
				call,
			)
		case strings.HasSuffix(r.URL.Path, "/connect"):
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("websocket upgrade: %v", err)
				return
			}
			atomic.AddInt32(&connectCalls, 1)
			select {
			case connectObserved <- struct{}{}:
			default:
			}
			t.Cleanup(func() { _ = conn.Close() })
		case r.URL.Path == "/api/v1/hotreload/relays/a-old" && r.Method == http.MethodDelete:
			atomic.AddInt32(&revokedOld, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"relay_id":"a-old","revoked":true}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/hotreload/relays/a-new-") && r.Method == http.MethodDelete:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"relay_id":"a-new-1","revoked":true}`))
		default:
			t.Errorf("unexpected backend request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backendServer.Close)

	wsBase := "ws" + strings.TrimPrefix(backendServer.URL, "http")
	relayBackend := &RelayTunnelBackend{
		client: api.NewClientWithBaseURL("backend-token", backendServer.URL),
		session: &api.HotReloadRelaySession{
			RelayID:      "a-old",
			PublicURL:    "https://hr-a-old.relay.example",
			ConnectURL:   wsBase + "/api/v1/hotreload/relays/a-old/connect",
			ConnectToken: "old-token",
			Transport:    "relay",
		},
		localPort:   freePort(t),
		runCtx:      context.Background(),
		failures:    make(chan RuntimeFailure, 1),
		disconnects: make(chan error, 1),
	}

	result, err := relayBackend.Reacquire(context.Background())
	if err != nil {
		t.Fatalf("Reacquire() error = %v", err)
	}
	if result.RelayID != "a-new-1" {
		t.Fatalf("RelayID = %q, want a-new-1", result.RelayID)
	}
	select {
	case <-connectObserved:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement relay connection")
	}
	if got := atomic.LoadInt32(&connectCalls); got != 1 {
		t.Fatalf("connect calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&revokedOld); got != 1 {
		t.Fatalf("old revokes = %d, want 1", got)
	}
	if relayBackend.Metadata().RelayID != "a-new-1" {
		t.Fatalf("Metadata().RelayID = %q, want a-new-1", relayBackend.Metadata().RelayID)
	}
	_ = relayBackend.Stop()
}

func TestRelayTunnelBackendReacquireRestartsHeartbeatMonitor(t *testing.T) {
	withRelayTestTimings(t, 10*time.Millisecond, 5*time.Millisecond, 25*time.Millisecond, 3)

	var oldRevoked int32
	var newHeartbeats int32
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	var backendServer *httptest.Server
	backendServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/hotreload/relays" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"relay_id":"a-new-1",
				"public_url":"https://hr-a-new-1.relay.example",
				"connect_url":"%s/api/v1/hotreload/relays/a-new-1/connect",
				"connect_token":"connect-token",
				"transport":"relay",
				"expires_at":"2026-04-10T12:00:00Z"
			}`, "ws"+strings.TrimPrefix(backendServer.URL, "http"))
		case strings.HasSuffix(r.URL.Path, "/connect"):
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("websocket upgrade: %v", err)
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		case r.URL.Path == "/api/v1/hotreload/relays/a-old/heartbeat" && r.Method == http.MethodPost:
			if atomic.LoadInt32(&oldRevoked) != 0 {
				http.Error(w, "Hot reload relay not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"relay_id":"a-old","expires_at":%q,"active":true}`, time.Now().Add(time.Minute).Format(time.RFC3339))
		case r.URL.Path == "/api/v1/hotreload/relays/a-new-1/heartbeat" && r.Method == http.MethodPost:
			atomic.AddInt32(&newHeartbeats, 1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"relay_id":"a-new-1","expires_at":%q,"active":true}`, time.Now().Add(time.Minute).Format(time.RFC3339))
		case r.URL.Path == "/api/v1/hotreload/relays/a-old" && r.Method == http.MethodDelete:
			atomic.StoreInt32(&oldRevoked, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"relay_id":"a-old","revoked":true}`))
		case r.URL.Path == "/api/v1/hotreload/relays/a-new-1" && r.Method == http.MethodDelete:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"relay_id":"a-new-1","revoked":true}`))
		default:
			t.Errorf("unexpected backend request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backendServer.Close)

	relayBackend := &RelayTunnelBackend{
		client: api.NewClientWithBaseURL("backend-token", backendServer.URL),
		session: &api.HotReloadRelaySession{
			RelayID:   "a-old",
			PublicURL: "https://hr-a-old.relay.example",
			Transport: "relay",
		},
		localPort:   freePort(t),
		runCtx:      context.Background(),
		failures:    make(chan RuntimeFailure, 4),
		disconnects: make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	relayBackend.StartHealthMonitor(ctx)

	if _, err := relayBackend.Reacquire(context.Background()); err != nil {
		t.Fatalf("Reacquire() error = %v", err)
	}
	deadline := time.After(time.Second)
	for atomic.LoadInt32(&newHeartbeats) == 0 {
		select {
		case <-deadline:
			t.Fatal("replacement heartbeat monitor did not heartbeat new relay")
		case <-time.After(5 * time.Millisecond):
		}
	}
	assertNoRuntimeFailure(t, relayBackend.Failures())
	_ = relayBackend.Stop()
}

func TestRelayRuntimeStopDoesNotReportDisconnect(t *testing.T) {
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

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn := <-serverConnCh
	t.Cleanup(func() { _ = serverConn.Close() })

	disconnects := make(chan error, 1)
	runtime := newRelayRuntime(context.Background(), freePort(t), clientConn, nil, nil, func(err error) {
		disconnects <- err
	}, nil)
	runtime.start()
	runtime.stop()

	select {
	case err := <-disconnects:
		t.Fatalf("intentional stop reported disconnect: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRelayRuntimeRemoteCloseReportsDisconnect(t *testing.T) {
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

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn := <-serverConnCh
	t.Cleanup(func() { _ = serverConn.Close() })

	disconnects := make(chan error, 1)
	runtime := newRelayRuntime(context.Background(), freePort(t), clientConn, nil, nil, func(err error) {
		disconnects <- err
	}, nil)
	runtime.start()
	_ = serverConn.Close()

	select {
	case <-disconnects:
	case <-time.After(time.Second):
		t.Fatal("remote close did not report disconnect")
	}
	runtime.stop()
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
	runtime := newRelayRuntime(context.Background(), portInt, clientConn, traceClient, nil, nil, nil)
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

func TestRelayHeartbeatNotFoundEmitsLeaseExpiredFailure(t *testing.T) {
	withRelayTestTimings(t, 10*time.Millisecond, 5*time.Millisecond, 25*time.Millisecond, 3)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Hot reload relay not found"}`))
	}))
	t.Cleanup(backendServer.Close)

	relayBackend := &RelayTunnelBackend{
		client:      api.NewClientWithBaseURL("", backendServer.URL),
		session:     &api.HotReloadRelaySession{RelayID: "a-test"},
		failures:    make(chan RuntimeFailure, 1),
		disconnects: make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go relayBackend.heartbeatLoop(ctx)

	failure := readRuntimeFailure(t, relayBackend.Failures())
	if failure.Kind != RuntimeFailureRelayLeaseExpired {
		t.Fatalf("failure kind = %q, want %q", failure.Kind, RuntimeFailureRelayLeaseExpired)
	}
	if !failure.Fatal {
		t.Fatal("expected lease expiry to be fatal")
	}
}

func TestRelayHeartbeatBudgetEmitsUnreachableFailure(t *testing.T) {
	withRelayTestTimings(t, 10*time.Millisecond, 5*time.Millisecond, 25*time.Millisecond, 5)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"relay unavailable"}`))
	}))
	t.Cleanup(backendServer.Close)

	relayBackend := &RelayTunnelBackend{
		client:      api.NewClientWithBaseURL("", backendServer.URL),
		session:     &api.HotReloadRelaySession{RelayID: "a-test"},
		failures:    make(chan RuntimeFailure, 1),
		disconnects: make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go relayBackend.heartbeatLoop(ctx)

	failure := readRuntimeFailure(t, relayBackend.Failures())
	if failure.Kind != RuntimeFailureRelayUnreachable {
		t.Fatalf("failure kind = %q, want %q", failure.Kind, RuntimeFailureRelayUnreachable)
	}
	if !failure.Fatal {
		t.Fatal("expected relay unreachable to be fatal")
	}
}

func TestRelayReconnectBudgetEmitsUnreachableFailure(t *testing.T) {
	withRelayTestTimings(t, time.Second, 5*time.Millisecond, 18*time.Millisecond, 5)

	relayBackend := &RelayTunnelBackend{
		session: &api.HotReloadRelaySession{
			RelayID:      "a-test",
			ConnectURL:   "ws://127.0.0.1:1/connect",
			ConnectToken: "token",
		},
		localPort:   8081,
		failures:    make(chan RuntimeFailure, 1),
		disconnects: make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go relayBackend.reconnectLoop(ctx)

	relayBackend.disconnects <- fmt.Errorf("relay websocket disconnected")

	failure := readRuntimeFailure(t, relayBackend.Failures())
	if failure.Kind != RuntimeFailureRelayUnreachable {
		t.Fatalf("failure kind = %q, want %q", failure.Kind, RuntimeFailureRelayUnreachable)
	}
}

func TestRelayReconnectSkipsStaleSessionAfterReacquire(t *testing.T) {
	withRelayTestTimings(t, time.Second, 25*time.Millisecond, 60*time.Millisecond, 5)

	logs := make(chan string, 8)
	relayBackend := &RelayTunnelBackend{
		session: &api.HotReloadRelaySession{
			RelayID:      "a-old",
			ConnectURL:   "ws://127.0.0.1:1/connect",
			ConnectToken: "old-token",
		},
		localPort:   8081,
		failures:    make(chan RuntimeFailure, 1),
		disconnects: make(chan error, 1),
		onLog: func(message string) {
			select {
			case logs <- message:
			default:
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go relayBackend.reconnectLoop(ctx)

	relayBackend.disconnects <- fmt.Errorf("relay websocket disconnected")
	for {
		select {
		case logLine := <-logs:
			if strings.Contains(logLine, "connection lost") {
				goto capturedOldSession
			}
		case <-time.After(time.Second):
			t.Fatal("reconnect loop did not capture the original disconnect")
		}
	}

capturedOldSession:
	relayBackend.mu.Lock()
	relayBackend.session = &api.HotReloadRelaySession{
		RelayID:      "a-new",
		ConnectURL:   "ws://127.0.0.1:1/connect",
		ConnectToken: "new-token",
	}
	relayBackend.mu.Unlock()

	time.Sleep(90 * time.Millisecond)
	assertNoRuntimeFailure(t, relayBackend.Failures())
}

func TestRelayRuntimeClassifiesLocalWebSocketConnectionRefused(t *testing.T) {
	clientConn, serverConn := newRelayRuntimeTestWebSocket(t)
	failures := make(chan RuntimeFailure, 1)
	runtime := newRelayRuntime(context.Background(), freePort(t), clientConn, nil, nil, nil, func(f RuntimeFailure) {
		failures <- f
	})
	t.Cleanup(runtime.stop)
	t.Cleanup(func() { _ = serverConn.Close() })

	runtime.handleWSStart(relayEnvelope{
		Kind:     "websocket.start",
		StreamID: "ws-1",
		Path:     "/expo-dev-plugins/broadcast",
	})

	failure := readRuntimeFailure(t, failures)
	if failure.Kind != RuntimeFailureLocalDevServerDown {
		t.Fatalf("failure kind = %q, want %q", failure.Kind, RuntimeFailureLocalDevServerDown)
	}
	if !failure.Fatal {
		t.Fatal("expected local websocket refusal to be fatal")
	}
}

func TestRelayRuntimePreservesMediaRangeHeaders(t *testing.T) {
	clientConn, serverConn := newRelayRuntimeTestWebSocket(t)
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assets/auth-start.mp4" {
			t.Fatalf("local path = %q, want /assets/auth-start.mp4", r.URL.Path)
		}
		if got := r.Header.Get("Range"); got != "bytes=0-1" {
			t.Fatalf("Range = %q, want bytes=0-1", got)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", "2")
		w.Header().Set("Content-Range", "bytes 0-1/4")
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("ab"))
	}))
	t.Cleanup(localServer.Close)
	req, err := http.NewRequest(http.MethodGet, localServer.URL, nil)
	if err != nil {
		t.Fatalf("parse local URL: %v", err)
	}
	_, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	var portInt int
	if _, err := fmt.Sscanf(port, "%d", &portInt); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}

	runtime := newRelayRuntime(context.Background(), portInt, clientConn, nil, nil, nil, nil)
	t.Cleanup(runtime.stop)
	t.Cleanup(func() { _ = serverConn.Close() })

	runtime.handleHTTPRequestStart(relayEnvelope{
		Kind:         "http.request.start",
		StreamID:     "media-1",
		Method:       http.MethodGet,
		Path:         "/assets/auth-start.mp4",
		Headers:      map[string][]string{"Range": {"bytes=0-1"}, "Content-Length": {"99"}},
		RequestClass: "asset",
	})
	runtime.handleHTTPRequestEnd(relayEnvelope{Kind: "http.request.end", StreamID: "media-1"})

	var start relayEnvelope
	var body []byte
	for {
		_, payload, err := serverConn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage() error = %v", err)
		}
		var env relayEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatalf("decode relay envelope: %v", err)
		}
		switch env.Kind {
		case "http.response.start":
			start = env
		case "http.response.body":
			chunk, err := base64.StdEncoding.DecodeString(env.BodyChunkB64)
			if err != nil {
				t.Fatalf("decode body chunk: %v", err)
			}
			body = append(body, chunk...)
		case "http.response.end":
			headers := http.Header(start.Headers)
			if start.Status != http.StatusPartialContent {
				t.Fatalf("response status = %d, want %d", start.Status, http.StatusPartialContent)
			}
			if got := headers.Get("Content-Length"); got != "2" {
				t.Fatalf("Content-Length = %q, want 2", got)
			}
			if got := headers.Get("Content-Range"); got != "bytes 0-1/4" {
				t.Fatalf("Content-Range = %q, want bytes 0-1/4", got)
			}
			if got := headers.Get("Accept-Ranges"); got != "bytes" {
				t.Fatalf("Accept-Ranges = %q, want bytes", got)
			}
			if got := headers.Get("Content-Type"); got != "video/mp4" {
				t.Fatalf("Content-Type = %q, want video/mp4", got)
			}
			if string(body) != "ab" {
				t.Fatalf("body = %q, want ab", string(body))
			}
			return
		}
	}
}

func TestRelayRuntimePreservesMediaHeadHeadersWithoutBody(t *testing.T) {
	clientConn, serverConn := newRelayRuntimeTestWebSocket(t)
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assets/auth-start.mp4" {
			t.Fatalf("local path = %q, want /assets/auth-start.mp4", r.URL.Path)
		}
		if r.Method != http.MethodHead {
			t.Fatalf("method = %q, want HEAD", r.Method)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", "4096")
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(localServer.Close)
	req, err := http.NewRequest(http.MethodGet, localServer.URL, nil)
	if err != nil {
		t.Fatalf("parse local URL: %v", err)
	}
	_, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	var portInt int
	if _, err := fmt.Sscanf(port, "%d", &portInt); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}

	runtime := newRelayRuntime(context.Background(), portInt, clientConn, nil, nil, nil, nil)
	t.Cleanup(runtime.stop)
	t.Cleanup(func() { _ = serverConn.Close() })

	runtime.handleHTTPRequestStart(relayEnvelope{
		Kind:         "http.request.start",
		StreamID:     "media-head-1",
		Method:       http.MethodHead,
		Path:         "/assets/auth-start.mp4",
		RequestClass: "asset",
	})
	runtime.handleHTTPRequestEnd(relayEnvelope{Kind: "http.request.end", StreamID: "media-head-1"})

	var start relayEnvelope
	var body []byte
	for {
		_, payload, err := serverConn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage() error = %v", err)
		}
		var env relayEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatalf("decode relay envelope: %v", err)
		}
		switch env.Kind {
		case "http.response.start":
			start = env
		case "http.response.body":
			chunk, err := base64.StdEncoding.DecodeString(env.BodyChunkB64)
			if err != nil {
				t.Fatalf("decode body chunk: %v", err)
			}
			body = append(body, chunk...)
		case "http.response.end":
			headers := http.Header(start.Headers)
			if start.Status != http.StatusOK {
				t.Fatalf("response status = %d, want %d", start.Status, http.StatusOK)
			}
			if got := headers.Get("Content-Length"); got != "4096" {
				t.Fatalf("Content-Length = %q, want 4096", got)
			}
			if got := headers.Get("Accept-Ranges"); got != "bytes" {
				t.Fatalf("Accept-Ranges = %q, want bytes", got)
			}
			if got := headers.Get("Content-Type"); got != "video/mp4" {
				t.Fatalf("Content-Type = %q, want video/mp4", got)
			}
			if len(body) != 0 {
				t.Fatalf("HEAD body length = %d, want 0", len(body))
			}
			return
		}
	}
}

func TestRelayRuntimeStripsBundleContentLength(t *testing.T) {
	clientConn, serverConn := newRelayRuntimeTestWebSocket(t)
	body := []byte("console.log('ok');")
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.bundle" {
			t.Fatalf("local path = %q, want /index.bundle", r.URL.Path)
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(localServer.Close)
	req, err := http.NewRequest(http.MethodGet, localServer.URL, nil)
	if err != nil {
		t.Fatalf("parse local URL: %v", err)
	}
	_, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	var portInt int
	if _, err := fmt.Sscanf(port, "%d", &portInt); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}

	runtime := newRelayRuntime(context.Background(), portInt, clientConn, nil, nil, nil, nil)
	t.Cleanup(runtime.stop)
	t.Cleanup(func() { _ = serverConn.Close() })

	runtime.handleHTTPRequestStart(relayEnvelope{
		Kind:         "http.request.start",
		StreamID:     "bundle-1",
		Method:       http.MethodGet,
		Path:         "/index.bundle",
		RequestClass: "bundle",
	})
	runtime.handleHTTPRequestEnd(relayEnvelope{Kind: "http.request.end", StreamID: "bundle-1"})

	for {
		_, payload, err := serverConn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage() error = %v", err)
		}
		var env relayEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatalf("decode relay envelope: %v", err)
		}
		if env.Kind == "http.response.start" {
			headers := http.Header(env.Headers)
			if got := headers.Get("Content-Length"); got != "" {
				t.Fatalf("Content-Length = %q, want stripped", got)
			}
			if got := headers.Get("Content-Type"); got != "application/javascript" {
				t.Fatalf("Content-Type = %q, want application/javascript", got)
			}
			return
		}
	}
}

func TestRelayRuntimeClassifiesMetro500AsNonFatal(t *testing.T) {
	clientConn, serverConn := newRelayRuntimeTestWebSocket(t)
	failures := make(chan RuntimeFailure, 1)
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("metro exploded"))
	}))
	t.Cleanup(localServer.Close)
	req, err := http.NewRequest(http.MethodGet, localServer.URL, nil)
	if err != nil {
		t.Fatalf("parse local URL: %v", err)
	}
	_, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	var portInt int
	if _, err := fmt.Sscanf(port, "%d", &portInt); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}

	runtime := newRelayRuntime(context.Background(), portInt, clientConn, nil, nil, nil, func(f RuntimeFailure) {
		failures <- f
	})
	t.Cleanup(runtime.stop)
	t.Cleanup(func() { _ = serverConn.Close() })

	runtime.handleHTTPRequestStart(relayEnvelope{
		Kind:     "http.request.start",
		StreamID: "http-1",
		Method:   http.MethodGet,
		Path:     "/index.bundle",
	})
	runtime.handleHTTPRequestEnd(relayEnvelope{Kind: "http.request.end", StreamID: "http-1"})

	failure := readRuntimeFailure(t, failures)
	if failure.Kind != RuntimeFailureMetro500 {
		t.Fatalf("failure kind = %q, want %q", failure.Kind, RuntimeFailureMetro500)
	}
	if failure.Fatal {
		t.Fatal("expected Metro 500 to be non-fatal")
	}
}

func withRelayTestTimings(t *testing.T, heartbeatEvery time.Duration, reconnectBackoff time.Duration, reconnectTimeout time.Duration, heartbeatFailures int) {
	t.Helper()
	oldHeartbeatEvery := relayHeartbeatEvery
	oldReconnectBackoff := relayReconnectBackoff
	oldReconnectTimeout := relayReconnectFailureTimeout
	oldHeartbeatFailures := relayHeartbeatFailuresBeforeFatal
	relayHeartbeatEvery = heartbeatEvery
	relayReconnectBackoff = reconnectBackoff
	relayReconnectFailureTimeout = reconnectTimeout
	relayHeartbeatFailuresBeforeFatal = heartbeatFailures
	t.Cleanup(func() {
		relayHeartbeatEvery = oldHeartbeatEvery
		relayReconnectBackoff = oldReconnectBackoff
		relayReconnectFailureTimeout = oldReconnectTimeout
		relayHeartbeatFailuresBeforeFatal = oldHeartbeatFailures
	})
}

func readRuntimeFailure(t *testing.T, failures <-chan RuntimeFailure) RuntimeFailure {
	t.Helper()
	select {
	case failure := <-failures:
		return failure
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime failure")
	}
	return RuntimeFailure{}
}

func newRelayRuntimeTestWebSocket(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
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

	select {
	case serverConn := <-serverConnCh:
		return clientConn, serverConn
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for relay runtime websocket")
	}
	return nil, nil
}
