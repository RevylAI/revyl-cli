//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// TestDeviceLifecycle exercises a device session lifecycle: start -> info ->
// doctor -> basic interactions -> screenshot -> stop.
//
// Gated by REVYL_E2E_DEVICE=true because device sessions are slow and expensive.
func TestDeviceLifecycle(t *testing.T) {
	if os.Getenv("REVYL_E2E_DEVICE") != "true" {
		t.Skip("REVYL_E2E_DEVICE not set; skipping device tests (slow/expensive)")
	}

	platform := os.Getenv("REVYL_E2E_DEVICE_PLATFORM")
	if platform == "" {
		platform = "ios"
	}

	var sessionIndex string
	var workflowRunID string

	step(t, "start_device_session", func(st *testing.T) {
		result := runCLI(t, "device", "start", "--platform", platform, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("device start failed: %s\n%s", result.Stdout, result.Stderr)
		}

		var resp struct {
			Index         int    `json:"index"`
			WorkflowRunID string `json:"workflow_run_id"`
		}
		raw := extractJSON(result.Stdout)
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			st.Fatalf("failed to parse device start response: %v", err)
		}

		sessionIndex = strconv.Itoa(resp.Index)
		workflowRunID = resp.WorkflowRunID
		st.Logf("session started: index=%s wfRunID=%s", sessionIndex, workflowRunID)

		t.Cleanup(func() {
			_ = runCLI(t, "device", "stop", "-s", sessionIndex)
		})
	})

	step(t, "device_info", func(st *testing.T) {
		result := runCLI(t, "device", "info", "-s", sessionIndex, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("device info failed: %s\n%s", result.Stdout, result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("device info is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "device_doctor", func(st *testing.T) {
		result := runCLI(t, "device", "doctor", "-s", sessionIndex)
		if result.ExitCode != 0 {
			st.Fatalf("device doctor failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "device_screenshot", func(st *testing.T) {
		result := runCLI(t, "device", "screenshot", "-s", sessionIndex, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("device screenshot failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "device_tap", func(st *testing.T) {
		result := runCLI(t, "device", "tap", "-s", sessionIndex, "--x", "200", "--y", "400", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("device tap failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "device_swipe", func(st *testing.T) {
		result := runCLI(t, "device", "swipe", "-s", sessionIndex, "--x", "200", "--y", "400", "--direction", "down", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("device swipe failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "device_home", func(st *testing.T) {
		result := runCLI(t, "device", "home", "-s", sessionIndex, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("device home failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "device_wait", func(st *testing.T) {
		result := runCLI(t, "device", "wait", "-s", sessionIndex, "--duration-ms", "500", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("device wait failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "stop_device_session", func(st *testing.T) {
		result := runCLI(t, "device", "stop", "-s", sessionIndex)
		if result.ExitCode != 0 {
			st.Fatalf("device stop failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})
}
