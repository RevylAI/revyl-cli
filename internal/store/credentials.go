// Package store provides credential management for app store integrations.
//
// This package handles storing and retrieving credentials for App Store Connect (iOS)
// and Google Play Developer API (Android). Credentials are stored separately from
// Revyl auth credentials at ~/.revyl/store-credentials.json so they survive
// `revyl auth logout` and have independent lifecycle management.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StoreCredentials holds credentials for all supported app stores.
type StoreCredentials struct {
	// IOS contains App Store Connect API credentials.
	IOS *IOSCredentials `json:"ios,omitempty"`

	// Android contains Google Play Developer API credentials.
	Android *AndroidCredentials `json:"android,omitempty"`
}

// IOSCredentials holds App Store Connect API key credentials.
//
// These are obtained from https://appstoreconnect.apple.com/access/integrations/api
// and consist of a Key ID, Issuer ID, and a .p8 private key file.
type IOSCredentials struct {
	// KeyID is the App Store Connect API key ID (e.g., "ABC123DEF4").
	KeyID string `json:"key_id"`

	// IssuerID is the App Store Connect API issuer ID (UUID format).
	IssuerID string `json:"issuer_id"`

	// PrivateKeyPath is the absolute path to the .p8 private key file.
	PrivateKeyPath string `json:"private_key_path"`
}

// AndroidCredentials holds Google Play Developer API credentials.
//
// These use a Google Cloud service account JSON key file for authentication.
type AndroidCredentials struct {
	// ServiceAccountPath is the absolute path to the service account JSON key file.
	ServiceAccountPath string `json:"service_account_path"`
}

// Manager handles store credential storage and retrieval.
type Manager struct {
	// configDir is the directory where credentials are stored.
	configDir string
}

// NewManager creates a new store credential manager.
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

// NewManagerWithDir creates a new store credential manager with a custom directory.
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

// credentialsPath returns the path to the store credentials file.
func (m *Manager) credentialsPath() string {
	return filepath.Join(m.configDir, "store-credentials.json")
}

// Load retrieves stored store credentials from disk.
//
// Returns:
//   - *StoreCredentials: The stored credentials, or empty credentials if file doesn't exist
//   - error: Any error that occurred during retrieval (other than file not existing)
func (m *Manager) Load() (*StoreCredentials, error) {
	data, err := os.ReadFile(m.credentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &StoreCredentials{}, nil
		}
		return nil, fmt.Errorf("failed to read store credentials: %w", err)
	}

	var creds StoreCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse store credentials: %w", err)
	}

	return &creds, nil
}

// Save writes store credentials to disk with restricted permissions.
//
// Parameters:
//   - creds: The credentials to store
//
// Returns:
//   - error: Any error that occurred during storage
func (m *Manager) Save(creds *StoreCredentials) error {
	if err := os.MkdirAll(m.configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal store credentials: %w", err)
	}

	if err := os.WriteFile(m.credentialsPath(), data, 0600); err != nil {
		return fmt.Errorf("failed to write store credentials: %w", err)
	}

	return nil
}

// SaveIOSCredentials stores iOS App Store Connect credentials.
// Preserves any existing Android credentials.
//
// Parameters:
//   - ios: The iOS credentials to store
//
// Returns:
//   - error: Any error that occurred during storage
func (m *Manager) SaveIOSCredentials(ios *IOSCredentials) error {
	creds, err := m.Load()
	if err != nil {
		return err
	}

	creds.IOS = ios
	return m.Save(creds)
}

// SaveAndroidCredentials stores Android Google Play credentials.
// Preserves any existing iOS credentials.
//
// Parameters:
//   - android: The Android credentials to store
//
// Returns:
//   - error: Any error that occurred during storage
func (m *Manager) SaveAndroidCredentials(android *AndroidCredentials) error {
	creds, err := m.Load()
	if err != nil {
		return err
	}

	creds.Android = android
	return m.Save(creds)
}

// HasIOSCredentials checks if iOS credentials are configured.
//
// Returns:
//   - bool: True if valid iOS credentials exist
func (m *Manager) HasIOSCredentials() bool {
	creds, err := m.Load()
	if err != nil {
		return false
	}
	return creds.IOS != nil && creds.IOS.KeyID != "" && creds.IOS.IssuerID != "" && creds.IOS.PrivateKeyPath != ""
}

// HasAndroidCredentials checks if Android credentials are configured.
//
// Returns:
//   - bool: True if valid Android credentials exist
func (m *Manager) HasAndroidCredentials() bool {
	creds, err := m.Load()
	if err != nil {
		return false
	}
	return creds.Android != nil && creds.Android.ServiceAccountPath != ""
}

// ValidateIOSCredentials checks that the iOS credentials are complete and the key file exists.
//
// Returns:
//   - error: Validation error or nil if valid
func (m *Manager) ValidateIOSCredentials() error {
	creds, err := m.Load()
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	if creds.IOS == nil {
		return fmt.Errorf("iOS credentials not configured. Run 'revyl publish auth ios' to set up")
	}

	if creds.IOS.KeyID == "" {
		return fmt.Errorf("iOS key_id is empty")
	}
	if creds.IOS.IssuerID == "" {
		return fmt.Errorf("iOS issuer_id is empty")
	}
	if creds.IOS.PrivateKeyPath == "" {
		return fmt.Errorf("iOS private_key_path is empty")
	}

	if _, err := os.Stat(creds.IOS.PrivateKeyPath); os.IsNotExist(err) {
		return fmt.Errorf("iOS private key file not found: %s", creds.IOS.PrivateKeyPath)
	}

	return nil
}

// ValidateAndroidCredentials checks that the Android credentials are complete and the file exists.
//
// Returns:
//   - error: Validation error or nil if valid
func (m *Manager) ValidateAndroidCredentials() error {
	creds, err := m.Load()
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	if creds.Android == nil {
		return fmt.Errorf("Android credentials not configured. Run 'revyl publish auth android' to set up")
	}

	if creds.Android.ServiceAccountPath == "" {
		return fmt.Errorf("Android service_account_path is empty")
	}

	if _, err := os.Stat(creds.Android.ServiceAccountPath); os.IsNotExist(err) {
		return fmt.Errorf("Android service account file not found: %s", creds.Android.ServiceAccountPath)
	}

	return nil
}
