package hotreload

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Destination-tunnel streams ride the relay's WebSocket stream kinds: the
// session's forwarder opens one stream per TCP connection on TunnelStreamPath,
// naming its selector in the dst query parameter, and every message carries
// raw bytes.
const (
	TunnelStreamPath        = "/_revyl/tunnel/v1"
	TunnelKeyHeader         = "X-Revyl-Tunnel-Key"
	TunnelSubprotocol       = "revyl-tunnel.v1"
	tunnelDialTimeout       = 10 * time.Second
	tunnelStreamQueueLength = 64
)

// TunnelCounts are the bounded totals a tunnel reports; never hosts or bytes of content.
type TunnelCounts struct {
	StreamsOpened  int64
	StreamsRefused int64
	DialFailures   int64
	BytesUp        int64
	BytesDown      int64
}

// TCPBridge dials the declared destinations of a destination tunnel on this
// machine. It refuses every stream without the tunnel key or for a
// destination outside its selectors, so the relay can't widen what it reaches.
type TCPBridge struct {
	key          []byte
	destinations map[string]bool
	dialer       net.Dialer

	streamsOpened  atomic.Int64
	streamsRefused atomic.Int64
	dialFailures   atomic.Int64
	bytesUp        atomic.Int64
	bytesDown      atomic.Int64
}

// NewTCPBridge accepts streams that present key and name one of destinations.
func NewTCPBridge(key string, destinations []string) *TCPBridge {
	allowed := make(map[string]bool, len(destinations))
	for _, destination := range destinations {
		allowed[destination] = true
	}
	return &TCPBridge{key: []byte(key), destinations: allowed, dialer: net.Dialer{Timeout: tunnelDialTimeout}}
}

// Counts returns the bridge's totals so far.
func (b *TCPBridge) Counts() TunnelCounts {
	return TunnelCounts{
		StreamsOpened:  b.streamsOpened.Load(),
		StreamsRefused: b.streamsRefused.Load(),
		DialFailures:   b.dialFailures.Load(),
		BytesUp:        b.bytesUp.Load(),
		BytesDown:      b.bytesDown.Load(),
	}
}

func (b *TCPBridge) authorize(env relayEnvelope) (string, bool) {
	if env.Path != TunnelStreamPath {
		return "", false
	}
	offered := false
	for _, protocol := range env.Subprotocols {
		offered = offered || protocol == TunnelSubprotocol
	}
	presented := []byte(relayHeadersToHTTP(env.Headers).Get(TunnelKeyHeader))
	if !offered || subtle.ConstantTimeCompare(presented, b.key) != 1 {
		return "", false
	}
	query, err := url.ParseQuery(env.Query)
	if err != nil {
		return "", false
	}
	destination := query.Get("dst")
	return destination, b.destinations[destination]
}

type relayTCPStream struct {
	conn         net.Conn
	cancel       context.CancelFunc
	incoming     chan []byte
	once         sync.Once
	remoteClosed atomic.Bool
	aborted      atomic.Bool
}

func (s *relayTCPStream) close() {
	s.once.Do(func() {
		s.cancel()
		_ = s.conn.Close()
	})
}

// abort closes the stream for a local failure, so the session's side is told
// the connection failed instead of ending normally.
func (s *relayTCPStream) abort() {
	s.aborted.Store(true)
	s.close()
}

func (r *relayRuntime) handleTCPStart(env relayEnvelope) {
	destination, ok := r.tcpBridge.authorize(env)
	if !ok {
		r.tcpBridge.streamsRefused.Add(1)
		_ = r.sendEnvelope(relayEnvelope{Kind: "stream.error", StreamID: env.StreamID, Message: "tunnel stream refused"})
		return
	}
	go r.runTCPStream(env, destination)
}

func (r *relayRuntime) runTCPStream(env relayEnvelope, destination string) {
	ctx, cancel := context.WithCancel(r.ctx)
	conn, err := r.tcpBridge.dialer.DialContext(ctx, "tcp", destination)
	if err != nil {
		cancel()
		r.tcpBridge.dialFailures.Add(1)
		r.log("[tunnel] %s is not answering on this machine", destination)
		_ = r.sendEnvelope(relayEnvelope{Kind: "stream.error", StreamID: env.StreamID, Message: "tunnel destination unavailable"})
		return
	}
	stream := &relayTCPStream{conn: conn, cancel: cancel, incoming: make(chan []byte, tunnelStreamQueueLength)}
	r.streamMu.Lock()
	r.tcpStreams[env.StreamID] = stream
	r.streamMu.Unlock()
	defer func() {
		stream.close()
		r.streamMu.Lock()
		delete(r.tcpStreams, env.StreamID)
		r.streamMu.Unlock()
	}()
	if err := r.sendEnvelope(relayEnvelope{Kind: "ws.opened", StreamID: env.StreamID, Subprotocol: TunnelSubprotocol}); err != nil {
		return
	}
	r.tcpBridge.streamsOpened.Add(1)

	go func() {
		defer stream.close()
		for {
			select {
			case <-ctx.Done():
				return
			case chunk := <-stream.incoming:
				if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
					stream.abort()
					return
				}
				if _, err := conn.Write(chunk); err != nil {
					stream.abort()
					return
				}
				r.tcpBridge.bytesUp.Add(int64(len(chunk)))
			}
		}
	}()

	buffer := make([]byte, relayChunkSize)
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			r.tcpBridge.bytesDown.Add(int64(n))
			if sendErr := r.sendEnvelope(relayEnvelope{
				Kind:         "ws.message",
				StreamID:     env.StreamID,
				Binary:       true,
				BodyChunkB64: base64.StdEncoding.EncodeToString(buffer[:n]),
			}); sendErr != nil {
				return
			}
		}
		if err != nil {
			switch {
			case r.ctx.Err() != nil, stream.remoteClosed.Load():
			case stream.aborted.Load():
				_ = r.sendEnvelope(relayEnvelope{Kind: "ws.close", StreamID: env.StreamID, CloseCode: websocket.CloseInternalServerErr})
			default:
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					r.log("[tunnel] connection to %s ended: %v", destination, err)
				}
				_ = r.sendEnvelope(relayEnvelope{Kind: "ws.close", StreamID: env.StreamID, CloseCode: websocket.CloseNormalClosure})
			}
			return
		}
	}
}

// deliverTCP queues relay bytes for a tunnel stream; a stream that can't keep
// up is closed rather than stalling the shared relay connection.
func (r *relayRuntime) deliverTCP(env relayEnvelope) bool {
	r.streamMu.Lock()
	stream := r.tcpStreams[env.StreamID]
	r.streamMu.Unlock()
	if stream == nil {
		return false
	}
	switch env.Kind {
	case "ws.close", "stream.error":
		stream.remoteClosed.Store(true)
		stream.close()
		return true
	}
	data, err := base64.StdEncoding.DecodeString(env.BodyChunkB64)
	if err != nil || !env.Binary {
		stream.abort()
		return true
	}
	select {
	case stream.incoming <- data:
	default:
		r.log("[tunnel] a connection fell behind and was closed")
		stream.abort()
	}
	return true
}
