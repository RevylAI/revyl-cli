// Package sandbox provides SSH utilities for Fleet sandbox operations.
//
// This package handles SSH connections to Fleet sandboxes via Cloudflare tunnels,
// including interactive sessions and remote command execution.
package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/revyl/cli/internal/api"
)

// SSHConfig holds the resolved SSH connection parameters for a sandbox.
type SSHConfig struct {
	// User is the SSH username (e.g., "revyl-admin").
	User string

	// Host is the Cloudflare tunnel hostname (e.g., "sandbox-1.fleet.revyl.dev").
	Host string

	// Port is the SSH port number (default: 22).
	Port int
}

// ResolveSSHConfig builds SSH connection parameters from a sandbox model.
// Uses the tunnel hostname for connectivity via Cloudflare Access.
//
// Parameters:
//   - sandbox: The sandbox to connect to
//
// Returns:
//   - *SSHConfig: Resolved SSH connection parameters
//   - error: If the sandbox has no tunnel hostname configured
func ResolveSSHConfig(sandbox *api.FleetSandbox) (*SSHConfig, error) {
	tunnelHost := sandbox.EffectiveTunnelHostname()
	if tunnelHost == "" {
		return nil, fmt.Errorf("sandbox %q has no tunnel hostname configured", sandbox.DisplayName())
	}

	return &SSHConfig{
		User: sandbox.EffectiveSSHUser(),
		Host: tunnelHost,
		Port: sandbox.EffectiveSSHPort(),
	}, nil
}

// proxyCommand returns the cloudflared ProxyCommand string for SSH.
//
// Parameters:
//   - host: The tunnel hostname
//
// Returns:
//   - string: The ProxyCommand value for ssh -o
func proxyCommand(host string) string {
	return fmt.Sprintf("cloudflared access ssh --hostname %s", host)
}

// sshBaseArgs returns the common SSH arguments for connecting via Cloudflare tunnel.
//
// Parameters:
//   - cfg: The SSH config
//
// Returns:
//   - []string: Base SSH arguments
func sshBaseArgs(cfg *SSHConfig) []string {
	return []string{
		"-o", fmt.Sprintf("ProxyCommand=%s", proxyCommand(cfg.Host)),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-p", fmt.Sprintf("%d", cfg.Port),
		fmt.Sprintf("%s@%s", cfg.User, cfg.Host),
	}
}

// SSHToSandbox opens an interactive SSH session to a sandbox.
// Attaches stdin/stdout/stderr for a full terminal experience.
//
// Parameters:
//   - sandbox: The sandbox to connect to
//
// Returns:
//   - error: Any error that occurred (including non-zero exit codes)
func SSHToSandbox(sandbox *api.FleetSandbox) error {
	cfg, err := ResolveSSHConfig(sandbox)
	if err != nil {
		return err
	}

	if err := EnsureCloudflared(); err != nil {
		return err
	}

	args := sshBaseArgs(cfg)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// SSHExec executes a command on a sandbox via SSH and returns the output.
// Does not attach stdin — use SSHToSandbox for interactive sessions.
//
// Parameters:
//   - sandbox: The sandbox to run the command on
//   - command: The shell command to execute
//
// Returns:
//   - string: Combined stdout output (trimmed)
//   - error: Any error that occurred (includes stderr in error message)
func SSHExec(sandbox *api.FleetSandbox, command string) (string, error) {
	cfg, err := ResolveSSHConfig(sandbox)
	if err != nil {
		return "", err
	}

	if err := EnsureCloudflared(); err != nil {
		return "", err
	}

	args := append(sshBaseArgs(cfg), "--", command)
	cmd := exec.Command("ssh", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return "", fmt.Errorf("SSH command failed: %w\nstderr: %s", err, stderrStr)
		}
		return "", fmt.Errorf("SSH command failed: %w", err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// EnsureCloudflared checks that cloudflared is installed and available on PATH.
// Provides installation instructions if not found.
//
// Returns:
//   - error: If cloudflared is not found, with installation instructions
func EnsureCloudflared() error {
	_, err := exec.LookPath("cloudflared")
	if err != nil {
		msg := "cloudflared is required for SSH access to sandboxes but was not found on your PATH"
		switch runtime.GOOS {
		case "darwin":
			msg += "\n\nInstall with Homebrew:\n  brew install cloudflared"
		case "linux":
			msg += "\n\nInstall instructions:\n  https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/installation/"
		default:
			msg += "\n\nSee: https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/installation/"
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// DefaultSSHPublicKeyPath returns the path to the user's default SSH public key.
// Tries ed25519 first, then RSA.
//
// Returns:
//   - string: Path to the SSH public key file
//   - error: If no SSH key is found
func DefaultSSHPublicKeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}

	candidates := []string{
		filepath.Join(home, ".ssh", "id_ed25519.pub"),
		filepath.Join(home, ".ssh", "id_rsa.pub"),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("no SSH public key found at ~/.ssh/id_ed25519.pub or ~/.ssh/id_rsa.pub\n\nGenerate one with:\n  ssh-keygen -t ed25519")
}

// ReadSSHPublicKey reads the contents of an SSH public key file.
//
// Parameters:
//   - path: Path to the SSH public key file
//
// Returns:
//   - string: The public key contents (trimmed)
//   - error: If the file cannot be read
func ReadSSHPublicKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read SSH public key at %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}
