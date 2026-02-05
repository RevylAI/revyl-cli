// Package hotreload provides hot reload functionality for rapid development iteration.
//
// Hot reload enables near-instant testing by:
//   - Starting a local dev server (Expo, Swift, or Android)
//   - Creating a Cloudflare tunnel to expose it publicly
//   - Running tests against a pre-built development client
//
// This package supports multiple dev server providers through the DevServer interface,
// allowing extensibility for different frameworks and platforms.
package hotreload

import (
	"context"
)

// DevServer defines the interface for hot reload development servers.
//
// Implementations of this interface manage the lifecycle of a local development
// server and provide the necessary information to connect a remote device to it.
//
// Current implementations:
//   - ExpoDevServer: Expo/React Native development server
//
// Future implementations:
//   - SwiftDevServer: InjectionIII for native iOS hot reload
//   - AndroidDevServer: Metro/Gradle for native Android hot reload
type DevServer interface {
	// Start launches the development server and blocks until it's ready to accept connections.
	//
	// The server should be fully initialized and accepting connections before this method returns.
	// If the server fails to start within a reasonable timeout, an error should be returned.
	//
	// Parameters:
	//   - ctx: Context for cancellation. If cancelled, the server should stop and return ctx.Err()
	//
	// Returns:
	//   - error: nil if server started successfully, otherwise the error that occurred
	Start(ctx context.Context) error

	// Stop terminates the development server and cleans up any resources.
	//
	// This method should be idempotent - calling it multiple times should not cause errors.
	// Any child processes should be terminated gracefully, with a fallback to forceful termination.
	//
	// Returns:
	//   - error: nil if server stopped successfully, otherwise the error that occurred
	Stop() error

	// GetPort returns the port number the development server is listening on.
	//
	// This port is used to configure the Cloudflare tunnel to forward traffic to the local server.
	//
	// Returns:
	//   - int: The port number (e.g., 8081 for Expo)
	GetPort() int

	// GetDeepLinkURL constructs the deep link URL for launching the development client.
	//
	// The deep link URL is used to launch the pre-built development client app on the device
	// and connect it to the local development server through the tunnel.
	//
	// Parameters:
	//   - tunnelURL: The public Cloudflare tunnel URL (e.g., "https://cog-abc123.revyl.ai")
	//
	// Returns:
	//   - string: The deep link URL (e.g., "myapp://expo-development-client/?url=https://...")
	GetDeepLinkURL(tunnelURL string) string

	// Name returns the human-readable name of the development server provider.
	//
	// This is used for logging and user-facing messages.
	//
	// Returns:
	//   - string: The provider name (e.g., "Expo", "Swift", "Android")
	Name() string

	// SetProxyURL sets the tunnel URL for bundle URL rewriting.
	//
	// For Expo servers, this sets the EXPO_PACKAGER_PROXY_URL environment variable
	// which causes Metro to rewrite bundle URLs to use the tunnel URL instead of localhost.
	// This is required for remote devices to fetch JavaScript bundles through the tunnel.
	//
	// Must be called before Start() for the setting to take effect.
	//
	// Parameters:
	//   - tunnelURL: The public tunnel URL (e.g., "https://xxx.trycloudflare.com")
	SetProxyURL(tunnelURL string)
}

// DevServerStatus represents the current status of a development server.
type DevServerStatus string

const (
	// DevServerStatusStopped indicates the server is not running.
	DevServerStatusStopped DevServerStatus = "stopped"

	// DevServerStatusStarting indicates the server is starting up.
	DevServerStatusStarting DevServerStatus = "starting"

	// DevServerStatusRunning indicates the server is running and ready.
	DevServerStatusRunning DevServerStatus = "running"

	// DevServerStatusError indicates the server encountered an error.
	DevServerStatusError DevServerStatus = "error"
)
