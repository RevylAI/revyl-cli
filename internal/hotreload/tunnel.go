package hotreload

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// stderrBufferSize is the number of recent cloudflared stderr lines retained for diagnostics.
const stderrBufferSize = 20

// TunnelConfig holds configuration for tunnel creation and retry behavior.
type TunnelConfig struct {
	// MaxAttempts is the maximum number of tunnel creation attempts before giving up.
	MaxAttempts int

	// BaseDelay is the initial delay between retry attempts.
	// Subsequent delays double (exponential backoff).
	BaseDelay time.Duration

	// URLTimeout is the maximum time to wait for cloudflared to emit its tunnel URL per attempt.
	URLTimeout time.Duration
}

// DefaultTunnelConfig returns sensible defaults for tunnel creation.
//
// Returns:
//   - TunnelConfig: 3 attempts, 2s base delay, 30s URL timeout
func DefaultTunnelConfig() TunnelConfig {
	return TunnelConfig{
		MaxAttempts: 3,
		BaseDelay:   2 * time.Second,
		URLTimeout:  30 * time.Second,
	}
}

// TunnelEventType classifies tunnel lifecycle events emitted on the Events channel.
type TunnelEventType int

const (
	// TunnelEventConnected signals the tunnel was established successfully.
	TunnelEventConnected TunnelEventType = iota

	// TunnelEventDisconnected signals the tunnel process exited unexpectedly.
	TunnelEventDisconnected

	// TunnelEventReconnecting signals a reconnection attempt is in progress.
	TunnelEventReconnecting

	// TunnelEventReconnected signals the tunnel was re-established after a disconnect.
	TunnelEventReconnected

	// TunnelEventFailed signals all reconnection attempts have been exhausted.
	TunnelEventFailed
)

// TunnelEvent represents a tunnel lifecycle state change.
type TunnelEvent struct {
	// Type classifies the event.
	Type TunnelEventType

	// URL is the tunnel's public URL (set for Connected and Reconnected events).
	URL string

	// Err is the associated error (set for Disconnected and Failed events).
	Err error

	// Attempt is the 1-indexed retry attempt number (set for Reconnecting events).
	Attempt int
}

// TunnelInfo contains information about an active tunnel.
type TunnelInfo struct {
	// TunnelID is the unique identifier for this tunnel session.
	TunnelID string

	// PublicURL is the public URL to access the tunnel (e.g., https://cog-xxx.revyl.ai).
	PublicURL string

	// LocalPort is the local port being tunneled.
	LocalPort int
}

// TunnelManager manages Cloudflare tunnel lifecycle including retries and health monitoring.
type TunnelManager struct {
	cloudflaredPath string
	credentials     *api.CloudflareCredentials
	config          TunnelConfig
	process         *exec.Cmd
	publicURL       string
	localPort       int
	cancel          context.CancelFunc
	mu              sync.Mutex
	onLog           func(string)

	// processExited is closed when the cloudflared process exits and Wait completes.
	// Created fresh by each call to startTunnelLocked.
	processExited chan struct{}

	// events delivers tunnel lifecycle events. Buffered to avoid blocking the health monitor.
	events chan TunnelEvent

	// healthCancel stops the background health monitor goroutine.
	healthCancel context.CancelFunc

	// stopped is set by stopLocked to suppress health-monitor reconnection.
	stopped bool
}

// NewTunnelManager creates a new TunnelManager.
//
// Parameters:
//   - cloudflaredPath: Path to the cloudflared binary
//   - credentials: Cloudflare credentials from the backend (may be nil for quick tunnels)
//
// Returns:
//   - *TunnelManager: A new tunnel manager instance
func NewTunnelManager(cloudflaredPath string, credentials *api.CloudflareCredentials) *TunnelManager {
	return &TunnelManager{
		cloudflaredPath: cloudflaredPath,
		credentials:     credentials,
		config:          DefaultTunnelConfig(),
		events:          make(chan TunnelEvent, 8),
	}
}

// SetConfig overrides the default tunnel configuration.
//
// Parameters:
//   - config: Tunnel configuration with retry and timeout settings
func (t *TunnelManager) SetConfig(config TunnelConfig) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.config = config
}

