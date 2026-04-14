package hotreload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/revyl/cli/internal/api"
)

const (
	relayChunkSize        = 32 * 1024
	relayHeartbeatEvery   = 30 * time.Second
	relayReconnectBackoff = 2 * time.Second
)

var relayHopByHopHeaders = map[string]bool{
	"connection":               true,
	"keep-alive":               true,
	"proxy-authenticate":       true,
	"proxy-authorization":      true,
	"te":                       true,
	"trailer":                  true,
	"transfer-encoding":        true,
	"upgrade":                  true,
	"host":                     true,
	"content-length":           true,
	"sec-websocket-accept":     true,
	"sec-websocket-extensions": true,
	"sec-websocket-key":        true,
	"sec-websocket-version":    true,
	"sec-websocket-protocol":   true,
}

// CheckRelayConnectivity validates that the backend relay control plane is reachable.
func CheckRelayConnectivity(ctx context.Context, apiClient *api.Client) error {
	if apiClient == nil {
		return fmt.Errorf("relay requires an authenticated API client")
	}

	healthURL := strings.TrimRight(apiClient.BaseURL(), "/") + "/health_check"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create relay health check request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Revyl backend relay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("relay health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

type relayEnvelope struct {
	Kind         string              `json:"kind"`
	StreamID     string              `json:"stream_id,omitempty"`
	Method       string              `json:"method,omitempty"`
	Path         string              `json:"path,omitempty"`
	Query        string              `json:"query,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Status       int                 `json:"status,omitempty"`
	Message      string              `json:"message,omitempty"`
	BodyChunkB64 string              `json:"body_chunk_b64,omitempty"`
	Text         string              `json:"text,omitempty"`
	Binary       bool                `json:"binary,omitempty"`
	CloseCode    int                 `json:"close_code,omitempty"`
}

type relayHTTPStream struct {
	bodyWriter *io.PipeWriter
	cancel     context.CancelFunc
}

type relayWSStream struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type relayRuntime struct {
	ctx        context.Context
	cancel     context.CancelFunc
	localPort  int
	conn       *websocket.Conn
	httpClient *http.Client
	onLog      func(string)

	writeMu  sync.Mutex
	streamMu sync.Mutex

	httpStreams map[string]*relayHTTPStream
	wsStreams   map[string]*relayWSStream

	disconnectOnce sync.Once
	onDisconnect   func(error)
}

func newRelayRuntime(
	parent context.Context,
	localPort int,
	conn *websocket.Conn,
	onLog func(string),
	onDisconnect func(error),
) *relayRuntime {
	ctx, cancel := context.WithCancel(parent)
	return &relayRuntime{
		ctx:          ctx,
		cancel:       cancel,
		localPort:    localPort,
		conn:         conn,
		onLog:        onLog,
		onDisconnect: onDisconnect,
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  false,
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
		httpStreams: make(map[string]*relayHTTPStream),
		wsStreams:   make(map[string]*relayWSStream),
	}
}

func (r *relayRuntime) log(format string, args ...interface{}) {
	if r.onLog != nil {
		r.onLog(fmt.Sprintf(format, args...))
	}
}

func (r *relayRuntime) start() {
	go r.readLoop()
}

func (r *relayRuntime) stop() {
	r.cancel()
	_ = r.conn.Close()

	r.streamMu.Lock()
	httpStreams := r.httpStreams
	wsStreams := r.wsStreams
	r.httpStreams = make(map[string]*relayHTTPStream)
	r.wsStreams = make(map[string]*relayWSStream)
	r.streamMu.Unlock()

	for _, stream := range httpStreams {
		stream.cancel()
		_ = stream.bodyWriter.Close()
	}
	for _, stream := range wsStreams {
		stream.mu.Lock()
		_ = stream.conn.Close()
		stream.mu.Unlock()
	}
}

func (r *relayRuntime) readLoop() {
	defer r.signalDisconnect(fmt.Errorf("relay websocket disconnected"))
	for {
		_, message, err := r.conn.ReadMessage()
		if err != nil {
			return
		}
		var env relayEnvelope
		if err := json.Unmarshal(message, &env); err != nil {
			r.log("[relay] failed to decode upstream message: %v", err)
			continue
		}
		r.handleEnvelope(env)
	}
}

func (r *relayRuntime) signalDisconnect(err error) {
	r.disconnectOnce.Do(func() {
		if r.onDisconnect != nil {
			r.onDisconnect(err)
		}
	})
}

func (r *relayRuntime) sendEnvelope(env relayEnvelope) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to encode relay message: %w", err)
	}
	return r.conn.WriteMessage(websocket.TextMessage, payload)
}

func (r *relayRuntime) handleEnvelope(env relayEnvelope) {
	switch env.Kind {
	case "http.request.start":
		r.handleHTTPRequestStart(env)
	case "http.request.body":
		r.handleHTTPRequestBody(env)
	case "http.request.end":
		r.handleHTTPRequestEnd(env)
	case "ws.start":
		r.handleWSStart(env)
	case "ws.message":
		r.handleWSMessage(env)
	case "ws.close":
		r.handleWSClose(env)
	case "stream.error":
		r.handleStreamError(env)
	case "ping":
		_ = r.sendEnvelope(relayEnvelope{Kind: "pong"})
	}
}

func (r *relayRuntime) handleHTTPRequestStart(env relayEnvelope) {
	targetURL := url.URL{
		Scheme:   "http",
		Host:     fmt.Sprintf("127.0.0.1:%d", r.localPort),
		Path:     env.Path,
		RawQuery: env.Query,
	}

	ctx, cancel := context.WithCancel(r.ctx)
	bodyReader, bodyWriter := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, env.Method, targetURL.String(), bodyReader)
	if err != nil {
		cancel()
		_ = r.sendEnvelope(relayEnvelope{
			Kind:     "stream.error",
			StreamID: env.StreamID,
			Message:  fmt.Sprintf("failed to create local request: %v", err),
		})
		return
	}
	req.Header = relayHeadersToHTTP(env.Headers)

	r.streamMu.Lock()
	r.httpStreams[env.StreamID] = &relayHTTPStream{bodyWriter: bodyWriter, cancel: cancel}
	r.streamMu.Unlock()

	go func() {
		defer func() {
			r.streamMu.Lock()
			delete(r.httpStreams, env.StreamID)
			r.streamMu.Unlock()
			cancel()
		}()

		resp, err := r.httpClient.Do(req)
		if err != nil {
			_ = r.sendEnvelope(relayEnvelope{
				Kind:     "stream.error",
				StreamID: env.StreamID,
				Message:  fmt.Sprintf("local dev server request failed: %v", err),
			})
			return
		}
		defer resp.Body.Close()

		if err := r.sendEnvelope(relayEnvelope{
			Kind:     "http.response.start",
			StreamID: env.StreamID,
			Status:   resp.StatusCode,
			Headers:  relayHeadersFromHTTP(resp.Header),
		}); err != nil {
			return
		}

		buf := make([]byte, relayChunkSize)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				chunk := base64.StdEncoding.EncodeToString(buf[:n])
				if err := r.sendEnvelope(relayEnvelope{
					Kind:         "http.response.body",
					StreamID:     env.StreamID,
					BodyChunkB64: chunk,
				}); err != nil {
					return
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = r.sendEnvelope(relayEnvelope{
					Kind:     "stream.error",
					StreamID: env.StreamID,
					Message:  fmt.Sprintf("failed reading local response body: %v", readErr),
				})
				return
			}
		}

		_ = r.sendEnvelope(relayEnvelope{
			Kind:     "http.response.end",
			StreamID: env.StreamID,
		})
	}()
}

func (r *relayRuntime) handleHTTPRequestBody(env relayEnvelope) {
	r.streamMu.Lock()
	stream := r.httpStreams[env.StreamID]
	r.streamMu.Unlock()
	if stream == nil {
		return
	}
	chunk, err := base64.StdEncoding.DecodeString(env.BodyChunkB64)
	if err != nil {
		stream.cancel()
		_ = stream.bodyWriter.CloseWithError(err)
		return
	}
	_, _ = stream.bodyWriter.Write(chunk)
}

func (r *relayRuntime) handleHTTPRequestEnd(env relayEnvelope) {
	r.streamMu.Lock()
	stream := r.httpStreams[env.StreamID]
	r.streamMu.Unlock()
	if stream == nil {
		return
	}
	_ = stream.bodyWriter.Close()
}

func (r *relayRuntime) handleWSStart(env relayEnvelope) {
	targetURL := url.URL{
		Scheme:   "ws",
		Host:     fmt.Sprintf("127.0.0.1:%d", r.localPort),
		Path:     env.Path,
		RawQuery: env.Query,
	}

	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	localConn, _, err := dialer.DialContext(r.ctx, targetURL.String(), relayHeadersToHTTP(env.Headers))
	if err != nil {
		_ = r.sendEnvelope(relayEnvelope{
			Kind:     "stream.error",
			StreamID: env.StreamID,
			Message:  fmt.Sprintf("failed to connect to local websocket: %v", err),
		})
		return
	}

	stream := &relayWSStream{conn: localConn}
	r.streamMu.Lock()
	r.wsStreams[env.StreamID] = stream
	r.streamMu.Unlock()

	if err := r.sendEnvelope(relayEnvelope{
		Kind:     "ws.opened",
		StreamID: env.StreamID,
	}); err != nil {
		stream.mu.Lock()
		_ = localConn.Close()
		stream.mu.Unlock()
		return
	}

	go func() {
		defer func() {
			r.streamMu.Lock()
			delete(r.wsStreams, env.StreamID)
			r.streamMu.Unlock()
			stream.mu.Lock()
			_ = localConn.Close()
			stream.mu.Unlock()
		}()

		for {
			messageType, payload, err := localConn.ReadMessage()
			if err != nil {
				closeCode := websocket.CloseNormalClosure
				if ce, ok := err.(*websocket.CloseError); ok {
					closeCode = ce.Code
				}
				_ = r.sendEnvelope(relayEnvelope{
					Kind:      "ws.close",
					StreamID:  env.StreamID,
					CloseCode: closeCode,
				})
				return
			}

			msg := relayEnvelope{
				Kind:     "ws.message",
				StreamID: env.StreamID,
				Binary:   messageType == websocket.BinaryMessage,
			}
			if msg.Binary {
				msg.BodyChunkB64 = base64.StdEncoding.EncodeToString(payload)
			} else {
				msg.Text = string(payload)
			}
			if err := r.sendEnvelope(msg); err != nil {
				return
			}
		}
	}()
}

func (r *relayRuntime) handleWSMessage(env relayEnvelope) {
	r.streamMu.Lock()
	stream := r.wsStreams[env.StreamID]
	r.streamMu.Unlock()
	if stream == nil {
		return
	}

	messageType := websocket.TextMessage
	data := []byte(env.Text)
	if env.Binary {
		messageType = websocket.BinaryMessage
		decoded, err := base64.StdEncoding.DecodeString(env.BodyChunkB64)
		if err != nil {
			_ = r.sendEnvelope(relayEnvelope{
				Kind:     "stream.error",
				StreamID: env.StreamID,
				Message:  fmt.Sprintf("failed to decode websocket payload: %v", err),
			})
			return
		}
		data = decoded
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	_ = stream.conn.WriteMessage(messageType, data)
}

func (r *relayRuntime) handleWSClose(env relayEnvelope) {
	r.streamMu.Lock()
	stream := r.wsStreams[env.StreamID]
	r.streamMu.Unlock()
	if stream == nil {
		return
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	_ = stream.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(env.CloseCode, ""),
		time.Now().Add(2*time.Second),
	)
	_ = stream.conn.Close()
}

func (r *relayRuntime) handleStreamError(env relayEnvelope) {
	r.streamMu.Lock()
	httpStream := r.httpStreams[env.StreamID]
	wsStream := r.wsStreams[env.StreamID]
	r.streamMu.Unlock()

	if httpStream != nil {
		httpStream.cancel()
		_ = httpStream.bodyWriter.CloseWithError(fmt.Errorf("%s", env.Message))
	}
	if wsStream != nil {
		wsStream.mu.Lock()
		_ = wsStream.conn.Close()
		wsStream.mu.Unlock()
	}
}

func relayHeadersToHTTP(raw map[string][]string) http.Header {
	headers := make(http.Header)
	for key, values := range raw {
		if relayHopByHopHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	return headers
}

func relayHeadersFromHTTP(raw http.Header) map[string][]string {
	headers := make(map[string][]string, len(raw))
	for key, values := range raw {
		if relayHopByHopHeaders[strings.ToLower(key)] {
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		headers[key] = copied
	}
	return headers
}

// RelayTunnelBackend exposes the local dev server through the backend-owned relay.
type RelayTunnelBackend struct {
	client   *api.Client
	provider string

	mu           sync.Mutex
	session      *api.HotReloadRelaySession
	runtime      *relayRuntime
	localPort    int
	cancel       context.CancelFunc
	healthCancel context.CancelFunc
	onLog        func(string)
	stopped      bool
	disconnects  chan error
}

// NewRelayTunnelBackend creates a backend-owned relay transport.
func NewRelayTunnelBackend(client *api.Client, provider string) TunnelBackend {
	return &RelayTunnelBackend{
		client:      client,
		provider:    provider,
		disconnects: make(chan error, 4),
	}
}

// SetLogCallback registers a log callback on the relay backend.
func (r *RelayTunnelBackend) SetLogCallback(cb func(string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onLog = cb
}

func (r *RelayTunnelBackend) log(format string, args ...interface{}) {
	r.mu.Lock()
	cb := r.onLog
	r.mu.Unlock()
	if cb != nil {
		cb(fmt.Sprintf(format, args...))
	}
}

// Metadata returns relay transport metadata.
func (r *RelayTunnelBackend) Metadata() TunnelBackendInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	info := TunnelBackendInfo{Transport: "relay"}
	if r.session != nil {
		info.RelayID = r.session.RelayID
	}
	return info
}

// Start provisions the relay session and connects the CLI websocket.
func (r *RelayTunnelBackend) Start(ctx context.Context, localPort int) (string, error) {
	if r.client == nil {
		return "", fmt.Errorf("relay transport requires an authenticated API client")
	}

	r.mu.Lock()
	if r.stopped {
		r.stopped = false
	}
	r.localPort = localPort
	r.mu.Unlock()

	session, err := r.createRelaySession(ctx)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	r.session = session
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.mu.Unlock()

	r.log("[relay] reserved relay session id=%s transport=relay", session.RelayID)
	if err := r.connectRuntime(runCtx, localPort); err != nil {
		_ = r.revokeRelaySession(context.Background(), session.RelayID)
		r.mu.Lock()
		r.session = nil
		r.cancel = nil
		r.mu.Unlock()
		return "", err
	}
	return session.PublicURL, nil
}

func (r *RelayTunnelBackend) createRelaySession(
	ctx context.Context,
) (*api.HotReloadRelaySession, error) {
	session, err := r.client.CreateHotReloadRelay(ctx, api.HotReloadRelayCreateParams{
		Provider: strings.TrimSpace(r.provider),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create relay session: %w", err)
	}
	return session, nil
}

func (r *RelayTunnelBackend) heartbeatRelaySession(
	ctx context.Context,
	relayID string,
) (*api.HotReloadRelayHeartbeatStatus, error) {
	resp, err := r.client.HeartbeatHotReloadRelay(ctx, relayID)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (r *RelayTunnelBackend) revokeRelaySession(ctx context.Context, relayID string) error {
	return r.client.RevokeHotReloadRelay(ctx, relayID)
}

func (r *RelayTunnelBackend) connectRuntime(ctx context.Context, localPort int) error {
	r.mu.Lock()
	session := r.session
	r.mu.Unlock()
	if session == nil {
		return fmt.Errorf("relay session is not initialized")
	}

	wsURL, err := session.ConnectWebSocketURL()
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	headers := http.Header{
		"User-Agent": []string{"revyl-cli-relay"},
	}
	if authHeader := session.ConnectAuthHeader(); authHeader != "" {
		headers.Set("Authorization", authHeader)
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return fmt.Errorf("failed to connect relay websocket: %w", err)
	}

	runtime := newRelayRuntime(ctx, localPort, conn, func(msg string) {
		r.log("%s", msg)
	}, func(err error) {
		select {
		case r.disconnects <- err:
		default:
		}
	})
	runtime.start()

	r.mu.Lock()
	oldRuntime := r.runtime
	r.runtime = runtime
	r.mu.Unlock()

	if oldRuntime != nil {
		oldRuntime.stop()
	}
	return nil
}

// StartHealthMonitor starts relay heartbeat and reconnect monitors.
func (r *RelayTunnelBackend) StartHealthMonitor(ctx context.Context) {
	r.mu.Lock()
	if r.healthCancel != nil {
		r.mu.Unlock()
		return
	}
	monitorCtx, cancel := context.WithCancel(ctx)
	r.healthCancel = cancel
	r.mu.Unlock()

	go r.heartbeatLoop(monitorCtx)
	go r.reconnectLoop(monitorCtx)
}

func (r *RelayTunnelBackend) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(relayHeartbeatEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.mu.Lock()
			session := r.session
			stopped := r.stopped
			r.mu.Unlock()
			if stopped || session == nil {
				return
			}
			if _, err := r.heartbeatRelaySession(ctx, session.RelayID); err != nil {
				r.log("[relay] heartbeat failed: %v", err)
			}
		}
	}
}

func (r *RelayTunnelBackend) reconnectLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-r.disconnects:
			r.mu.Lock()
			session := r.session
			localPort := r.localPort
			stopped := r.stopped
			r.mu.Unlock()
			if stopped || session == nil {
				return
			}
			r.log("[relay] connection lost: %v", err)
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(relayReconnectBackoff):
				}
				if err := r.connectRuntime(ctx, localPort); err != nil {
					r.log("[relay] reconnect failed: %v", err)
					continue
				}
				r.log("[relay] reconnected to backend relay id=%s transport=relay", session.RelayID)
				break
			}
		}
	}
}

// Stop tears down the relay session and websocket connection.
func (r *RelayTunnelBackend) Stop() error {
	r.mu.Lock()
	r.stopped = true
	runtime := r.runtime
	session := r.session
	cancel := r.cancel
	healthCancel := r.healthCancel
	r.runtime = nil
	r.session = nil
	r.cancel = nil
	r.healthCancel = nil
	r.mu.Unlock()

	if healthCancel != nil {
		healthCancel()
	}
	if cancel != nil {
		cancel()
	}
	if runtime != nil {
		runtime.stop()
	}
	if session != nil {
		if err := r.revokeRelaySession(context.Background(), session.RelayID); err != nil {
			return err
		}
	}
	return nil
}

// PublicURL returns the current relay URL.
func (r *RelayTunnelBackend) PublicURL() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session == nil {
		return ""
	}
	return r.session.PublicURL
}
