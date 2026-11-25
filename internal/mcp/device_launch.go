package mcp

import (
	"context"
	"fmt"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/launcharguments"
	"github.com/revyl/cli/internal/launchenv"
)

// LaunchAppOnSession launches an app on a session's device. Launch values
// restart the app and apply to that one launch, so they need a device worker
// that advertises supports_per_launch_values.
func (m *DeviceSessionManager) LaunchAppOnSession(ctx context.Context, session *DeviceSession, request api.DeviceLaunchRequest) ([]byte, error) {
	if len(request.LaunchEnvVars) == 0 && len(request.LaunchArguments) == 0 {
		return m.WorkerRequestOnSession(ctx, session, "/launch", request)
	}
	if session == nil {
		return nil, fmt.Errorf("no session resolved for launch")
	}
	if err := CheckDeviceActionPlatform(session, "launch"); err != nil {
		return nil, err
	}
	if err := launchenv.Validate(request.LaunchEnvVars); err != nil {
		return nil, err
	}
	if err := launcharguments.Validate(request.LaunchArguments); err != nil {
		return nil, err
	}
	if len(request.LaunchArguments) > 0 && session.Platform != "ios" {
		return nil, fmt.Errorf("launch arguments are iOS-only; Android apps take launch environment variables")
	}
	healthBody, err := m.WorkerRequestOnSession(ctx, session, "/health", nil)
	if err != nil {
		return nil, fmt.Errorf("could not check whether this device accepts launch values for one launch: %w", err)
	}
	health, err := parseWorkerHealth(healthBody)
	if err != nil {
		return nil, err
	}
	if !health.SupportsPerLaunchValues {
		return nil, fmt.Errorf("this session's device worker can't apply launch values to a single launch yet; set them when you start the session instead")
	}
	return m.WorkerRequestOnSession(ctx, session, "/launch", request)
}