// SetLogCallback registers a callback for tunnel log messages (retries, health events).
//
// Parameters:
//   - cb: Callback function receiving formatted log strings
func (t *TunnelManager) SetLogCallback(cb func(string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onLog = cb
}

// Events returns a read-only channel of tunnel lifecycle events.
// The channel is buffered (capacity 8) and never closed; drain it to avoid missed events.
//
// Returns:
//   - <-chan TunnelEvent: Channel of lifecycle events
func (t *TunnelManager) Events() <-chan TunnelEvent {
	return t.events
}

// log sends a formatted message to the log callback if registered.
func (t *TunnelManager) log(format string, args ...interface{}) {
	if t.onLog != nil {
		t.onLog(fmt.Sprintf(format, args...))
	}
}

// emit sends a tunnel event without blocking. Drops the event if the channel is full.
func (t *TunnelManager) emit(event TunnelEvent) {
	select {
	case t.events <- event:
	default:
	}
}

// StartTunnel creates a Cloudflare tunnel pointing to the local port (single attempt).
//
// Parameters:
//   - ctx: Context for cancellation
//   - port: Local port to tunnel to
//
// Returns:
//   - *TunnelInfo: Information about the created tunnel
//   - error: Any error that occurred, including buffered cloudflared stderr output for diagnostics
func (t *TunnelManager) StartTunnel(ctx context.Context, port int) (*TunnelInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = false
	return t.startTunnelLocked(ctx, port)
}

// startTunnelLocked creates a single tunnel attempt.
// Callers must hold t.mu. Buffers cloudflared stderr output and includes it in errors.
// Does NOT reset t.stopped; callers (StartTunnel, StartTunnelWithRetry) manage that flag
// so a concurrent Stop() is respected during retry backoff windows.
func (t *TunnelManager) startTunnelLocked(ctx context.Context, port int) (*TunnelInfo, error) {
	if t.stopped {
		return nil, fmt.Errorf("tunnel was stopped")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	ctx, t.cancel = context.WithCancel(ctx)

	t.process = exec.CommandContext(ctx, t.cloudflaredPath,
		"tunnel",
		"--config", "/dev/null",
		"--url", fmt.Sprintf("http://localhost:%d", port))

	stderr, err := t.process.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to capture stderr: %w", err)
	}

	if err := t.process.Start(); err != nil {
		return nil, fmt.Errorf("failed to start cloudflared: %w", err)
	}

	t.processExited = make(chan struct{})
	exitedChan := t.processExited
	procRef := t.process

	urlChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		defer func() {
			procRef.Wait()
			close(exitedChan)
		}()

		scanner := bufio.NewScanner(stderr)
		urlRegex := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
		var recentLines []string
		urlFound := false

		for scanner.Scan() {
			if urlFound {
				continue
			}
			line := scanner.Text()
			if len(recentLines) >= stderrBufferSize {
				recentLines = recentLines[1:]
			}
			recentLines = append(recentLines, line)

			if match := urlRegex.FindString(line); match != "" {
				urlChan <- match
				urlFound = true
			}
		}
		if !urlFound {
			output := strings.Join(recentLines, "\n")
			if scanErr := scanner.Err(); scanErr != nil {
				errChan <- fmt.Errorf("error reading cloudflared output: %w\nOutput:\n%s", scanErr, output)
			} else if output != "" {
				errChan <- fmt.Errorf("cloudflared exited without providing URL\nOutput:\n%s", output)
			} else {
				errChan <- fmt.Errorf("cloudflared exited without providing URL (no output)")
			}
		}
	}()

	select {
	case url := <-urlChan:
		t.publicURL = url
		t.localPort = port
		return &TunnelInfo{
			TunnelID:  fmt.Sprintf("quick-%d", port),
			PublicURL: url,
			LocalPort: port,
		}, nil
	case err := <-errChan:
		_ = t.stopProcessLocked()
		return nil, err
	case <-time.After(t.config.URLTimeout):
		_ = t.stopProcessLocked()
		return nil, fmt.Errorf("timeout waiting for tunnel URL (%s)", t.config.URLTimeout)
	case <-ctx.Done():
		_ = t.stopProcessLocked()
		return nil, ctx.Err()
	}
}

