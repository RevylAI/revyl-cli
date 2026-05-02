package hotreload

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckMetroHealth_PassesOnOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	port := serverPort(t, srv)
	c := checkMetroHealth(port, "")
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckMetroHealth_FailsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	port := serverPort(t, srv)
	c := checkMetroHealth(port, "")
	if c.Passed {
		t.Fatal("expected fail on 500 status")
	}
}

func TestCheckMetroHealth_FailsOnConnectionRefused(t *testing.T) {
	c := checkMetroHealth(freePort(t), "")
	if c.Passed {
		t.Fatal("expected fail on connection refused")
	}
}

func TestCheckLocalWebSocket_PassesOn101(t *testing.T) {
	ln := startWebSocketServer(t)
	defer ln.Close()

	port := listenerPort(t, ln)
	c := checkLocalWebSocket(port, "")
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckLocalWebSocket_FailsOnRefused(t *testing.T) {
	c := checkLocalWebSocket(freePort(t), "")
	if c.Passed {
		t.Fatal("expected fail on connection refused")
	}
}

func TestCheckTunnelHTTP_PassesOnOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := checkTunnelHTTP(0, srv.URL)
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckTunnelHTTP_FailsOnBadURL(t *testing.T) {
	c := checkTunnelHTTP(0, "http://127.0.0.1:1")
	if c.Passed {
		t.Fatal("expected fail on unreachable URL")
	}
}

func TestCheckManifestURLs_PassesWhenClean(t *testing.T) {
	manifest := map[string]interface{}{
		"launchAsset": map[string]string{"url": "https://tunnel.example.com/bundle.js"},
		"extra": map[string]interface{}{
			"expoGo":     map[string]string{"debuggerHost": "tunnel.example.com"},
			"expoClient": map[string]string{"hostUri": "tunnel.example.com"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(manifest)
	}))
	defer srv.Close()

	c := checkManifestURLs(8082, srv.URL)
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckManifestURLs_RequestsExpoPlatformHeaderAndQuery(t *testing.T) {
	manifest := map[string]interface{}{
		"launchAsset": map[string]string{"url": "https://tunnel.example.com/bundle.js"},
		"extra": map[string]interface{}{
			"expoGo":     map[string]string{"debuggerHost": "tunnel.example.com"},
			"expoClient": map[string]string{"hostUri": "tunnel.example.com"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("expo-platform") != "ios" || r.URL.Query().Get("platform") != "ios" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!DOCTYPE html><html><body>Expo dev tools</body></html>")
			return
		}
		json.NewEncoder(w).Encode(manifest)
	}))
	defer srv.Close()

	c := checkManifestURLsForPlatform(8082, srv.URL, "ios")
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckManifestURLs_RequestsAndroidPlatform(t *testing.T) {
	var headerPlatform string
	var queryPlatform string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerPlatform = r.Header.Get("expo-platform")
		queryPlatform = r.URL.Query().Get("platform")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"launchAsset": map[string]string{"url": "https://relay.example.com/index.bundle"},
			"extra": map[string]interface{}{
				"expoGo":     map[string]string{"debuggerHost": "relay.example.com"},
				"expoClient": map[string]string{"hostUri": "relay.example.com"},
			},
		})
	}))
	defer srv.Close()

	c := checkManifestURLsForPlatform(8081, srv.URL, "android")
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
	if headerPlatform != "android" {
		t.Fatalf("expo-platform = %q, want android", headerPlatform)
	}
	if queryPlatform != "android" {
		t.Fatalf("platform query = %q, want android", queryPlatform)
	}
}

func TestCheckManifestURLs_FailsOnPortLeak(t *testing.T) {
	manifest := map[string]interface{}{
		"launchAsset": map[string]string{"url": "https://tunnel.example.com:8082/bundle.js"},
		"extra": map[string]interface{}{
			"expoGo":     map[string]string{"debuggerHost": "tunnel.example.com:8082"},
			"expoClient": map[string]string{"hostUri": "tunnel.example.com:8082"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(manifest)
	}))
	defer srv.Close()

	c := checkManifestURLs(8082, srv.URL)
	if c.Passed {
		t.Fatal("expected fail on local port leak")
	}
	if !strings.Contains(c.Detail, "launchAsset") {
		t.Fatalf("detail = %q, expected mention of launchAsset", c.Detail)
	}
}

func TestRunPostStartupDiagnostics_AllPass(t *testing.T) {
	manifest := map[string]interface{}{
		"launchAsset": map[string]string{"url": "https://tunnel.example.com/bundle.js"},
		"extra": map[string]interface{}{
			"expoGo":     map[string]string{"debuggerHost": "tunnel.example.com"},
			"expoClient": map[string]string{"hostUri": "tunnel.example.com"},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(manifest)
	})
	mux.HandleFunc("/hot", websocketUpgradeHandler)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	port := serverPort(t, srv)
	result := RunPostStartupDiagnostics(port, srv.URL, "expo")

	if !result.AllPassed {
		for _, c := range result.Checks {
			if !c.Passed {
				t.Errorf("check %q failed: %s", c.Name, c.Detail)
			}
		}
		t.Fatal("expected all checks to pass")
	}
	if len(result.Checks) != 5 {
		t.Fatalf("got %d checks, want 5", len(result.Checks))
	}
}

func TestRunPostStartupDiagnostics_BareRN_SkipsManifest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Metro debugger</body></html>")
	})
	mux.HandleFunc("/hot", websocketUpgradeHandler)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	port := serverPort(t, srv)
	result := RunPostStartupDiagnostics(port, srv.URL, "react-native")

	if !result.AllPassed {
		for _, c := range result.Checks {
			if !c.Passed {
				t.Errorf("check %q failed: %s", c.Name, c.Detail)
			}
		}
		t.Fatal("expected all checks to pass for bare RN (manifest check should be skipped)")
	}
	if len(result.Checks) != 4 {
		t.Fatalf("got %d checks, want 4 (manifest check should be excluded)", len(result.Checks))
	}
}

