package hotreload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/revyl/cli/internal/api"
)

// Envelope kinds for the reverse (laptop -> device) data plane. These mirror
// the forward-mode kinds handled in relay.go, but flow the other direction:
// the CLI opens streams and the backend dials a port on the device.
const (
	reverseKindDialStart = "device.dial.start"
	reverseKindData      = "device.dial.data"
	reverseKindClose     = "device.dial.close"
	reverseKindError     = "stream.error"
)

// ReverseRelayTunnelBackend exposes a port on the cloud device (the Dart VM
// Service) as a local TCP address on the developer's machine, by proxying
// raw bytes over a backend-owned relay websocket.
//
// It is the mirror image of RelayTunnelBackend: instead of accepting
// device-initiated streams and dialing 127.0.0.1 on the laptop, it accepts
// laptop-initiated connections on a local listener and asks the backend to
// dial the device. The transport is protocol-agnostic (raw TCP), so it carries
// the VM Service's HTTP upgrade and websocket frames transparently — which is
// exactly what `flutter attach --debug-url` needs.
//
// NOTE: This requires backend support for reverse-mode relay sessions. See
// docs/developer_loop/flutter-hot-reload-design.md for the contract. Until the
// backend ships, StartReverse will fail at session creation; the type is wired
// and tested against the envelope protocol in isolation.
type ReverseRelayTunnelBackend struct {
	client   *api.Client
	provider string
	platform string

	mu         sync.Mutex
	session    *api.HotReloadRelaySession
	conn       *websocket.Conn
	listener   net.Listener
	localAddr  string
	devicePort int
	runCtx     context.Context
	cancel     context.CancelFunc
	stopped    bool
	onLog      func(string)

	writeMu sync.Mutex

	streamMu sync.Mutex
	streams  map[string]net.Conn
}

// NewReverseRelayTunnelBackend creates a reverse relay transport.
func NewReverseRelayTunnelBackend(client *api.Client, provider, platform string) *ReverseRelayTunnelBackend {
	return &ReverseRelayTunnelBackend{
		client:   client,
		provider: strings.TrimSpace(provider),
		platform: strings.TrimSpace(platform),
		streams:  make(map[string]net.Conn),
	}
}

// SetLogCallback registers a log callback.
func (r *ReverseRelayTunnelBackend) SetLogCallback(cb func(string)) {
	r.mu.Lock()
	r.onLog = cb
	r.mu.Unlock()
}

func (r *ReverseRelayTunnelBackend) log(format string, args ...interface{}) {
	r.mu.Lock()
	cb := r.onLog
	r.mu.Unlock()
	if cb != nil {
		cb(fmt.Sprintf(format, args...))
	}
}

// LocalAddr returns the current local proxy address, or "" if not running.
func (r *ReverseRelayTunnelBackend) LocalAddr() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.localAddr
}

