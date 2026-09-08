package hotreload

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

func TestRelayWebSocketProxy(t *testing.T) {
	testutil.TestWebSocketProxy(t, func(ctx context.Context, target string) error {
		backend := &RelayTunnelBackend{
			session: &api.HotReloadRelaySession{
				ConnectURL: target,
			},
			disconnects: make(chan error, 1),
			failures:    make(chan RuntimeFailure, 1),
		}
		for range 2 {
			if err := backend.connectRuntime(ctx, 8081); err != nil {
				return err
			}
			select {
			case <-backend.disconnects:
			case <-ctx.Done():
				backend.runtime.stop()
				return ctx.Err()
			}
			backend.runtime.stop()
		}
		return nil
	})
}

func TestRelayLocalWebSocketBypassesProxy(t *testing.T) {
	if os.Getenv("REVYL_TEST_SUBPROCESS") != t.Name() {
		testutil.RunInSubprocess(t, map[string]string{
			"HTTP_PROXY":  "http://127.0.0.1:1",
			"HTTPS_PROXY": "http://127.0.0.1:1",
		})
		return
	}
	upgrader := websocket.Upgrader{}
	connected := make(chan struct{}, 1)
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		t.Cleanup(func() { _ = conn.Close() })
		connected <- struct{}{}
	}))
	t.Cleanup(localServer.Close)
	_, portText, err := net.SplitHostPort(localServer.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := newRelayRuntimeTestWebSocket(t)
	t.Cleanup(func() { _ = serverConn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runtime := newRelayRuntime(ctx, port, clientConn, nil, nil, nil, nil)
	defer runtime.stop()
	runtime.handleWSStart(relayEnvelope{StreamID: "fixture-stream", Path: "/hot"})
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("local WebSocket did not bypass the unavailable proxy")
	}
}

func TestRelayWebSocketDirectFlow(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-relay-token" {
			t.Error("direct relay connection lost its authentication header")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteJSON(relayEnvelope{Kind: "ping"}); err != nil {
			t.Error(err)
			return
		}
		var pong relayEnvelope
		if err := conn.ReadJSON(&pong); err != nil {
			t.Error(err)
		} else if pong.Kind != "pong" {
			t.Error("relay ping did not receive a pong")
		}
	})
	testutil.TestDirectWebSocket(t, handler, func(ctx context.Context, target string) error {
		backend := &RelayTunnelBackend{
			session: &api.HotReloadRelaySession{
				ConnectURL:   target,
				ConnectToken: "fixture-relay-token",
			},
			disconnects: make(chan error, 1),
			failures:    make(chan RuntimeFailure, 1),
		}
		for range 2 {
			if err := backend.connectRuntime(ctx, 8081); err != nil {
				return err
			}
			select {
			case <-backend.disconnects:
			case <-ctx.Done():
				backend.runtime.stop()
				return ctx.Err()
			}
			backend.runtime.stop()
		}
		return nil
	})
}