// StartTunnelWithRetry creates a Cloudflare tunnel with configurable retry and exponential backoff.
// Uses the TunnelConfig set via SetConfig (or DefaultTunnelConfig).
//
// Parameters:
//   - ctx: Context for cancellation
//   - port: Local port to tunnel to
//
// Returns:
//   - *TunnelInfo: Information about the created tunnel
//   - error: The last error if all attempts fail, wrapped with the attempt count
func (t *TunnelManager) StartTunnelWithRetry(ctx context.Context, port int) (*TunnelInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stopped = false

	var lastErr error
	delay := t.config.BaseDelay

	for attempt := 1; attempt <= t.config.MaxAttempts; attempt++ {
		if attempt > 1 {
			t.emit(TunnelEvent{Type: TunnelEventReconnecting, Attempt: attempt})
		}

		info, err := t.startTunnelLocked(ctx, port)
		if err == nil {
			if attempt > 1 {
				t.log("Tunnel established on attempt %d/%d", attempt, t.config.MaxAttempts)
			}
			t.emit(TunnelEvent{Type: TunnelEventConnected, URL: info.PublicURL})
			return info, nil
		}

		lastErr = err

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if attempt < t.config.MaxAttempts {
			t.log("Tunnel attempt %d/%d failed: %v", attempt, t.config.MaxAttempts, err)
			t.log("Retrying in %s...", delay)

			t.mu.Unlock()
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				t.mu.Lock()
				return nil, ctx.Err()
			}
			t.mu.Lock()

			if t.stopped {
				return nil, fmt.Errorf("tunnel was stopped")
			}
			delay *= 2
		}
	}

	return nil, fmt.Errorf("tunnel creation failed after %d attempts: %w", t.config.MaxAttempts, lastErr)
}

// Stop terminates the tunnel and its health monitor.
//
// Returns:
//   - error: Any error that occurred during shutdown
func (t *TunnelManager) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopLocked()
}

// stopLocked terminates the health monitor, cloudflared process, and clears state.
// Callers must hold t.mu.
func (t *TunnelManager) stopLocked() error {
	t.stopped = true

	if t.healthCancel != nil {
		t.healthCancel()
		t.healthCancel = nil
	}

	return t.stopProcessLocked()
}

// stopProcessLocked terminates only the cloudflared process without affecting the health monitor.
// Cancels the process context first (triggering exec.CommandContext's SIGKILL), then waits
// for the process to exit. Used internally during retry cleanup. Callers must hold t.mu.
func (t *TunnelManager) stopProcessLocked() error {
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}

	if t.process != nil && t.process.Process != nil {
		if t.processExited != nil {
			select {
			case <-t.processExited:
			case <-time.After(5 * time.Second):
				_ = t.process.Process.Kill()
			}
		}
		t.process = nil
	}

	t.publicURL = ""
	t.localPort = 0
	return nil
}

// StartHealthMonitor spawns a background goroutine that watches the cloudflared process.
// If the process exits unexpectedly, it attempts to re-establish the tunnel using retry logic.
// Events are emitted on the Events channel and logged via the log callback.
//
// The monitor runs until ctx is cancelled, Stop is called, or reconnection permanently fails.
//
// Parameters:
//   - ctx: Parent context; cancellation stops the monitor
func (t *TunnelManager) StartHealthMonitor(ctx context.Context) {
	healthCtx, healthCancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.healthCancel = healthCancel
	t.mu.Unlock()

	go t.runHealthMonitor(healthCtx)
}

// runHealthMonitor is the health monitor goroutine loop.
// It waits for the cloudflared process to exit and attempts reconnection.
func (t *TunnelManager) runHealthMonitor(ctx context.Context) {
	for {
		t.mu.Lock()
		exited := t.processExited
		t.mu.Unlock()

		if exited == nil {
			return
		}

		select {
		case <-exited:
			t.mu.Lock()
			if t.stopped {
				t.mu.Unlock()
				return
			}
			port := t.localPort
			t.mu.Unlock()

			if ctx.Err() != nil {
				return
			}

			t.emit(TunnelEvent{Type: TunnelEventDisconnected})
			t.log("Tunnel disconnected unexpectedly, attempting reconnection...")

			info, err := t.StartTunnelWithRetry(ctx, port)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				t.emit(TunnelEvent{Type: TunnelEventFailed, Err: err})
				t.log("Tunnel reconnection failed permanently: %v", err)
				return
			}

			t.emit(TunnelEvent{Type: TunnelEventReconnected, URL: info.PublicURL})
			t.log("Tunnel reconnected: %s", info.PublicURL)

		case <-ctx.Done():
			return
		}
	}
}

// GetPublicURL returns the public tunnel URL.
//
// Returns:
//   - string: The public URL, or empty string if tunnel is not running
func (t *TunnelManager) GetPublicURL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.publicURL
}

// IsRunning returns whether the tunnel is currently running.
//
// Returns:
//   - bool: True if tunnel is running
func (t *TunnelManager) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.process != nil && t.publicURL != ""
}

