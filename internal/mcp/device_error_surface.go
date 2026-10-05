package mcp

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
		nextStepScreenshot:           "Try screenshot() to see the current screen and adjust the target description",
		nextStepDiagnose:             "Call device_doctor() to check worker health",
		nextStepDeviceConnecting:     "The device may not be fully connected yet -- wait a few seconds and retry, or call device_doctor() to diagnose",
		nextStepRetryStart:           "Try again or call device_doctor() to diagnose",
		nextStepRetryWhenIdle:        "Wait for it to finish, then retry; call screenshot() to see the current screen",
	},
	ErrorSurfaceCLI: {
		nextStepListSessions:         "Run 'revyl device list' to see active sessions",
		nextStepSelectSession:        "Specify -s <index or session ID> or run 'revyl device list --json' to see active sessions and their IDs",
		nextStepStartSession:         "Start one with 'revyl device start' (add --platform android for Android)",
		nextStepStartNewSession:      "Start a new session with 'revyl device start'",
		nextStepStartPlatformSession: "Start a new one with 'revyl device start --platform %s'",
		nextStepScreenshot:           "Run 'revyl device screenshot --out screen.png' to see the current screen, then adjust --target",
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
