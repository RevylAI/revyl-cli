package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newDeviceTestCommand(t *testing.T, devMode bool) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{Use: "device"}
	cmd.Flags().Bool("dev", false, "")
	if devMode {
		if err := cmd.Flags().Set("dev", "true"); err != nil {
			t.Fatalf("set --dev flag: %v", err)
		}
	}
	return cmd
}

func TestDeviceCommandPrefix(t *testing.T) {
	t.Parallel()

	cmd := newDeviceTestCommand(t, false)
	if got := deviceCommandPrefix(cmd); got != "revyl" {
		t.Fatalf("deviceCommandPrefix() = %q, want %q", got, "revyl")
	}

	devCmd := newDeviceTestCommand(t, true)
	if got := deviceCommandPrefix(devCmd); got != "revyl --dev" {
		t.Fatalf("deviceCommandPrefix() with --dev = %q, want %q", got, "revyl --dev")
	}
}

func TestHumanizeDeviceSessionResolveError_NoSessionAtIndex(t *testing.T) {
	t.Parallel()

	cmd := newDeviceTestCommand(t, false)
	inputErr := fmt.Errorf("no session at index 0. Call list_device_sessions() to see active sessions")

	err := humanizeDeviceSessionResolveError(cmd, inputErr)
	if err == nil {
		t.Fatal("humanizeDeviceSessionResolveError() error = nil, want non-nil")
	}
	got := err.Error()
	if !strings.Contains(got, "Run 'revyl device list' to see active sessions") {
		t.Fatalf("error = %q, want CLI list guidance", got)
	}
	if strings.Contains(got, "list_device_sessions()") {
		t.Fatalf("error = %q, should not contain MCP function names", got)
	}
}

func TestHumanizeDeviceSessionResolveError_NoActiveSessions(t *testing.T) {
	t.Parallel()

	cmd := newDeviceTestCommand(t, false)
	inputErr := fmt.Errorf("no active device sessions. Start one with start_device_session(platform='ios') or start_device_session(platform='android')")

	err := humanizeDeviceSessionResolveError(cmd, inputErr)
	if err == nil {
		t.Fatal("humanizeDeviceSessionResolveError() error = nil, want non-nil")
	}
	got := err.Error()
	if !strings.Contains(got, "Start one with 'revyl device start'") {
		t.Fatalf("error = %q, want CLI start guidance", got)
	}
	if strings.Contains(got, "start_device_session(") {
		t.Fatalf("error = %q, should not contain MCP function names", got)
	}
}

func TestHumanizeDeviceSessionResolveError_MultipleSessions(t *testing.T) {
	t.Parallel()

	cmd := newDeviceTestCommand(t, false)
	inputErr := fmt.Errorf("multiple sessions active. Specify session_index or call list_device_sessions() to see them")

	err := humanizeDeviceSessionResolveError(cmd, inputErr)
	if err == nil {
		t.Fatal("humanizeDeviceSessionResolveError() error = nil, want non-nil")
	}
	got := err.Error()
	if !strings.Contains(got, "Specify -s <index> or run 'revyl device list' to see active sessions") {
		t.Fatalf("error = %q, want CLI multi-session guidance", got)
	}
	if strings.Contains(got, "session_index") || strings.Contains(got, "list_device_sessions()") {
		t.Fatalf("error = %q, should not contain MCP wording", got)
	}
}

func TestHumanizeDeviceSessionResolveError_DevModeCommandPrefix(t *testing.T) {
	t.Parallel()

	cmd := newDeviceTestCommand(t, true)
	inputErr := fmt.Errorf("no session at index 2. Call list_device_sessions() to see active sessions")

	err := humanizeDeviceSessionResolveError(cmd, inputErr)
	if err == nil {
		t.Fatal("humanizeDeviceSessionResolveError() error = nil, want non-nil")
	}
	got := err.Error()
	if !strings.Contains(got, "Run 'revyl --dev device list' to see active sessions") {
		t.Fatalf("error = %q, want dev-mode CLI guidance", got)
	}
}
