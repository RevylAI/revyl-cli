package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcppkg "github.com/revyl/cli/internal/mcp"
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

func TestBuildCodeExecutionLiveStepRequest(t *testing.T) {
	t.Parallel()

	got := buildCodeExecutionLiveStepRequest("  script-123  ", "")
	want := mcppkg.LiveStepRequest{
		StepType:        "code_execution",
		StepDescription: "script-123",
	}

	if got.StepType != want.StepType {
		t.Fatalf("step type = %q, want %q", got.StepType, want.StepType)
	}
	if got.StepDescription != want.StepDescription {
		t.Fatalf("step description = %q, want %q", got.StepDescription, want.StepDescription)
	}
	if got.Metadata != nil {
		t.Fatalf("metadata = %#v, want nil", got.Metadata)
	}
}

func TestBuildCodeExecutionLiveStepRequest_WithVariableName(t *testing.T) {
	t.Parallel()

	got := buildCodeExecutionLiveStepRequest("  script-123  ", "  capture  ")
	if got.StepType != "code_execution" {
		t.Fatalf("step type = %q, want %q", got.StepType, "code_execution")
	}
	if got.StepDescription != "script-123" {
		t.Fatalf("step description = %q, want %q", got.StepDescription, "script-123")
	}
	if got.Metadata["variable_name"] != "capture" {
		t.Fatalf("variable_name = %#v, want %q", got.Metadata["variable_name"], "capture")
	}
}

func TestBuildLocalVarLiveStepRequest_Set(t *testing.T) {
	t.Parallel()

	got := buildLocalVarLiveStepRequest("set", "  capture  ", "  42  ")
	if got.StepType != "local_var" {
		t.Fatalf("step type = %q, want %q", got.StepType, "local_var")
	}
	if got.StepDescription != "local-var set" {
		t.Fatalf("step description = %q, want %q", got.StepDescription, "local-var set")
	}
	if got.Metadata["variable_name"] != "capture" {
		t.Fatalf("variable_name = %#v, want %q", got.Metadata["variable_name"], "capture")
	}
	if got.Metadata["variable_value"] != "  42  " {
		t.Fatalf("variable_value = %#v, want %q", got.Metadata["variable_value"], "  42  ")
	}
	if got.Metadata["operation"] != "set" {
		t.Fatalf("operation = %#v, want %q", got.Metadata["operation"], "set")
	}
}

func TestBuildLocalVarLiveStepRequest_List(t *testing.T) {
	t.Parallel()

	got := buildLocalVarLiveStepRequest("list", "", "")
	if got.StepType != "local_var" {
		t.Fatalf("step type = %q, want %q", got.StepType, "local_var")
	}
	if got.Metadata["operation"] != "list" {
		t.Fatalf("operation = %#v, want %q", got.Metadata["operation"], "list")
	}
	if _, ok := got.Metadata["variable_name"]; ok {
		t.Fatalf("variable_name present for list request: %#v", got.Metadata)
	}
	if _, ok := got.Metadata["variable_value"]; ok {
		t.Fatalf("variable_value present for list request: %#v", got.Metadata)
	}
}

func TestDeviceLocalVarFlagsRegistered(t *testing.T) {
	t.Parallel()

	if flag := deviceCodeExecutionCmd.Flags().Lookup("variable-name"); flag == nil {
		t.Fatal("expected --variable-name flag on device code-execution command")
	}
	if flag := deviceLocalVarSetCmd.Flags().Lookup("variable-name"); flag == nil {
		t.Fatal("expected --variable-name flag on device local-var set command")
	}
	if flag := deviceLocalVarSetCmd.Flags().Lookup("value"); flag == nil {
		t.Fatal("expected --value flag on device local-var set command")
	}
	if flag := deviceLocalVarGetCmd.Flags().Lookup("variable-name"); flag == nil {
		t.Fatal("expected --variable-name flag on device local-var get command")
	}
	if flag := deviceLocalVarDeleteCmd.Flags().Lookup("variable-name"); flag == nil {
		t.Fatal("expected --variable-name flag on device local-var delete command")
	}
}

func TestFormatLiveStepFallback_LocalVarList(t *testing.T) {
	t.Parallel()

	response := &mcppkg.LiveStepResponse{
		Success:  true,
		StepType: "local_var",
		StepID:   "step-1",
		StepOutput: json.RawMessage(
			`{"status":"success","variables":{"zeta":"3","account_label":"blank"}}`,
		),
	}

	got := formatLiveStepFallback(
		"Local var",
		mcppkg.LiveStepRequest{
			StepType: "local_var",
			Metadata: map[string]any{"operation": "list"},
		},
		response,
	)

	want := "Local vars:\naccount_label=blank\nzeta=3"
	if got != want {
		t.Fatalf("formatLiveStepFallback() = %q, want %q", got, want)
	}
}

