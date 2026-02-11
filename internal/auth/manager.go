// Package auth provides authentication management for the Revyl CLI.
//
// This package handles storing and retrieving API credentials from
// the user's home directory (~/.revyl/credentials.json).
//
// The CLI supports two authentication methods:
// 1. Browser-based OAuth flow (default) - stores AccessToken with expiration
// 2. API key authentication (fallback) - stores APIKey for CI/CD environments
package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Credentials represents stored authentication credentials.
// Supports both browser-based OAuth tokens and API keys.
type Credentials struct {
	// APIKey is the Revyl API key for authentication (legacy/CI mode).
	APIKey string `json:"api_key,omitempty"`

	// AccessToken is the PropelAuth access token from browser auth.
	AccessToken string `json:"access_token,omitempty"`

	// RefreshToken is the PropelAuth refresh token for token renewal.
	RefreshToken string `json:"refresh_token,omitempty"`

	// ExpiresAt is the timestamp when the access token expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// Email is the user's email address (optional, for display).
	Email string `json:"email,omitempty"`

	// OrgID is the user's organization ID (optional).
	OrgID string `json:"org_id,omitempty"`

	// UserID is the user's ID (optional).
	UserID string `json:"user_id,omitempty"`

	// AuthMethod indicates how the user authenticated ("browser", "api_key", or "browser_api_key").
	AuthMethod string `json:"auth_method,omitempty"`

	// APIKeyID is the PropelAuth-assigned key ID when a persistent CLI key was created.
	// Used for key rotation and cleanup on logout.
	APIKeyID string `json:"api_key_id,omitempty"`
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
// Priority order:
// 1. REVYL_API_KEY environment variable (for CI/CD)
// 2. Valid (non-expired) access token from browser auth
// 3. API key from stored credentials
//
// Returns:
//   - *Credentials: The stored credentials, or nil if not found
//   - error: Any error that occurred during retrieval
func (m *Manager) GetCredentials() (*Credentials, error) {
	// Check environment variable first (for CI/CD)
	if apiKey := os.Getenv("REVYL_API_KEY"); apiKey != "" {
		return &Credentials{APIKey: apiKey, AuthMethod: "env"}, nil
	}

	return m.GetFileCredentials()
}

// GetFileCredentials retrieves credentials directly from the file,
// bypassing the environment variable check.
//
// This is useful when you need to read/update file-based credentials
// even when REVYL_API_KEY is set (e.g., after saving browser auth credentials).
//
// Returns:
//   - *Credentials: The stored credentials, or nil if not found
//   - error: Any error that occurred during retrieval
func (m *Manager) GetFileCredentials() (*Credentials, error) {
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

// GetActiveToken returns the token to use for API authentication.
// Returns the access token if valid, otherwise falls back to API key.
//
// Returns:
//   - string: The token to use for authentication
//   - error: Any error that occurred
func (m *Manager) GetActiveToken() (string, error) {
	creds, err := m.GetCredentials()
	if err != nil {
		return "", err
	}
	if creds == nil {
		return "", nil
	}

	// Check for valid access token first
	if creds.AccessToken != "" && !creds.IsExpired() {
		return creds.AccessToken, nil
	}

	// Fall back to API key
	return creds.APIKey, nil
}

// IsExpired checks if the access token has expired.
//
// Returns:
//   - bool: True if the token is expired or expiration is not set
func (c *Credentials) IsExpired() bool {
	if c.ExpiresAt == nil {
		// If no expiration set, consider it valid (API key mode)
		return false
	}
	// Add a small buffer (1 minute) to avoid edge cases
	return time.Now().Add(time.Minute).After(*c.ExpiresAt)
}

// GetDisplayName returns a user-friendly display name for the credentials.
//
// Returns:
//   - string: Email if available, otherwise UserID, otherwise "Unknown"
func (c *Credentials) GetDisplayName() string {
	if c.Email != "" {
		return c.Email
	}
	if c.UserID != "" {
		return c.UserID
	}
	return "Unknown"
}

// HasValidAuth checks if the credentials have valid authentication.
//
// Returns:
//   - bool: True if either a valid access token or API key exists
func (c *Credentials) HasValidAuth() bool {
	// Check for valid access token
	if c.AccessToken != "" && !c.IsExpired() {
		return true
	}
	// Check for API key
	return c.APIKey != ""
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
//   - bool: True if credentials exist and have valid authentication
func (m *Manager) IsAuthenticated() bool {
	creds, err := m.GetCredentials()
	if err != nil {
		return false
	}
	if creds == nil {
		return false
	}
	return creds.HasValidAuth()
}

// SaveBrowserCredentials stores credentials from browser-based authentication.
//
// Decodes the JWT to extract the real server-side expiry (`exp` claim) so that
// IsExpired() accurately reflects when the backend will reject the token.
// Falls back to the caller-provided expiresIn if JWT decoding fails.
//
// Parameters:
//   - result: The browser auth result containing token and user info
//   - expiresIn: Fallback duration until the token expires (used if JWT exp extraction fails)
//
// Returns:
//   - error: Any error that occurred during storage
func (m *Manager) SaveBrowserCredentials(result *BrowserAuthResult, expiresIn time.Duration) error {
	// Try to extract the real expiry from the JWT; fall back to caller-provided duration.
	expiresAt := time.Now().Add(expiresIn)
	if jwtExp, err := extractJWTExpiry(result.Token); err == nil {
		expiresAt = jwtExp
	}

	creds := &Credentials{
		AccessToken: result.Token,
		ExpiresAt:   &expiresAt,
		Email:       result.Email,
		OrgID:       result.OrgID,
		UserID:      result.UserID,
		AuthMethod:  "browser",
	}
	return m.SaveCredentials(creds)
}

// extractJWTExpiry decodes a JWT (without signature verification) and returns
// the expiration time from the "exp" claim.
//
// JWTs have three base64url-encoded segments separated by dots: header.payload.signature.
// We only need the payload to read the "exp" claim.
//
// Parameters:
//   - token: A JWT string (e.g., the PropelAuth access token)
//
// Returns:
//   - time.Time: The token's expiration time
//   - error: If the token is malformed or has no "exp" claim
func extractJWTExpiry(token string) (time.Time, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid JWT: expected at least 2 dot-separated segments")
	}

	// Base64url decode the payload (second segment).
	// Add padding if necessary since JWTs use unpadded base64url.
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return time.Time{}, fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("JWT has no exp claim")
	}

	return time.Unix(int64(claims.Exp), 0), nil
}

// SaveAPIKeyCredentials stores credentials from API key authentication.
//
// Parameters:
//   - apiKey: The API key
//   - email: User's email (optional)
//   - orgID: Organization ID (optional)
//   - userID: User ID (optional)
//
// Returns:
//   - error: Any error that occurred during storage
func (m *Manager) SaveAPIKeyCredentials(apiKey, email, orgID, userID string) error {
	creds := &Credentials{
		APIKey:     apiKey,
		Email:      email,
		OrgID:      orgID,
		UserID:     userID,
		AuthMethod: "api_key",
	}
	return m.SaveCredentials(creds)
}

// SaveBrowserAPIKeyCredentials stores credentials from browser-based auth
// that generated a persistent API key (instead of a short-lived access token).
// These credentials never expire, providing the same UX as manual API key auth
// while being created automatically via the browser login flow.
//
// Parameters:
//   - result: The browser auth result containing the API key token and user info
//   - apiKeyID: The PropelAuth-assigned key ID for rotation/cleanup
//
// Returns:
//   - error: Any error that occurred during storage
func (m *Manager) SaveBrowserAPIKeyCredentials(result *BrowserAuthResult, apiKeyID string) error {
	creds := &Credentials{
		APIKey:     result.Token,
		Email:      result.Email,
		OrgID:      result.OrgID,
		UserID:     result.UserID,
		AuthMethod: "browser_api_key",
		APIKeyID:   apiKeyID,
	}
	return m.SaveCredentials(creds)
}
