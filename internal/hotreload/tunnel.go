package hotreload

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/revyl/cli/internal/backendheaders"
	"github.com/revyl/cli/internal/config"
)

type TunnelBackendInfo struct {
	Transport string
	RelayID   string
}

// TunnelBackend exposes a local dev server port to a target device.
type TunnelBackend interface {
	Start(ctx context.Context, localPort int) (publicURL string, err error)
	StartHealthMonitor(ctx context.Context)
	Stop() error
	PublicURL() string
}

type TunnelBackendInfoProvider interface {
	Metadata() TunnelBackendInfo
}

type RelayReacquireResult struct {
	TunnelURL string
	RelayID   string
	Transport string
}

type TunnelBackendReacquirer interface {
	Reacquire(ctx context.Context) (*RelayReacquireResult, error)
}

type TunnelFailureReporter interface {
	Failures() <-chan RuntimeFailure
}

// ExternalTunnelBackend wraps a caller-managed tunnel URL as a no-op TunnelBackend.
type ExternalTunnelBackend struct {
	publicURL string
}

func NewExternalTunnelBackend(publicURL string) *ExternalTunnelBackend {
	return &ExternalTunnelBackend{publicURL: publicURL}
}

func (e *ExternalTunnelBackend) Start(_ context.Context, _ int) (string, error) {
	return e.publicURL, nil
}

func (e *ExternalTunnelBackend) StartHealthMonitor(_ context.Context) {}

func (e *ExternalTunnelBackend) Stop() error { return nil }

func (e *ExternalTunnelBackend) PublicURL() string { return e.publicURL }

func (e *ExternalTunnelBackend) Metadata() TunnelBackendInfo {
	return TunnelBackendInfo{Transport: "external"}
}

// TransportLocal serves the dev server at 127.0.0.1 for a device sharing loopback.
const TransportLocal = "local"

type LocalTunnelBackend struct {
	url string
}

func (l *LocalTunnelBackend) Start(_ context.Context, localPort int) (string, error) {
	l.url = fmt.Sprintf("http://127.0.0.1:%d", localPort)
	return l.url, nil
}

func (l *LocalTunnelBackend) StartHealthMonitor(_ context.Context) {}

func (l *LocalTunnelBackend) Stop() error { return nil }

func (l *LocalTunnelBackend) PublicURL() string { return l.url }

func (l *LocalTunnelBackend) Metadata() TunnelBackendInfo {
	return TunnelBackendInfo{Transport: TransportLocal}
}

type ConnectivityCheckResult struct {
	CanReachRevylAPI bool
	CanResolveDNS    bool
	BlockedBy        string
	Suggestion       string
}

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
	backendheaders.SetCloudAgentConversationContext(req)

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