func TestWaitForMetroTunnel_PassesAfterRetry(t *testing.T) {
	var ready atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/hot", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		websocketUpgradeHandler(w, r)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	go func() {
		time.Sleep(100 * time.Millisecond)
		ready.Store(true)
	}()

	result, err := WaitForMetroTunnel(
		context.Background(),
		serverPort(t, srv),
		srv.URL,
		time.Second,
		25*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected tunnel to become ready, got error: %v", err)
	}
	if result == nil || !result.AllPassed {
		t.Fatalf("expected passing result, got %+v", result)
	}
}

func TestWaitForMetroTunnel_TimesOutWithFailedChecks(t *testing.T) {
	result, err := WaitForMetroTunnel(
		context.Background(),
		0,
		"http://127.0.0.1:1",
		150*time.Millisecond,
		25*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if result == nil {
		t.Fatal("expected final diagnostic result on failure")
	}
	if !strings.Contains(err.Error(), "Tunnel HTTP") {
		t.Fatalf("expected failed check in error, got %q", err.Error())
	}
}

func TestWaitForExpoMetroRelay_PassesAfterStatusAndManifestReady(t *testing.T) {
	var ready atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.Header.Get("expo-platform") != "ios" || r.URL.Query().Get("platform") != "ios" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!DOCTYPE html><html><body>Expo dev tools</body></html>")
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"launchAsset": map[string]string{"url": "https://relay.example.com/bundle.js"},
			"extra": map[string]interface{}{
				"expoGo":     map[string]string{"debuggerHost": "relay.example.com"},
				"expoClient": map[string]string{"hostUri": "relay.example.com"},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	go func() {
		time.Sleep(100 * time.Millisecond)
		ready.Store(true)
	}()

	result, err := WaitForExpoMetroRelay(
		context.Background(),
		8081,
		srv.URL,
		time.Second,
		25*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected Expo relay to become ready, got error: %v", err)
	}
	if result == nil || !result.AllPassed {
		t.Fatalf("expected passing result, got %+v", result)
	}
}

func TestWaitForExpoMetroRelay_UsesAndroidPlatform(t *testing.T) {
	var headerPlatform string
	var queryPlatform string

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		headerPlatform = r.Header.Get("expo-platform")
		queryPlatform = r.URL.Query().Get("platform")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"launchAsset": map[string]string{"url": "https://relay.example.com/bundle.js"},
			"extra": map[string]interface{}{
				"expoGo":     map[string]string{"debuggerHost": "relay.example.com"},
				"expoClient": map[string]string{"hostUri": "relay.example.com"},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	result, err := WaitForExpoMetroRelayForPlatform(
		context.Background(),
		8081,
		srv.URL,
		time.Second,
		25*time.Millisecond,
		"android",
	)
	if err != nil {
		t.Fatalf("expected Expo relay to become ready, got error: %v", err)
	}
	if result == nil || !result.AllPassed {
		t.Fatalf("expected passing result, got %+v", result)
	}
	if headerPlatform != "android" {
		t.Fatalf("expo-platform = %q, want android", headerPlatform)
	}
	if queryPlatform != "android" {
		t.Fatalf("platform query = %q, want android", queryPlatform)
	}
}

func TestWaitForExpoMetroRelay_TimesOutOnManifestPortLeak(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"launchAsset": map[string]string{"url": "http://127.0.0.1:8081/bundle.js"},
			"extra": map[string]interface{}{
				"expoGo":     map[string]string{"debuggerHost": "127.0.0.1:8081"},
				"expoClient": map[string]string{"hostUri": "127.0.0.1:8081"},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	result, err := WaitForExpoMetroRelay(
		context.Background(),
		8081,
		srv.URL,
		150*time.Millisecond,
		25*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if result == nil {
		t.Fatal("expected final diagnostic result on failure")
	}
	if !strings.Contains(err.Error(), "Manifest URLs") {
		t.Fatalf("expected manifest failure in error, got %q", err.Error())
	}
}

func TestProbeWebSocketUpgrade_FailsOnHTTPEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	err := probeWebSocketUpgrade(addr, false)
	if err == nil {
		t.Fatal("expected error for non-websocket endpoint")
	}
}

// --- helpers ---

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr := srv.Listener.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

func listenerPort(t *testing.T, ln net.Listener) int {
	t.Helper()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := listenerPort(t, ln)
	ln.Close()
	return port
}

// startWebSocketServer returns a TCP listener that performs a minimal
// WebSocket upgrade handshake for any connection.
func startWebSocketServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleWSUpgrade(conn)
		}
	}()
	return ln
}

func handleWSUpgrade(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	request := string(buf[:n])
	key := ""
	for _, line := range strings.Split(request, "\r\n") {
		if strings.HasPrefix(line, "Sec-WebSocket-Key:") {
			key = strings.TrimSpace(strings.TrimPrefix(line, "Sec-WebSocket-Key:"))
		}
	}

	accept := computeAcceptKey(key)
	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	conn.Write([]byte(resp))
}

func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-5AB5DC11E65B"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// websocketUpgradeHandler is an http.HandlerFunc that hijacks the connection
// and performs a WebSocket upgrade.
func websocketUpgradeHandler(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
		http.Error(w, "not a websocket request", http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	key := r.Header.Get("Sec-WebSocket-Key")
	accept := computeAcceptKey(key)
	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	buf.WriteString(resp)
	buf.Flush()
}
