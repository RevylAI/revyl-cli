package hotreload

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DiagnosticCheck represents the result of a single diagnostic probe.
type DiagnosticCheck struct {
	// Name is a short human-readable label for this check (e.g. "Metro health").
	Name string

	// Passed indicates whether the check succeeded.
	Passed bool

	// Detail provides additional context on success or the error message on failure.
	Detail string
}

// DiagnosticResult aggregates the outcomes of all post-startup HMR health checks.
type DiagnosticResult struct {
	// Checks contains each individual probe result in execution order.
	Checks []DiagnosticCheck

	// AllPassed is true only when every check succeeded.
	AllPassed bool
}

// diagnosticHTTPTimeout is the timeout for HTTP and WebSocket probe connections.
const diagnosticHTTPTimeout = 5 * time.Second

// metroTunnelReadyTimeout bounds how long startup waits for the tunnel to
// become externally reachable before failing fast.
const metroTunnelReadyTimeout = 20 * time.Second

// metroTunnelReadyPollInterval controls how frequently tunnel readiness is re-checked.
const metroTunnelReadyPollInterval = 1500 * time.Millisecond

// diagnosticCheckFunc probes a single post-startup hot reload invariant.
type diagnosticCheckFunc func(localPort int, tunnelURL string) DiagnosticCheck

// RunPostStartupDiagnostics probes the HMR pipeline after the dev loop reports
// ready and returns structured pass/fail results. Checks run synchronously in
// order so the first failure can short-circuit if needed.
//
// Checks performed:
//  1. Metro health endpoint (GET http://127.0.0.1:{port}/status)
//  2. Local HMR WebSocket upgrade (ws://127.0.0.1:{port}/hot)
//  3. Tunnel HTTP reachability (GET {tunnelURL}/status)
//  4. Tunnel WebSocket upgrade (wss://{tunnelURL}/hot)
//  5. Manifest URL correctness — Expo only (no local-port leaks in launchAsset.url, debuggerHost, hostUri)
//
// Parameters:
//   - localPort: The local Metro dev server port
//   - tunnelURL: The public relay URL (e.g. "https://hr-abc.revyl.ai")
//   - providerName: The hot reload provider (e.g. "expo", "react-native").
//     The manifest URL check is skipped for non-Expo providers because bare
//     Metro does not serve a JSON manifest at the root path.
//
// Returns:
//   - *DiagnosticResult: Aggregated results with per-check detail
func RunPostStartupDiagnostics(localPort int, tunnelURL string, providerName string) *DiagnosticResult {
	checks := []diagnosticCheckFunc{
		checkMetroHealth,
		checkLocalWebSocket,
		checkTunnelHTTP,
		checkTunnelWebSocket,
	}
	if providerName == "expo" {
		checks = append(checks, checkManifestURLs)
	}
	return runDiagnosticChecks(localPort, tunnelURL, checks)
}

// WaitForMetroTunnel waits until the public Metro tunnel is reachable.
//
// This is primarily used by bare React Native startup. If the tunnel URL has
// not propagated yet, the cloud device can launch before the JavaScript bundle
// is reachable and remain on a blank white screen.
//
// Parameters:
//   - ctx: Context for cancellation while waiting.
//   - localPort: The local Metro dev server port.
//   - tunnelURL: The public relay URL.
//   - timeout: Maximum time to wait for the tunnel to become reachable.
//   - interval: Delay between retry attempts.
//
// Returns:
//   - *DiagnosticResult: The final probe result.
//   - error: Any timeout, cancellation, or readiness failure.
func WaitForMetroTunnel(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	interval time.Duration,
) (*DiagnosticResult, error) {
	checks := []diagnosticCheckFunc{
		checkTunnelHTTP,
		checkTunnelWebSocket,
	}

	deadline := time.Now().Add(timeout)
	var lastResult *DiagnosticResult

	for {
		lastResult = runDiagnosticChecks(localPort, tunnelURL, checks)
		if lastResult.AllPassed {
			return lastResult, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return lastResult, fmt.Errorf(
				"timed out after %s waiting for Metro tunnel readiness: %s",
				timeout,
				formatFailedChecks(lastResult.Checks),
			)
		}

		sleepFor := interval
		if sleepFor > remaining {
			sleepFor = remaining
		}

		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return lastResult, ctx.Err()
		case <-timer.C:
		}
	}
}