func TestFormatLiveStepFallback_LocalVarGet(t *testing.T) {
	t.Parallel()

	response := &mcppkg.LiveStepResponse{
		Success:  true,
		StepType: "local_var",
		StepID:   "step-1",
		StepOutput: json.RawMessage(
			`{"status":"success","variable_name":"account_label","variable_value":"blank"}`,
		),
	}

	got := formatLiveStepFallback(
		"Local var",
		mcppkg.LiveStepRequest{
			StepType: "local_var",
			Metadata: map[string]any{"operation": "get"},
		},
		response,
	)

	if got != "account_label=blank" {
		t.Fatalf("formatLiveStepFallback() = %q, want %q", got, "account_label=blank")
	}
}

func TestFormatLiveStepFallback_LocalVarDelete(t *testing.T) {
	t.Parallel()

	response := &mcppkg.LiveStepResponse{
		Success:  true,
		StepType: "local_var",
		StepID:   "step-1",
		StepOutput: json.RawMessage(
			`{"status":"success","variable_name":"account_label","variable_value":"blank"}`,
		),
	}

	got := formatLiveStepFallback(
		"Local var",
		mcppkg.LiveStepRequest{
			StepType: "local_var",
			Metadata: map[string]any{"operation": "delete"},
		},
		response,
	)

	if got != "Deleted account_label (was blank)" {
		t.Fatalf("formatLiveStepFallback() = %q, want %q", got, "Deleted account_label (was blank)")
	}
}

func TestFormatLiveStepFallback_ExtractWithoutVariableName(t *testing.T) {
	t.Parallel()

	response := &mcppkg.LiveStepResponse{
		Success:  true,
		StepType: "extract",
		StepID:   "step-1",
		StepOutput: json.RawMessage(
			`{"status":"success","extracted_data":{"information":"blank"}}`,
		),
	}

	got := formatLiveStepFallback(
		"Extract",
		mcppkg.LiveStepRequest{
			StepType: "extract",
		},
		response,
	)

	if got != "blank" {
		t.Fatalf("formatLiveStepFallback() = %q, want %q", got, "blank")
	}
}

func TestFormatLiveStepFallback_ExtractWithVariableName(t *testing.T) {
	t.Parallel()

	response := &mcppkg.LiveStepResponse{
		Success:  true,
		StepType: "extract",
		StepID:   "step-1",
		StepOutput: json.RawMessage(
			`{"status":"success","extracted_data":{"information":"blank"}}`,
		),
	}

	got := formatLiveStepFallback(
		"Extract",
		mcppkg.LiveStepRequest{
			StepType: "extract",
			Metadata: map[string]any{"variable_name": "account_label"},
		},
		response,
	)

	if got != "account_label=blank" {
		t.Fatalf("formatLiveStepFallback() = %q, want %q", got, "account_label=blank")
	}
}

func TestFormatLiveStepFallback_CodeExecutionWithoutVariableName(t *testing.T) {
	t.Parallel()

	response := &mcppkg.LiveStepResponse{
		Success:  true,
		StepType: "code_execution",
		StepID:   "step-1",
		StepOutput: json.RawMessage(
			`{"status":"success","metadata":{"stdout":"blank\n"}}`,
		),
	}

	got := formatLiveStepFallback(
		"Code execution",
		mcppkg.LiveStepRequest{
			StepType: "code_execution",
		},
		response,
	)

	if got != "blank\n" {
		t.Fatalf("formatLiveStepFallback() = %q, want %q", got, "blank\n")
	}
}

func TestFormatLiveStepFallback_CodeExecutionWithVariableName(t *testing.T) {
	t.Parallel()

	response := &mcppkg.LiveStepResponse{
		Success:  true,
		StepType: "code_execution",
		StepID:   "step-1",
		StepOutput: json.RawMessage(
			`{"status":"success","metadata":{"stdout":"blank\n"}}`,
		),
	}

	got := formatLiveStepFallback(
		"Code execution",
		mcppkg.LiveStepRequest{
			StepType: "code_execution",
			Metadata: map[string]any{"variable_name": "account_label"},
		},
		response,
	)

	if got != "account_label=blank\n" {
		t.Fatalf("formatLiveStepFallback() = %q, want %q", got, "account_label=blank\n")
	}
}