// StartReverse provisions a reverse relay session, connects the CLI websocket,
// and opens a local TCP listener that proxies to devicePort on the device.
func (r *ReverseRelayTunnelBackend) StartReverse(ctx context.Context, devicePort int) (string, error) {
	if r.client == nil {
		return "", fmt.Errorf("reverse relay requires an authenticated API client")
	}
	if devicePort <= 0 {
		return "", fmt.Errorf("reverse relay requires a positive device port, got %d", devicePort)
	}

	session, err := r.client.CreateHotReloadRelay(ctx, api.HotReloadRelayCreateParams{
		Provider:   r.provider,
		Platform:   r.platform,
		Mode:       api.HotReloadRelayModeReverse,
		DevicePort: devicePort,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create reverse relay session: %w", err)
	}

	wsURL, err := session.ConnectWebSocketURL()
	if err != nil {
		return "", err
	}
	headers := http.Header{"User-Agent": []string{"revyl-cli-relay"}}
	if authHeader := session.ConnectAuthHeader(); authHeader != "" {
		headers.Set("Authorization", authHeader)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		_ = r.client.RevokeHotReloadRelay(context.Background(), session.RelayID)
		return "", fmt.Errorf("failed to connect reverse relay websocket: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = conn.Close()
		_ = r.client.RevokeHotReloadRelay(context.Background(), session.RelayID)
		return "", fmt.Errorf("failed to open local reverse listener: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)

	r.mu.Lock()
	r.session = session
	r.conn = conn
	r.listener = listener
	r.localAddr = listener.Addr().String()
	r.devicePort = devicePort
	r.runCtx = runCtx
	r.cancel = cancel
	r.stopped = false
	localAddr := r.localAddr
	r.mu.Unlock()

	r.log("[reverse-relay] session id=%s device_port=%d local=%s", session.RelayID, devicePort, localAddr)

	go r.acceptLoop(runCtx, listener)
	go r.readLoop(runCtx)

	return localAddr, nil
}

// acceptLoop accepts local connections (from `flutter attach`) and bridges each
// to the device through the relay.
func (r *ReverseRelayTunnelBackend) acceptLoop(ctx context.Context, listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			r.log("[reverse-relay] accept error: %v", err)
			return
		}
		go r.handleLocalConn(ctx, conn)
	}
}

func (r *ReverseRelayTunnelBackend) handleLocalConn(ctx context.Context, conn net.Conn) {
	streamID := uuid.NewString()

	r.streamMu.Lock()
	r.streams[streamID] = conn
	r.streamMu.Unlock()

	defer func() {
		r.streamMu.Lock()
		delete(r.streams, streamID)
		r.streamMu.Unlock()
		_ = conn.Close()
	}()

	if err := r.sendEnvelope(relayEnvelope{Kind: reverseKindDialStart, StreamID: streamID}); err != nil {
		r.log("[reverse-relay] failed to open device stream: %v", err)
		return
	}

	buf := make([]byte, relayChunkSize)
	for {
		n, readErr := conn.Read(buf)
		if n > 0 {
			chunk := base64.StdEncoding.EncodeToString(buf[:n])
			if err := r.sendEnvelope(relayEnvelope{
				Kind:         reverseKindData,
				StreamID:     streamID,
				BodyChunkB64: chunk,
			}); err != nil {
				return
			}
		}
		if readErr != nil {
			if readErr != io.EOF && ctx.Err() == nil {
				r.log("[reverse-relay] local read error: %v", readErr)
			}
			_ = r.sendEnvelope(relayEnvelope{Kind: reverseKindClose, StreamID: streamID})
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// readLoop pumps device->laptop bytes from the relay back into local conns.
func (r *ReverseRelayTunnelBackend) readLoop(ctx context.Context) {
	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()
	if conn == nil {
		return
	}
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				r.log("[reverse-relay] websocket disconnected: %v", err)
			}
			return
		}
		var env relayEnvelope
		if err := json.Unmarshal(message, &env); err != nil {
			r.log("[reverse-relay] failed to decode message: %v", err)
			continue
		}
		r.handleEnvelope(env)
	}
}

func (r *ReverseRelayTunnelBackend) handleEnvelope(env relayEnvelope) {
	switch env.Kind {
	case reverseKindData:
		r.streamMu.Lock()
		conn := r.streams[env.StreamID]
		r.streamMu.Unlock()
		if conn == nil {
			return
		}
		chunk, err := base64.StdEncoding.DecodeString(env.BodyChunkB64)
		if err != nil {
			_ = conn.Close()
			return
		}
		_, _ = conn.Write(chunk)
	case reverseKindClose, reverseKindError:
		r.streamMu.Lock()
		conn := r.streams[env.StreamID]
		delete(r.streams, env.StreamID)
		r.streamMu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
	case "ping":
		_ = r.sendEnvelope(relayEnvelope{Kind: "pong"})
	}
}

func (r *ReverseRelayTunnelBackend) sendEnvelope(env relayEnvelope) error {
	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("reverse relay websocket is not connected")
	}

	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to encode reverse relay message: %w", err)
	}

	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, payload)
}

// StopReverse tears down the local listener, websocket, and relay session.
func (r *ReverseRelayTunnelBackend) StopReverse() error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return nil
	}
	r.stopped = true
	cancel := r.cancel
	listener := r.listener
	conn := r.conn
	session := r.session
	r.listener = nil
	r.conn = nil
	r.session = nil
	r.localAddr = ""
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}

	r.streamMu.Lock()
	streams := r.streams
	r.streams = make(map[string]net.Conn)
	r.streamMu.Unlock()
	for _, c := range streams {
		_ = c.Close()
	}

	if session != nil && r.client != nil {
		if err := r.client.RevokeHotReloadRelay(context.Background(), session.RelayID); err != nil {
			return err
		}
	}
	return nil
}
