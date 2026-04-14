package hotreload

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/revyl/cli/internal/config"
)

// TunnelBackendInfo describes transport metadata exposed by a tunnel backend.
type TunnelBackendInfo struct {
	Transport string
	RelayID   string
}

// TunnelBackend abstracts the network transport that exposes a local port to
// cloud devices.
type TunnelBackend interface {
	// Start creates the tunnel/relay and returns the public URL that devices can reach.
	Start(ctx context.Context, localPort int) (publicURL string, err error)

	// StartHealthMonitor spawns background liveness checks and automatic reconnection.
	StartHealthMonitor(ctx context.Context)

	// Stop tears down the tunnel/relay.
	Stop() error

	// PublicURL returns the current public URL, or "" if not running.
	PublicURL() string
}

// TunnelBackendInfoProvider is implemented by backends that expose transport metadata.
type TunnelBackendInfoProvider interface {
	Metadata() TunnelBackendInfo
}

// ConnectivityCheckResult contains the result of network connectivity checks.
type ConnectivityCheckResult struct {
	// CanReachRevylAPI indicates if the Revyl backend is reachable.
	CanReachRevylAPI bool

	// CanResolveDNS indicates if DNS resolution is working.
	CanResolveDNS bool

	// BlockedBy describes what is blocking connectivity ("dns", "firewall", "proxy", "unknown").
	BlockedBy string

	// Suggestion contains a helpful message for the user.
	Suggestion string
}

// CheckConnectivity performs pre-flight checks before attempting hot reload startup.
func CheckConnectivity(ctx context.Context) (*ConnectivityCheckResult, error) {
	result := &ConnectivityCheckResult{}

	backendURL, err := url.Parse(config.GetBackendURL(false))
	if err != nil {
		return nil, fmt.Errorf("failed to parse backend url: %w", err)
	}
	host := strings.TrimSpace(backendURL.Hostname())
	if host == "" {
		return nil, fmt.Errorf("backend host is empty")
	}

	if _, err := net.LookupHost(host); err != nil {
		result.BlockedBy = "dns"
		result.Suggestion = "DNS resolution failed. Your network may be blocking external DNS queries."
		return result, nil
	}
	result.CanResolveDNS = true

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(config.GetBackendURL(false), "/")+"/health_check", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		result.BlockedBy = "firewall"
		result.Suggestion = "Cannot reach the Revyl backend. Your firewall may be blocking outbound HTTPS."
		return result, nil
	}
	resp.Body.Close()
	result.CanReachRevylAPI = true

	return result, nil
}

// DiagnoseAndSuggest provides user-friendly error messages for network issues.
func DiagnoseAndSuggest(result *ConnectivityCheckResult) string {
	if result == nil {
		return ""
	}
	if result.CanReachRevylAPI && result.CanResolveDNS {
		return ""
	}

	var msg strings.Builder
	msg.WriteString("Network connectivity issue detected.\n\n")

	switch result.BlockedBy {
	case "dns":
		msg.WriteString("Problem: DNS resolution is failing.\n")
		msg.WriteString("This often happens on corporate networks with restricted DNS.\n\n")
		msg.WriteString("Suggestions:\n")
		msg.WriteString("  1. Try using a different network (e.g., mobile hotspot)\n")
		msg.WriteString("  2. Ask your IT team to allow DNS queries for *.revyl.ai\n")
		msg.WriteString("  3. Try setting DNS to 1.1.1.1 or 8.8.8.8\n")

	case "firewall":
		msg.WriteString("Problem: Outbound HTTPS connections are being blocked.\n")
		msg.WriteString("Your corporate firewall may be restricting access.\n\n")
		msg.WriteString("Suggestions:\n")
		msg.WriteString("  1. Try using a different network (e.g., mobile hotspot)\n")
		msg.WriteString("  2. Ask your IT team to allowlist *.revyl.ai\n")
		msg.WriteString("  3. If using a VPN, try disconnecting\n")

	case "proxy":
		msg.WriteString("Problem: A proxy is interfering with connections.\n\n")
		msg.WriteString("Suggestions:\n")
		msg.WriteString("  1. Try bypassing the proxy for *.revyl.ai\n")
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
