package devpush

import (
	"context"
	"fmt"

	"github.com/revyl/cli/internal/mcp"
)

// RelayTransport delivers artifacts via direct binary streaming through the
// relay service. The worker receives data on a local filesystem path, avoiding
// the S3 round-trip.
//
// Not yet implemented — this is a compile-time placeholder so the interface
// stays satisfied and the relay can be wired in without touching any calling
// code.
type RelayTransport struct{}

// PushArtifact streams binary data directly to the worker through the relay.
func (t *RelayTransport) PushArtifact(_ context.Context, _ *mcp.DeviceSession, _ []byte) (ArtifactRef, error) {
	return ArtifactRef{}, fmt.Errorf("relay transport not yet implemented")
}

// SendInstall sends an install request through the relay with a local path.
func (t *RelayTransport) SendInstall(_ context.Context, _ *mcp.DeviceSession, _ ArtifactRef, _ InstallOpts) (*InstallResult, error) {
	return nil, fmt.Errorf("relay transport not yet implemented")
}
