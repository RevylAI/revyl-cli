// Package api provides fleet sandbox API client methods.
//
// These methods communicate with the Fleet Management endpoints
// at /api/v1/fleet/* on the Revyl backend.
package api

import (
	"context"
	"fmt"
)

// GetFleetStatus retrieves the Fleet pool status summary.
// Returns aggregate counts of sandbox availability.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *FleetPoolStatus: Pool status with total/available/claimed/maintenance counts
//   - error: Any error that occurred
func (c *Client) GetFleetStatus(ctx context.Context) (*FleetPoolStatus, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/fleet/sandboxes/status", nil)
	if err != nil {
		return nil, err
	}

	var result FleetPoolStatus
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ListSandboxes retrieves all sandboxes in the Fleet pool.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - []FleetSandbox: List of all sandboxes
//   - error: Any error that occurred
func (c *Client) ListSandboxes(ctx context.Context) ([]FleetSandbox, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/fleet/sandboxes", nil)
	if err != nil {
		return nil, err
	}

	var result []FleetSandbox
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// GetFleetDashboard retrieves the combined Fleet dashboard data.
// Provides pool status, all sandboxes, user's worktrees, and tunnels in one call.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *FleetDashboardResponse: Combined dashboard data
//   - error: Any error that occurred
func (c *Client) GetFleetDashboard(ctx context.Context) (*FleetDashboardResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/fleet/sandboxes/dashboard", nil)
	if err != nil {
		return nil, err
	}

	var result FleetDashboardResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ClaimSandbox claims an available sandbox from the pool.
// Uses atomic database operations to prevent race conditions.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *ClaimSandboxResponse: The claim result with sandbox details
//   - error: Any error that occurred
func (c *Client) ClaimSandbox(ctx context.Context) (*ClaimSandboxResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/fleet/sandboxes/claim", nil)
	if err != nil {
		return nil, err
	}

	var result ClaimSandboxResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ReleaseSandbox releases a claimed sandbox back to the pool.
// The caller must be the owner of the sandbox (or an admin when force=true).
//
// Parameters:
//   - ctx: Context for cancellation
//   - sandboxID: The UUID of the sandbox to release
//   - force: If true, skip ownership check (admin only)
//
// Returns:
//   - *ReleaseSandboxResponse: The release result
//   - error: Any error that occurred
func (c *Client) ReleaseSandbox(ctx context.Context, sandboxID string, force bool) (*ReleaseSandboxResponse, error) {
	path := fmt.Sprintf("/api/v1/fleet/sandboxes/%s/release", sandboxID)
	if force {
		path += "?force=true"
	}

	resp, err := c.doRequest(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}

	var result ReleaseSandboxResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetMySandboxes retrieves the authenticated user's claimed sandboxes.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - []FleetSandbox: List of user's claimed sandboxes
//   - error: Any error that occurred
func (c *Client) GetMySandboxes(ctx context.Context) ([]FleetSandbox, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/fleet/sandboxes/mine", nil)
	if err != nil {
		return nil, err
	}

	var result []FleetSandbox
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// PushSSHKey pushes an SSH public key to a sandbox.
// The backend SSHes into the sandbox and adds the key to authorized_keys.
//
// Parameters:
//   - ctx: Context for cancellation
//   - sandboxID: The UUID of the sandbox
//   - publicKey: The SSH public key content (e.g., "ssh-ed25519 AAAA... user@host")
//
// Returns:
//   - *PushSSHKeyResponse: The push result
//   - error: Any error that occurred
func (c *Client) PushSSHKey(ctx context.Context, sandboxID, publicKey string) (*PushSSHKeyResponse, error) {
	req := &PushSSHKeyRequest{PublicKey: publicKey}
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/fleet/sandboxes/%s/ssh-key", sandboxID), req)
	if err != nil {
		return nil, err
	}

	var result PushSSHKeyResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetSSHKeyStatus checks whether an SSH key is configured on a sandbox.
//
// Parameters:
//   - ctx: Context for cancellation
//   - sandboxID: The UUID of the sandbox
//
// Returns:
//   - *SSHKeyStatusResponse: The SSH key status
//   - error: Any error that occurred
func (c *Client) GetSSHKeyStatus(ctx context.Context, sandboxID string) (*SSHKeyStatusResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/fleet/sandboxes/%s/ssh-key/status", sandboxID), nil)
	if err != nil {
		return nil, err
	}

	var result SSHKeyStatusResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}
