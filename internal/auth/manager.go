// Package auth provides authentication management for the Revyl CLI.
//
// This package handles storing and retrieving API credentials from
// the user's home directory (~/.revyl/credentials.json).
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Credentials represents stored authentication credentials.
type Credentials struct {
	// APIKey is the Revyl API key for authentication.
	APIKey string `json:"api_key"`

	// Email is the user's email address (optional, for display).
	Email string `json:"email,omitempty"`

	// OrgID is the user's organization ID (optional).
	OrgID string `json:"org_id,omitempty"`

	// UserID is the user's ID (optional).
	UserID string `json:"user_id,omitempty"`
}

// Manager handles credential storage and retrieval.
type Manager struct {
	// configDir is the directory where credentials are stored.
	configDir string
}

// NewManager creates a new credential manager.
//
// Returns:
//   - *Manager: A new manager instance using ~/.revyl as the config directory
func NewManager() *Manager {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}

	return &Manager{
		configDir: filepath.Join(homeDir, ".revyl"),
	}
}

// NewManagerWithDir creates a new credential manager with a custom directory.
//
// Parameters:
//   - configDir: The directory to store credentials in
//
// Returns:
//   - *Manager: A new manager instance
func NewManagerWithDir(configDir string) *Manager {
	return &Manager{
		configDir: configDir,
	}
}

// credentialsPath returns the path to the credentials file.
func (m *Manager) credentialsPath() string {
	return filepath.Join(m.configDir, "credentials.json")
}

// GetCredentials retrieves stored credentials.
//
// First checks for REVYL_API_KEY environment variable, then falls back
// to stored credentials file.
//
// Returns:
//   - *Credentials: The stored credentials, or nil if not found
//   - error: Any error that occurred during retrieval
func (m *Manager) GetCredentials() (*Credentials, error) {
	// Check environment variable first (for CI/CD)
	if apiKey := os.Getenv("REVYL_API_KEY"); apiKey != "" {
		return &Credentials{APIKey: apiKey}, nil
	}

	data, err := os.ReadFile(m.credentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	return &creds, nil
}

// SaveCredentials stores credentials to disk.
//
// Parameters:
//   - creds: The credentials to store
//
// Returns:
//   - error: Any error that occurred during storage
func (m *Manager) SaveCredentials(creds *Credentials) error {
	// Ensure config directory exists
	if err := os.MkdirAll(m.configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	// Write with restricted permissions (owner read/write only)
	if err := os.WriteFile(m.credentialsPath(), data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}

	return nil
}

// ClearCredentials removes stored credentials.
//
// Returns:
//   - error: Any error that occurred during removal
func (m *Manager) ClearCredentials() error {
	err := os.Remove(m.credentialsPath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove credentials: %w", err)
	}
	return nil
}

// IsAuthenticated checks if valid credentials exist.
//
// Returns:
//   - bool: True if credentials exist and have an API key
func (m *Manager) IsAuthenticated() bool {
	creds, err := m.GetCredentials()
	if err != nil {
		return false
	}
	return creds != nil && creds.APIKey != ""
}
