// Package config provides URL configuration management for the Revyl CLI.
//
// This package handles dynamic URL resolution for production and development
// environments, reading port configuration from .env files when in dev mode.
// It also supports auto-detection of running backend services.
package config

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ProdBackendURL is the production backend API URL.
	ProdBackendURL = "https://backend.revyl.ai"

	// ProdAppURL is the production frontend app URL.
	ProdAppURL = "https://app.revyl.ai"

	// DefaultBackendPort is the fallback port if cognisim_backend/.env is not found.
	DefaultBackendPort = "8000"

	// DefaultFrontendPort is the fallback port if frontend/.env is not found.
	DefaultFrontendPort = "3000"

	// portCheckTimeout is the timeout for checking if a port is open.
	portCheckTimeout = 100 * time.Millisecond
)

// commonBackendPorts are the ports to try when auto-detecting the backend.
// Order matters - most common ports first.
var commonBackendPorts = []string{"8000", "8001", "8080", "3000"}

// commonFrontendPorts are the ports to try when auto-detecting the frontend.
// Order matters - most common ports first.
var commonFrontendPorts = []string{"3000", "8002", "8080", "3001"}

// findMonorepoRoot searches upward from the current directory to find the monorepo root.
// The root is identified by having a cognisim_backend/ directory.
//
// Returns:
//   - string: The path to the monorepo root, or empty string if not found
func findMonorepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		// Check if this looks like the monorepo root
		if _, err := os.Stat(filepath.Join(dir, "cognisim_backend")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// readPortFromEnv reads the PORT value from an .env file.
//
// Parameters:
//   - path: The path to the .env file
//
// Returns:
//   - string: The port value, or empty string if not found
func readPortFromEnv(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PORT=") {
			return strings.TrimPrefix(line, "PORT=")
		}
	}
	return ""
}

// GetBackendPort reads the PORT from cognisim_backend/.env.
// Falls back to DefaultBackendPort if the file is not found.
//
// Returns:
//   - string: The backend port number
func GetBackendPort() string {
	// First check environment variable override
	if port := os.Getenv("REVYL_BACKEND_PORT"); port != "" {
		return port
	}

	root := findMonorepoRoot()
	if root == "" {
		return DefaultBackendPort
	}
	envPath := filepath.Join(root, "cognisim_backend", ".env")
	if port := readPortFromEnv(envPath); port != "" {
		return port
	}
	return DefaultBackendPort
}

// GetBackendPortWithAutoDetect reads the PORT from cognisim_backend/.env,
// and if no server is running on that port, tries common alternative ports.
// This is useful when the backend might be running on a non-standard port.
//
// Returns:
//   - string: The backend port number (either from config or auto-detected)
func GetBackendPortWithAutoDetect() string {
	// First check environment variable override
	if port := os.Getenv("REVYL_BACKEND_PORT"); port != "" {
		return port
	}

	// Try to get port from .env file
	configuredPort := GetBackendPort()

	// Check if the configured port is actually listening
	if isPortOpen("localhost", configuredPort) {
		return configuredPort
	}

	// Try common ports if configured port isn't responding
	for _, port := range commonBackendPorts {
		if port != configuredPort && isPortOpen("localhost", port) {
			return port
		}
	}

	// Fall back to configured port even if not responding
	// (let the actual request fail with a clear error)
	return configuredPort
}

// isPortOpen checks if a TCP port is open on the given host.
//
// Parameters:
//   - host: The hostname to check
//   - port: The port number to check
//
// Returns:
//   - bool: True if the port is open and accepting connections
func isPortOpen(host, port string) bool {
	address := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", address, portCheckTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetFrontendPort reads the PORT from frontend/.env.
// Falls back to DefaultFrontendPort if the file is not found.
//
// Returns:
//   - string: The frontend port number
func GetFrontendPort() string {
	root := findMonorepoRoot()
	if root == "" {
		return DefaultFrontendPort
	}
	envPath := filepath.Join(root, "frontend", ".env")
	if port := readPortFromEnv(envPath); port != "" {
		return port
	}
	return DefaultFrontendPort
}

// GetFrontendPortWithAutoDetect reads the PORT from frontend/.env,
// and if no server is running on that port, tries common alternative ports.
// This is useful when the frontend might be running on a non-standard port.
//
// Returns:
//   - string: The frontend port number (either from config or auto-detected)
func GetFrontendPortWithAutoDetect() string {
	// First check environment variable override
	if port := os.Getenv("REVYL_FRONTEND_PORT"); port != "" {
		return port
	}

	// Try to get port from .env file
	configuredPort := GetFrontendPort()

	// Check if the configured port is actually listening
	if isPortOpen("localhost", configuredPort) {
		return configuredPort
	}

	// Try common ports if configured port isn't responding
	for _, port := range commonFrontendPorts {
		if port != configuredPort && isPortOpen("localhost", port) {
			return port
		}
	}

	// Fall back to configured port even if not responding
	// (let the actual request fail with a clear error)
	return configuredPort
}

// GetBackendURL returns the backend API URL based on the dev mode setting.
// In dev mode, it uses auto-detection to find a running backend server.
//
// Parameters:
//   - devMode: If true, returns localhost URL with auto-detected port
//
// Returns:
//   - string: The backend API URL
func GetBackendURL(devMode bool) string {
	if devMode {
		return fmt.Sprintf("http://localhost:%s", GetBackendPortWithAutoDetect())
	}
	return ProdBackendURL
}

// GetAppURL returns the frontend app URL based on the dev mode setting.
// In dev mode, it uses auto-detection to find a running frontend server.
//
// Parameters:
//   - devMode: If true, returns localhost URL with auto-detected port
//
// Returns:
//   - string: The frontend app URL
func GetAppURL(devMode bool) string {
	if devMode {
		return fmt.Sprintf("http://localhost:%s", GetFrontendPortWithAutoDetect())
	}
	return ProdAppURL
}
