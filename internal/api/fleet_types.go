// Package api provides CLI-friendly helper methods for generated Fleet types.
//
// The fleet types (FleetSandbox, FleetPoolStatus, etc.) are auto-generated
// in generated.go from the backend OpenAPI spec. This file adds ergonomic
// methods used by the CLI commands.
package api

// DisplayName returns the best human-readable name for the sandbox.
// Prefers hostname, falls back to vm_name.
//
// Returns:
//   - string: The display name
func (s *FleetSandbox) DisplayName() string {
	if s.Hostname != nil && *s.Hostname != "" {
		return *s.Hostname
	}
	return s.VmName
}

// EffectiveSSHUser returns the SSH user, defaulting to "revyl-admin".
//
// Returns:
//   - string: The SSH user
func (s *FleetSandbox) EffectiveSSHUser() string {
	if s.SshUser != nil && *s.SshUser != "" {
		return *s.SshUser
	}
	return "revyl-admin"
}

// EffectiveSSHPort returns the SSH port, defaulting to 22.
//
// Returns:
//   - int: The SSH port
func (s *FleetSandbox) EffectiveSSHPort() int {
	if s.SshPort != nil && *s.SshPort > 0 {
		return *s.SshPort
	}
	return 22
}

// EffectiveTunnelHostname returns the tunnel hostname, or empty string if nil.
//
// Returns:
//   - string: The tunnel hostname
func (s *FleetSandbox) EffectiveTunnelHostname() string {
	if s.TunnelHostname != nil {
		return *s.TunnelHostname
	}
	return ""
}

// EffectiveClaimedBy returns the claimed_by value, or empty string if nil.
//
// Returns:
//   - string: The user ID of the claimer
func (s *FleetSandbox) EffectiveClaimedBy() string {
	if s.ClaimedBy != nil {
		return *s.ClaimedBy
	}
	return ""
}

// EffectiveStatus returns the status as a string, or "unknown" if nil.
//
// Returns:
//   - string: The sandbox status
func (s *FleetSandbox) EffectiveStatus() string {
	if s.Status != nil {
		return string(*s.Status)
	}
	return "unknown"
}

// EffectiveMaintenance returns the maintenance count, or 0 if nil.
//
// Returns:
//   - int: The maintenance count
func (s *FleetPoolStatus) EffectiveMaintenance() int {
	if s.Maintenance != nil {
		return *s.Maintenance
	}
	return 0
}

// EffectiveAlreadyExists returns the already_exists value, or false if nil.
//
// Returns:
//   - bool: Whether the key already existed
func (r *PushSSHKeyResponse) EffectiveAlreadyExists() bool {
	if r.AlreadyExists != nil {
		return *r.AlreadyExists
	}
	return false
}

// EffectiveSandboxReachable returns the sandbox_reachable value, or false if nil.
//
// Returns:
//   - bool: Whether the sandbox is reachable
func (r *SSHKeyStatusResponse) EffectiveSandboxReachable() bool {
	if r.SandboxReachable != nil {
		return *r.SandboxReachable
	}
	return false
}

// EffectiveKeyFingerprint returns the key fingerprint, or empty string if nil.
//
// Returns:
//   - string: The key fingerprint
func (r *SSHKeyStatusResponse) EffectiveKeyFingerprint() string {
	if r.KeyFingerprint != nil {
		return *r.KeyFingerprint
	}
	return ""
}
