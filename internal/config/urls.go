// Package config provides URL configuration management for the Revyl CLI.
//
// This package handles dynamic URL resolution for production and development
// environments, reading port configuration from .env files when in dev mode.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ProdBackendURL is the production backend API URL.
	ProdBackendURL = "https://backend.revyl.ai"

	// ProdAppURL is the production frontend app URL.
	ProdAppURL = "https://app.revyl.ai"

	// DefaultBackendPort is the fallback port if cognisim_backend/.env is not found.
	DefaultBackendPort = "8001"

	// DefaultFrontendPort is the fallback port if frontend/.env is not found.
	DefaultFrontendPort = "8002"
)

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

// GetBackendURL returns the backend API URL based on the dev mode setting.
//
// Parameters:
//   - devMode: If true, returns localhost URL with port from cognisim_backend/.env
//
// Returns:
//   - string: The backend API URL
func GetBackendURL(devMode bool) string {
	if devMode {
		return fmt.Sprintf("http://localhost:%s", GetBackendPort())
	}
	return ProdBackendURL
}

// GetAppURL returns the frontend app URL based on the dev mode setting.
// This is used for report URLs and other frontend links.
//
// Parameters:
//   - devMode: If true, returns localhost URL with port from frontend/.env
//
// Returns:
//   - string: The frontend app URL
func GetAppURL(devMode bool) string {
	if devMode {
		return fmt.Sprintf("http://localhost:%s", GetFrontendPort())
	}
	return ProdAppURL
}
