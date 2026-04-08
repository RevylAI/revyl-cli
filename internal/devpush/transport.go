// Package devpush abstracts artifact delivery and fast-install commands
// between the CLI and device workers. The transport layer is pluggable so the
// same dev-loop code works with the current proxy+S3 stack and a future relay
// service.
package devpush

import (
	"context"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/mcp"
)

// WorkerTransport abstracts how the CLI delivers binary artifacts and install
// commands to a worker. Today the only implementation uploads to S3 and sends
// the URL through the backend proxy. When the relay service ships, a second
// implementation will stream bytes directly.
type WorkerTransport interface {
	// PushArtifact delivers binary data to a worker and returns a reference
	// the worker can use to access it (presigned URL for S3, local filesystem
	// path for relay).
	//
	// Parameters:
	//   - ctx: cancellation context
	//   - session: target device session
	//   - data: raw bytes (typically a zip of changed .app files)
	//
	// Returns:
	//   - ArtifactRef: transport-specific handle the worker understands
	//   - error: upload or streaming failure
	PushArtifact(ctx context.Context, session *mcp.DeviceSession, data []byte) (ArtifactRef, error)

	// SendInstall tells the worker to install from the pushed artifact using
	// the specified install strategy (delta patch, fast install, or default).
	//
	// Parameters:
	//   - ctx: cancellation context
	//   - session: target device session
	//   - ref: artifact reference returned by PushArtifact
	//   - opts: install mode, bundle ID, platform
	//
	// Returns:
	//   - InstallResult: outcome including install method and data-preservation flag
	//   - error: worker or transport error
	SendInstall(ctx context.Context, session *mcp.DeviceSession, ref ArtifactRef, opts InstallOpts) (*InstallResult, error)
}

// ArtifactRef is a transport-agnostic reference to pushed data. Exactly one
// field is populated depending on the transport.
type ArtifactRef struct {
	URL  string // presigned S3 URL (proxy transport)
	Path string // local filesystem path on worker (relay transport)
}

// InstallOpts configures worker-side install behaviour.
type InstallOpts struct {
	// Mode selects the install strategy: "delta" patches cached .app,
	// "fast" replaces it fully but uses hot-swap, "" uses the default
	// simctl/adb install path.
	Mode string

	// BundleID is the app identifier for launch after install.
	BundleID string

	// Platform is "ios" or "android".
	Platform string

	// DeletedFiles lists relative paths to remove from the cached app
	// before applying a delta zip. Only relevant for mode="delta".
	DeletedFiles []string
}

// InstallResult carries the worker's response after an install action.
type InstallResult struct {
	Success       bool    `json:"success"`
	BundleID      string  `json:"bundle_id,omitempty"`
	DataPreserved bool    `json:"data_preserved,omitempty"`
	InstallMethod string  `json:"install_method,omitempty"` // "hot_swap", "fastdeploy", "simctl_install"
	LatencyMs     float64 `json:"latency_ms"`
	Error         string  `json:"error,omitempty"`
}

// NewTransport returns the appropriate WorkerTransport for the current
// configuration. Today this always returns a ProxyS3Transport; when the relay
// service is available, it will inspect config or feature flags.
//
// Parameters:
//   - apiClient: authenticated API client for presigned URL requests
//   - sessionMgr: device session manager for proxied worker requests
func NewTransport(apiClient *api.Client, sessionMgr *mcp.DeviceSessionManager) WorkerTransport {
	return &ProxyS3Transport{apiClient: apiClient, sessionMgr: sessionMgr}
}
