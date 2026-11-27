package mcp

import (
	"fmt"
	"strings"
)

// TargetNotFoundCode is the stable error code for a grounded action whose
// described target is not on the screen. No device action runs when it is
// returned.
const TargetNotFoundCode = "target_not_found"

// TargetNotFoundError reports that grounding found nothing on the screen that
// fits the target description, so the action was not sent to the device.
type TargetNotFoundError struct {
	Target   string
	Reason   string
	NextStep string
}

func (e *TargetNotFoundError) Error() string {
	message := fmt.Sprintf("%s: Revyl could not find %q on the screen, so nothing was done on the device", TargetNotFoundCode, e.Target)
	reason := strings.TrimSpace(e.Reason)
	if strings.HasPrefix(reason, "Could not locate ") {
		_, reason, _ = strings.Cut(reason, ". ")
	}
	if reason = strings.TrimSuffix(strings.TrimSpace(reason), "."); reason != "" {
		message += ". " + reason
	}
	return message + ". " + e.NextStep
}

func (m *DeviceSessionManager) targetNotFound(target, reason string) *TargetNotFoundError {
	return &TargetNotFoundError{Target: target, Reason: reason, NextStep: m.nextStep(nextStepScreenshot)}
}

// ErrorSurface selects how device-session errors name the next step. The
// session manager is shared by the MCP server and the CLI, and each surface
// needs guidance its caller can act on: MCP clients call tools, while shell
// users and agents driving the CLI run revyl commands.
type ErrorSurface int

const (
	// ErrorSurfaceMCP names MCP tools such as screenshot(). It is the default.
	ErrorSurfaceMCP ErrorSurface = iota

	// ErrorSurfaceCLI names copy-pasteable revyl commands.
	ErrorSurfaceCLI
)

type deviceNextStep int

const (
	nextStepListSessions deviceNextStep = iota
	nextStepSelectSession
	nextStepStartSession
	nextStepStartNewSession
	nextStepStartPlatformSession
	nextStepScreenshot
	nextStepDiagnose
	nextStepDeviceConnecting
	nextStepRetryStart
	nextStepRetryWhenIdle
)

// deviceNextStepText is the single source of follow-up guidance for
// device-session errors. Every step must have text for every surface.
var deviceNextStepText = map[ErrorSurface]map[deviceNextStep]string{
	ErrorSurfaceMCP: {
		nextStepListSessions:         "Call list_device_sessions() to see active sessions",
		nextStepSelectSession:        "Pass session_id (or session_index), or call list_device_sessions() to see them",
		nextStepStartSession:         "Start one with start_device_session(platform='ios') or start_device_session(platform='android')",
		nextStepStartNewSession:      "Start a new session with start_device_session()",
		nextStepStartPlatformSession: "Start a new one with start_device_session(platform='%s')",
		nextStepScreenshot:           "Call screenshot() and describe an element you can see as the target, or pass x and y coordinates instead",
		nextStepDiagnose:             "Call device_doctor() to check worker health",
		nextStepDeviceConnecting:     "The device may not be fully connected yet -- wait a few seconds and retry, or call device_doctor() to diagnose",
		nextStepRetryStart:           "Try again or call device_doctor() to diagnose",
		nextStepRetryWhenIdle:        "Wait for it to finish, then retry; call screenshot() to see the current screen",
	},
	ErrorSurfaceCLI: {
		nextStepListSessions:         "Run 'revyl device list' to see active sessions",
		nextStepSelectSession:        "Specify -s <index or session ID> or run 'revyl device list --json' to see active sessions and their IDs",
		nextStepStartSession:         "Start one with 'revyl device start' (use --platform ios|android|web; Web also needs --app-id and --url)",
		nextStepStartNewSession:      "Start a new session with 'revyl device start'",
		nextStepStartPlatformSession: "Start a new one with 'revyl device start --platform %s'",
		nextStepScreenshot:           "Run 'revyl device screenshot --out screen.png' and describe an element you can see in --target, or pass --x/--y coordinates instead",
		nextStepDiagnose:             "Run 'revyl device doctor' to check worker health",
		nextStepDeviceConnecting:     "The device is still connecting; wait a few seconds and retry, or run 'revyl device doctor' to check it",
		nextStepRetryStart:           "Retry 'revyl device start', or run 'revyl device doctor' to diagnose",
		nextStepRetryWhenIdle:        "Wait for it to finish, then retry; run 'revyl device screenshot --out screen.png' to see the current screen",
	},
}

// SetErrorSurface selects the surface whose commands or tools device-session
// errors name as the next step. Like SetDevMode, call it right after
// construction, before the manager is shared.
func (m *DeviceSessionManager) SetErrorSurface(surface ErrorSurface) {
	m.errorSurface = surface
}

func (m *DeviceSessionManager) nextStep(step deviceNextStep) string {
	return deviceNextStepText[m.errorSurface][step]
}