// ConnectivityCheckResult contains the result of network connectivity checks.
type ConnectivityCheckResult struct {
	// CanReachCloudflare indicates if Cloudflare API is reachable.
	CanReachCloudflare bool

	// CanReachRevylAPI indicates if Revyl API is reachable.
	CanReachRevylAPI bool

	// CanResolveDNS indicates if DNS resolution is working.
	CanResolveDNS bool

	// BlockedBy describes what is blocking connectivity ("dns", "firewall", "proxy", "unknown").
	BlockedBy string

	// Suggestion contains a helpful message for the user.
	Suggestion string
}

// CheckConnectivity performs pre-flight checks before attempting tunnel creation.
//
// Checks performed:
//  1. DNS resolution for cloudflare.com and revyl.ai
//  2. HTTPS connection to Cloudflare API
//  3. HTTPS connection to Revyl API
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *ConnectivityCheckResult: Detailed diagnostics
//   - error: Any error that occurred during checks
func CheckConnectivity(ctx context.Context) (*ConnectivityCheckResult, error) {
	result := &ConnectivityCheckResult{}

	// 1. Check DNS resolution
	if _, err := net.LookupHost("cloudflare.com"); err != nil {
		result.BlockedBy = "dns"
		result.Suggestion = "DNS resolution failed. Your network may be blocking external DNS queries."
		return result, nil
	}
	result.CanResolveDNS = true

	// 2. Check HTTPS to Cloudflare
	client := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.cloudflare.com/client/v4/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		result.BlockedBy = "firewall"
		result.Suggestion = "Cannot reach Cloudflare API. Your firewall may be blocking outbound HTTPS."
		return result, nil
	}
	resp.Body.Close()
	result.CanReachCloudflare = true

	// 3. Check Revyl API
	req, err = http.NewRequestWithContext(ctx, "GET", config.GetBackendURL(false)+"/health", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err = client.Do(req)
	if err != nil {
		result.BlockedBy = "firewall"
		result.Suggestion = "Cannot reach Revyl API. Your firewall may be blocking revyl.ai."
		return result, nil
	}
	resp.Body.Close()
	result.CanReachRevylAPI = true

	return result, nil
}

// DiagnoseAndSuggest provides user-friendly error messages for network issues.
//
// Parameters:
//   - result: The connectivity check result
//
// Returns:
//   - string: A helpful message for the user, or empty string if all checks passed
func DiagnoseAndSuggest(result *ConnectivityCheckResult) string {
	if result.CanReachCloudflare && result.CanReachRevylAPI && result.CanResolveDNS {
		return "" // All good
	}

	var msg strings.Builder
	msg.WriteString("Network connectivity issue detected.\n\n")

	switch result.BlockedBy {
	case "dns":
		msg.WriteString("Problem: DNS resolution is failing.\n")
		msg.WriteString("This often happens on corporate networks with restricted DNS.\n\n")
		msg.WriteString("Suggestions:\n")
		msg.WriteString("  1. Try using a different network (e.g., mobile hotspot)\n")
		msg.WriteString("  2. Ask your IT team to allow DNS queries to cloudflare.com and revyl.ai\n")
		msg.WriteString("  3. Try setting DNS to 1.1.1.1 or 8.8.8.8\n")

	case "firewall":
		msg.WriteString("Problem: Outbound HTTPS connections are being blocked.\n")
		msg.WriteString("Your corporate firewall may be restricting access.\n\n")
		msg.WriteString("Suggestions:\n")
		msg.WriteString("  1. Try using a different network (e.g., mobile hotspot)\n")
		msg.WriteString("  2. Ask your IT team to allowlist:\n")
		msg.WriteString("     - *.cloudflare.com\n")
		msg.WriteString("     - *.revyl.ai\n")
		msg.WriteString("     - *.trycloudflare.com\n")
		msg.WriteString("  3. If using a VPN, try disconnecting\n")

	case "proxy":
		msg.WriteString("Problem: A proxy is interfering with connections.\n\n")
		msg.WriteString("Suggestions:\n")
		msg.WriteString("  1. Try bypassing the proxy for *.revyl.ai and *.cloudflare.com\n")
		msg.WriteString("  2. Check your HTTP_PROXY/HTTPS_PROXY environment variables\n")

	default:
		msg.WriteString("Problem: Unknown network issue.\n\n")
		msg.WriteString("Suggestions:\n")
		msg.WriteString("  1. Check your internet connection\n")
		msg.WriteString("  2. Try using a different network\n")
		msg.WriteString("  3. Contact support@revyl.ai with this error\n")
	}

	return msg.String()
}
