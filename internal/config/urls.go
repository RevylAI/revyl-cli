// Package config provides project, compilation, migration, and URL configuration for the Revyl CLI.
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
	ProdBackendURL = "https://backend.revyl.ai"

	ProdAppURL = "https://app.revyl.ai"

	DefaultBackendPort = "8000"

	DefaultFrontendPort = "3000"

	portCheckTimeout = 100 * time.Millisecond
)

var commonBackendPorts = []string{"8000", "8001", "8080", "3000"}

var commonFrontendPorts = []string{"3000", "8002", "8080", "3001"}

// findMonorepoRoot walks upward from cwd until os.Stat finds a cognisim_backend
// entry, returning "" if none is found.
func findMonorepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
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

// GetBackendPort returns REVYL_BACKEND_PORT if non-empty, else PORT from
// cognisim_backend/.env, falling back to DefaultBackendPort.
func GetBackendPort() string {
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

// GetBackendPortWithAutoDetect returns REVYL_BACKEND_PORT if set; otherwise it
// probes localhost TCP listeners starting with GetBackendPort, then commonBackendPorts.
func GetBackendPortWithAutoDetect() string {
	if port := os.Getenv("REVYL_BACKEND_PORT"); port != "" {
		return port
	}

	configuredPort := GetBackendPort()

	if isPortOpen("localhost", configuredPort) {
		return configuredPort
	}

	for _, port := range commonBackendPorts {
		if port != configuredPort && isPortOpen("localhost", port) {
			return port
		}
	}

	return configuredPort
}

func isPortOpen(host, port string) bool {
	address := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", address, portCheckTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

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

// GetFrontendPortWithAutoDetect returns REVYL_FRONTEND_PORT if set; otherwise it
// probes localhost TCP listeners starting with GetFrontendPort, then commonFrontendPorts.
func GetFrontendPortWithAutoDetect() string {
	if port := os.Getenv("REVYL_FRONTEND_PORT"); port != "" {
		return port
	}

	configuredPort := GetFrontendPort()

	if isPortOpen("localhost", configuredPort) {
		return configuredPort
	}

	for _, port := range commonFrontendPorts {
		if port != configuredPort && isPortOpen("localhost", port) {
			return port
		}
	}

	return configuredPort
}

// GetBackendURL resolves the backend URL in order: REVYL_BACKEND_URL (trimmed
// of trailing slashes), localhost via GetBackendPortWithAutoDetect in devMode, or ProdBackendURL.
func GetBackendURL(devMode bool) string {
	if envURL := os.Getenv("REVYL_BACKEND_URL"); envURL != "" {
		return strings.TrimRight(envURL, "/")
	}
	if devMode {
		return fmt.Sprintf("http://localhost:%s", GetBackendPortWithAutoDetect())
	}
	return ProdBackendURL
}

// GetAppURL resolves the app URL in order: REVYL_APP_URL (trimmed of trailing
// slashes), localhost via GetFrontendPortWithAutoDetect in devMode, or ProdAppURL.
func GetAppURL(devMode bool) string {
	if envURL := os.Getenv("REVYL_APP_URL"); envURL != "" {
		return strings.TrimRight(envURL, "/")
	}
	if devMode {
		return fmt.Sprintf("http://localhost:%s", GetFrontendPortWithAutoDetect())
	}
	return ProdAppURL
}

func HasURLOverride() bool {
	return os.Getenv("REVYL_BACKEND_URL") != "" || os.Getenv("REVYL_APP_URL") != ""
}