// runDiagnosticChecks executes a list of diagnostic probes and aggregates the results.
func runDiagnosticChecks(localPort int, tunnelURL string, checks []diagnosticCheckFunc) *DiagnosticResult {
	result := &DiagnosticResult{AllPassed: true}

	for _, check := range checks {
		c := check(localPort, tunnelURL)
		result.Checks = append(result.Checks, c)
		if !c.Passed {
			result.AllPassed = false
		}
	}

	return result
}

// formatFailedChecks summarizes the failed probe names and details.
func formatFailedChecks(checks []DiagnosticCheck) string {
	failed := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.Passed {
			continue
		}
		failed = append(failed, fmt.Sprintf("%s (%s)", check.Name, check.Detail))
	}
	if len(failed) == 0 {
		return "unknown failure"
	}
	return strings.Join(failed, "; ")
}

// checkMetroHealth verifies the local Metro server is responding.
// Uses 127.0.0.1 to match the relay path and avoid false failures on
// machines where "localhost" resolves to IPv6.
func checkMetroHealth(localPort int, _ string) DiagnosticCheck {
	client := &http.Client{Timeout: diagnosticHTTPTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/status", localPort))
	if err != nil {
		return DiagnosticCheck{Name: "Metro health", Passed: false, Detail: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DiagnosticCheck{Name: "Metro health", Passed: false, Detail: fmt.Sprintf("status %d", resp.StatusCode)}
	}
	return DiagnosticCheck{Name: "Metro health", Passed: true, Detail: "OK"}
}

// checkLocalWebSocket attempts a WebSocket upgrade to the local HMR endpoint.
// Uses 127.0.0.1 to match the relay path and avoid false failures on
// machines where "localhost" resolves to IPv6.
func checkLocalWebSocket(localPort int, _ string) DiagnosticCheck {
	addr := fmt.Sprintf("127.0.0.1:%d", localPort)
	err := probeWebSocketUpgrade(addr, false)
	if err != nil {
		return DiagnosticCheck{Name: "Local WebSocket (/hot)", Passed: false, Detail: err.Error()}
	}
	return DiagnosticCheck{Name: "Local WebSocket (/hot)", Passed: true, Detail: "OK"}
}

// checkTunnelHTTP verifies the tunnel forwards HTTP to Metro.
func checkTunnelHTTP(_ int, tunnelURL string) DiagnosticCheck {
	client := &http.Client{Timeout: diagnosticHTTPTimeout}
	statusURL := strings.TrimRight(tunnelURL, "/") + "/status"
	resp, err := client.Get(statusURL)
	if err != nil {
		return DiagnosticCheck{Name: "Tunnel HTTP", Passed: false, Detail: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DiagnosticCheck{Name: "Tunnel HTTP", Passed: false, Detail: fmt.Sprintf("status %d", resp.StatusCode)}
	}
	return DiagnosticCheck{Name: "Tunnel HTTP", Passed: true, Detail: "OK"}
}

// checkTunnelWebSocket attempts a WebSocket upgrade through the tunnel.
func checkTunnelWebSocket(_ int, tunnelURL string) DiagnosticCheck {
	host := strings.TrimPrefix(strings.TrimPrefix(tunnelURL, "https://"), "http://")
	host = strings.TrimRight(host, "/")
	err := probeWebSocketUpgrade(host, strings.HasPrefix(tunnelURL, "https://"))
	if err != nil {
		return DiagnosticCheck{Name: "Tunnel WebSocket", Passed: false, Detail: err.Error()}
	}
	return DiagnosticCheck{Name: "Tunnel WebSocket", Passed: true, Detail: "OK"}
}

// checkManifestURLs fetches the manifest through the tunnel and verifies that
// bundle/host URLs don't leak the local Metro port (which the cloud device
// cannot reach).
func checkManifestURLs(localPort int, tunnelURL string) DiagnosticCheck {
	client := &http.Client{Timeout: diagnosticHTTPTimeout}
	resp, err := client.Get(strings.TrimRight(tunnelURL, "/") + "/")
	if err != nil {
		return DiagnosticCheck{Name: "Manifest URLs", Passed: false, Detail: fmt.Sprintf("fetch failed: %s", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return DiagnosticCheck{Name: "Manifest URLs", Passed: false, Detail: fmt.Sprintf("read failed: %s", err)}
	}

	var manifest struct {
		LaunchAsset struct {
			URL string `json:"url"`
		} `json:"launchAsset"`
		Extra struct {
			ExpoGo struct {
				DebuggerHost string `json:"debuggerHost"`
			} `json:"expoGo"`
			ExpoClient struct {
				HostURI string `json:"hostUri"`
			} `json:"expoClient"`
		} `json:"extra"`
	}

	if err := json.Unmarshal(body, &manifest); err != nil {
		return DiagnosticCheck{Name: "Manifest URLs", Passed: false, Detail: fmt.Sprintf("parse failed: %s", err)}
	}

	localPortStr := fmt.Sprintf(":%d", localPort)
	var leaks []string
	if strings.Contains(manifest.LaunchAsset.URL, localPortStr) {
		leaks = append(leaks, fmt.Sprintf("launchAsset.url contains %s", localPortStr))
	}
	if strings.Contains(manifest.Extra.ExpoGo.DebuggerHost, localPortStr) {
		leaks = append(leaks, fmt.Sprintf("debuggerHost contains %s", localPortStr))
	}
	if strings.Contains(manifest.Extra.ExpoClient.HostURI, localPortStr) {
		leaks = append(leaks, fmt.Sprintf("hostUri contains %s", localPortStr))
	}

	if len(leaks) > 0 {
		return DiagnosticCheck{
			Name:   "Manifest URLs",
			Passed: false,
			Detail: fmt.Sprintf("local port leak: %s", strings.Join(leaks, "; ")),
		}
	}
	return DiagnosticCheck{Name: "Manifest URLs", Passed: true, Detail: "OK (no port mismatch)"}
}

// probeWebSocketUpgrade performs a raw TCP WebSocket upgrade handshake and
// returns nil if the server responds with 101 Switching Protocols.
//
// Parameters:
//   - hostPort: The host:port to connect to. If no port is present and useTLS
//     is true, ":443" is appended.
//   - useTLS: Whether to wrap the connection with TLS.
//
// Returns:
//   - error: nil on successful 101 response, otherwise describes the failure.
func probeWebSocketUpgrade(hostPort string, useTLS bool) error {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		if useTLS {
			port = "443"
			hostPort = host + ":443"
		} else {
			port = "80"
			hostPort = host + ":80"
		}
	}

	dialer := &net.Dialer{Timeout: diagnosticHTTPTimeout}
	var conn net.Conn
	if useTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", hostPort, &tls.Config{ServerName: host})
	} else {
		conn, err = dialer.Dial("tcp", hostPort)
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(diagnosticHTTPTimeout))

	// Per RFC 7230 §5.4, omit the port from the Host header when it is the
	// default for the scheme (443 for HTTPS, 80 for HTTP).
	hostHeader := host
	if (useTLS && port != "443") || (!useTLS && port != "80") {
		hostHeader = hostPort
	}

	req := fmt.Sprintf(
		"GET /hot HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n",
		hostHeader,
	)
	if _, err := conn.Write([]byte(req)); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	statusLine := string(buf[:n])
	if idx := strings.Index(statusLine, "\r\n"); idx > 0 {
		statusLine = statusLine[:idx]
	}

	if !strings.Contains(statusLine, "101") {
		return fmt.Errorf("unexpected response: %s", statusLine)
	}
	return nil
}
