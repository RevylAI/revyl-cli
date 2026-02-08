package hotreload

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/api"
)

// TunnelInfo contains information about an active tunnel.
type TunnelInfo struct {
	// TunnelID is the unique identifier for this tunnel session.
	TunnelID string

	// PublicURL is the public URL to access the tunnel (e.g., https://cog-xxx.revyl.ai).
	PublicURL string

	// LocalPort is the local port being tunneled.
	LocalPort int
}

// TunnelManager manages Cloudflare tunnel lifecycle.
type TunnelManager struct {
	cloudflaredPath string
	credentials     *api.CloudflareCredentials
	process         *exec.Cmd
	publicURL       string
	cancel          context.CancelFunc
	mu              sync.Mutex
	urlReady        chan struct{}
}

// NewTunnelManager creates a new TunnelManager.
//
// Parameters:
//   - cloudflaredPath: Path to the cloudflared binary
//   - credentials: Cloudflare credentials from the backend
//
// Returns:
//   - *TunnelManager: A new tunnel manager instance
func NewTunnelManager(cloudflaredPath string, credentials *api.CloudflareCredentials) *TunnelManager {
	return &TunnelManager{
		cloudflaredPath: cloudflaredPath,
		credentials:     credentials,
		urlReady:        make(chan struct{}),
	}
}

// StartTunnel creates a Cloudflare tunnel pointing to the local port.
//
// Parameters:
//   - ctx: Context for cancellation
//   - port: Local port to tunnel to
//
// Returns:
//   - *TunnelInfo: Information about the created tunnel
//   - error: Any error that occurred
func (t *TunnelManager) StartTunnel(ctx context.Context, port int) (*TunnelInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Create cancellable context
	ctx, t.cancel = context.WithCancel(ctx)

	// Build command - use quick tunnel for simplicity
	// Quick tunnels don't require account setup and work immediately
	// Use --config /dev/null to ignore any stale credentials in ~/.cloudflared/
	t.process = exec.CommandContext(ctx, t.cloudflaredPath,
		"tunnel",
		"--config", "/dev/null",
		"--url", fmt.Sprintf("http://localhost:%d", port))

	// Capture stderr (cloudflared outputs URL to stderr)
	stderr, err := t.process.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to capture stderr: %w", err)
	}

	// Start the process
	if err := t.process.Start(); err != nil {
		return nil, fmt.Errorf("failed to start cloudflared: %w", err)
	}

	// Parse output for tunnel URL
	urlChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stderr)
		// Regex to match: https://xxx.trycloudflare.com
		urlRegex := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

		for scanner.Scan() {
			line := scanner.Text()
			if match := urlRegex.FindString(line); match != "" {
				urlChan <- match
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errChan <- fmt.Errorf("error reading cloudflared output: %w", err)
		} else {
			errChan <- fmt.Errorf("cloudflared exited without providing URL")
		}
	}()

	// Wait for URL with timeout
	select {
	case url := <-urlChan:
		t.publicURL = url
		close(t.urlReady)
		return &TunnelInfo{
			TunnelID:  fmt.Sprintf("quick-%d", port),
			PublicURL: url,
			LocalPort: port,
		}, nil
	case err := <-errChan:
		t.Stop()
		return nil, err
	case <-time.After(30 * time.Second):
		t.Stop()
		return nil, fmt.Errorf("timeout waiting for tunnel URL (30s)")
	case <-ctx.Done():
		t.Stop()
		return nil, ctx.Err()
	}
}

// Stop terminates the tunnel.
//
// Returns:
//   - error: Any error that occurred during shutdown
func (t *TunnelManager) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}

	if t.process != nil && t.process.Process != nil {
		// Try graceful termination first
		if err := t.process.Process.Signal(os.Interrupt); err != nil {
			// Fall back to kill
			t.process.Process.Kill()
		}

		// Wait for process to exit (with timeout)
		done := make(chan error, 1)
		go func() {
			done <- t.process.Wait()
		}()

		select {
		case <-done:
			// Process exited
		case <-time.After(5 * time.Second):
			// Force kill
			t.process.Process.Kill()
		}

		t.process = nil
	}

	t.publicURL = ""
	return nil
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
	req, err = http.NewRequestWithContext(ctx, "GET", "https://backend.revyl.ai/health", nil)
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

// isNetworkError checks if an error is likely a network-related error.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	networkIndicators := []string{
		"connection refused",
		"no such host",
		"network is unreachable",
		"timeout",
		"dial tcp",
		"dial udp",
		"i/o timeout",
		"connection reset",
		"EOF",
	}

	for _, indicator := range networkIndicators {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(indicator)) {
			return true
		}
	}

	return false
}
