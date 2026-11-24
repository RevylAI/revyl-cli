package hotreload

import (
	"context"
)

type DevServerOutputStream string

const (
	DevServerOutputStdout DevServerOutputStream = "stdout"
	DevServerOutputStderr DevServerOutputStream = "stderr"

	// DevServerOutputHMR indicates an HMR event synthesized from dev-server output.
	DevServerOutputHMR DevServerOutputStream = "hmr"
)

type DevServerOutput struct {
	Stream DevServerOutputStream
	Line   string
}

// MaxDevServerOutputLineBytes bounds one dev-server output line, so a long
// bundler line is still read instead of stopping the stdout reader.
const MaxDevServerOutputLineBytes = 16 * 1024 * 1024

type DevServerOutputCallback func(output DevServerOutput)

// DevServer manages the lifecycle of a local development server for hot reload.
type DevServer interface {
	Start(ctx context.Context) error
	Stop() error
	GetPort() int
	GetDeepLinkURL(tunnelURL string) string
	Name() string
	// SetProxyURL configures the public tunnel URL for bundle URL rewriting; must be called before Start.
	SetProxyURL(tunnelURL string)
}

type DevServerOutputEmitter interface {
	// SetOutputCallback registers a process output callback; nil disables streaming.
	SetOutputCallback(callback DevServerOutputCallback)
}

type DevServerDebugConfigurable interface {
	SetDebugMode(enabled bool)
}

type DevServerFailureReporter interface {
	Failures() <-chan RuntimeFailure
}

type DevServerStatus string

const (
	DevServerStatusStopped  DevServerStatus = "stopped"
	DevServerStatusStarting DevServerStatus = "starting"
	DevServerStatusRunning  DevServerStatus = "running"
	DevServerStatusError    DevServerStatus = "error"
)
