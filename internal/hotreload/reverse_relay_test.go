package hotreload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/revyl/cli/internal/api"
)

func TestReverseRelayStartRequiresClient(t *testing.T) {
	backend := NewReverseRelayTunnelBackend(nil, "flutter", "ios")
	if _, err := backend.StartReverse(context.Background(), 1234); err == nil {
		t.Fatal("StartReverse with nil client should error")
	}
}

func TestReverseRelayStartRequiresPositivePort(t *testing.T) {
	backend := NewReverseRelayTunnelBackend(&api.Client{}, "flutter", "ios")
	if _, err := backend.StartReverse(context.Background(), 0); err == nil {
		t.Fatal("StartReverse with non-positive device port should error")
	}
}

func TestReverseRelayStopBeforeStartIsNoop(t *testing.T) {
	backend := NewReverseRelayTunnelBackend(&api.Client{}, "flutter", "ios")
	if err := backend.StopReverse(); err != nil {
		t.Fatalf("StopReverse before start should be a no-op, got %v", err)
	}
	if addr := backend.LocalAddr(); addr != "" {
		t.Fatalf("LocalAddr should be empty before start, got %q", addr)
	}
}

// TestReverseRelayProxyRoundTrip stands up a fake backend that echoes device
// data back, then verifies bytes written to the local proxy address come back
// through the relay. This exercises the full CLI-side data plane without a real
// device.
func TestReverseRelayProxyRoundTrip(t *testing.T) {
	upgrader := websocket.Upgrader{}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/hotreload/relays", func(w http.ResponseWriter, r *http.Request) {
		session := api.HotReloadRelaySession{
			RelayID:      "relay-test",
			PublicURL:    "https://example.invalid",
			ConnectURL:   "ws://" + r.Host + "/relay-ws",
			ConnectToken: "token",
			Transport:    api.HotReloadRelayModeReverse,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(session)
	})
	mux.HandleFunc("/relay-ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var env relayEnvelope
			if err := json.Unmarshal(message, &env); err != nil {
				continue
			}
			// Echo device data back to the laptop, simulating the device.
			if env.Kind == reverseKindData {
				_ = conn.WriteJSON(relayEnvelope{
					Kind:         reverseKindData,
					StreamID:     env.StreamID,
					BodyChunkB64: env.BodyChunkB64,
				})
			}
		}
	})
	mux.HandleFunc("/api/v1/hotreload/relays/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	backend := NewReverseRelayTunnelBackend(client, "flutter", "ios")

	localAddr, err := backend.StartReverse(context.Background(), 8181)
	if err != nil {
		t.Fatalf("StartReverse error: %v", err)
	}
	defer backend.StopReverse()

	if backend.LocalAddr() != localAddr {
		t.Fatalf("LocalAddr mismatch: %q vs %q", backend.LocalAddr(), localAddr)
	}

	conn, err := net.DialTimeout("tcp", localAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial local proxy: %v", err)
	}
	defer conn.Close()

	payload := []byte("vm-service-handshake")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write to proxy: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, len(payload))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read echoed bytes: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("round-trip mismatch: got %q want %q", string(buf[:n]), string(payload))
	}
}

func TestReverseRelayEnvelopeEncoding(t *testing.T) {
	// Guard against accidental field-tag changes that would break the wire format.
	env := relayEnvelope{Kind: reverseKindData, StreamID: "s1", BodyChunkB64: base64.StdEncoding.EncodeToString([]byte("x"))}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded relayEnvelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Kind != reverseKindData || decoded.StreamID != "s1" {
		t.Fatalf("round-trip decode mismatch: %+v", decoded)
	}
}
