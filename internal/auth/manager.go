// Package auth resolves CLI credentials and handles browser and device approval sign-in.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	defaultDeviceLabel                     = "Revyl CLI"
	cloudRuntimeContextFilename            = "cloud-runtime.json"
	cloudRuntimeContextSchema              = 1
	unresolvedAPIKeyEnvironmentPlaceholder = "${env:REVYL_API_KEY}"
)

// IsUnresolvedAPIKeyEnvironmentValue reports whether a host passed through its
// literal REVYL_API_KEY interpolation syntax instead of resolving the secret.
func IsUnresolvedAPIKeyEnvironmentValue(value string) bool {
	return value == unresolvedAPIKeyEnvironmentPlaceholder
}

const (
	// AuthMethodBrowser is a short-lived access token from a desktop browser login.
	AuthMethodBrowser = "browser"

	AuthMethodAPIKey = "api_key"

	AuthMethodEnvironment = "env"

	// AuthMethodBrowserAPIKey is a persistent key minted by browser or device approval.
	AuthMethodBrowserAPIKey = "browser_api_key"
)

// IsPersistentAPIKeyAuthMethod distinguishes API key methods, including
// environment keys, from short-lived access tokens.
func IsPersistentAPIKeyAuthMethod(method string) bool {
	switch method {
	case AuthMethodAPIKey, AuthMethodEnvironment, AuthMethodBrowserAPIKey:
		return true
	default:
		return false
	}
}

// IsInteractiveLoginAuthMethod reports whether a credential came from a login
// the user performed, as opposed to one supplied by configuration.
//
// A 401 means something different for each: a login can expire or be revoked
// and is worth re-running, while a configured key was simply wrong.
//
// Parameters:
//   - method: The AuthMethod recorded on stored credentials.
//
// Returns:
//   - bool: True when re-running a login is the right recovery.
func IsInteractiveLoginAuthMethod(method string) bool {
	switch method {
	case AuthMethodBrowser, AuthMethodBrowserAPIKey:
		return true
	default:
		return false
	}
}

// Credentials represents stored authentication credentials.
// Supports both browser-based OAuth tokens and API keys.
type Credentials struct {
	APIKey string `json:"api_key,omitempty"`

	AccessToken string `json:"access_token,omitempty"`

	// RefreshToken is the PropelAuth refresh token for token renewal.
	RefreshToken string `json:"refresh_token,omitempty"`

	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	Email string `json:"email,omitempty"`

	OrgID string `json:"org_id,omitempty"`

	UserID string `json:"user_id,omitempty"`

	// AuthMethod indicates how the user authenticated; one of the
	// AuthMethod* constants above.
	AuthMethod string `json:"auth_method,omitempty"`

	// APIKeyID is the provider key ID used by logout to attempt revocation.
	APIKeyID string `json:"api_key_id,omitempty"`

	LocalAuthOverride bool `json:"local_auth_override,omitempty"`
}

type clientIdentity struct {
	ClientInstanceID string `json:"client_instance_id"`
}

// cloudRuntimeContext stores ephemeral headless Cloud state outside normal user credentials.
type cloudRuntimeContext struct {
	SchemaVersion    int    `json:"schema_version"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	APIKey           string `json:"api_key,omitempty"`
}

// CredentialResolution reports the selected credentials and source diagnostics.
type CredentialResolution struct {
	Credentials         *Credentials
	HeadlessCloud       bool
	CloudContextInvalid bool
	// APIKeyEnvironment records how REVYL_API_KEY appeared in this process.
	APIKeyEnvironment APIKeyEnvironmentState
}

type APIKeyEnvironmentState string

const (
	// APIKeyEnvironmentAbsent includes unset and whitespace-only values.
	APIKeyEnvironmentAbsent APIKeyEnvironmentState = "absent"
	// APIKeyEnvironmentUnresolved means the host passed its literal interpolation syntax.
	APIKeyEnvironmentUnresolved APIKeyEnvironmentState = "unresolved"
	// APIKeyEnvironmentPresent indicates a nonblank value that is not a placeholder.
	APIKeyEnvironmentPresent APIKeyEnvironmentState = "present"
)

// classifyAPIKeyEnvironment reports how REVYL_API_KEY appeared without exposing it.
//
// Parameters:
//   - rawValue: Exact REVYL_API_KEY value read from the environment.
//   - configured: Whether the variable was set at all.
//
// Returns:
//   - APIKeyEnvironmentState: Secret-free classification of the environment value.
func classifyAPIKeyEnvironment(rawValue string, configured bool) APIKeyEnvironmentState {
	switch {
	case IsUnresolvedAPIKeyEnvironmentValue(rawValue):
		return APIKeyEnvironmentUnresolved
	case !configured || strings.TrimSpace(rawValue) == "":
		return APIKeyEnvironmentAbsent
	default:
		return APIKeyEnvironmentPresent
	}
}

type Manager struct {
	configDir string
}

// AuthDirectoryEnvironmentVariableName overrides the directory for credentials,
// client identity, and imported Cloud context.
const AuthDirectoryEnvironmentVariableName = "REVYL_AUTH_DIRECTORY"

// NewManager creates a new credential manager.
//
// Returns:
//   - *Manager: A new manager instance using REVYL_AUTH_DIRECTORY when set,
//     otherwise ~/.revyl, as the config directory
func NewManager() *Manager {
	if authDirectory := strings.TrimSpace(os.Getenv(AuthDirectoryEnvironmentVariableName)); authDirectory != "" {
		if absoluteAuthDirectory, err := filepath.Abs(authDirectory); err == nil {
			authDirectory = absoluteAuthDirectory
		}
		return NewManagerWithDir(authDirectory)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}

	return &Manager{
		configDir: filepath.Join(homeDir, ".revyl"),
	}
}

func NewManagerWithDir(configDir string) *Manager {
	return &Manager{
		configDir: configDir,
	}
}

func (m *Manager) credentialsPath() string {
	return filepath.Join(m.configDir, "credentials.json")
}

// CredentialsDisplayPath returns where this manager stores the credentials file,
// for messages shown to the user.
//
// Returns:
//   - string: "~/.revyl/credentials.json" for the default home directory,
//     otherwise the full path inside REVYL_AUTH_DIRECTORY.
func (m *Manager) CredentialsDisplayPath() string {
	if homeDir, err := os.UserHomeDir(); err == nil && m.configDir == filepath.Join(homeDir, ".revyl") {
		return "~/.revyl/credentials.json"
	}
	return m.credentialsPath()
}

// clientIdentityPath returns the path to the stable client identity file.
func (m *Manager) clientIdentityPath() string {
	return filepath.Join(m.configDir, "client-identity.json")
}

func (m *Manager) cloudRuntimeContextPath() string {
	return filepath.Join(m.configDir, cloudRuntimeContextFilename)
}

// GetOrCreateClientInstanceID reuses the saved identity or creates one.
// The new ID is returned even if it cannot be persisted.
func (m *Manager) GetOrCreateClientInstanceID() (string, error) {
	if data, err := os.ReadFile(m.clientIdentityPath()); err == nil {
		var identity clientIdentity
		if err := json.Unmarshal(data, &identity); err == nil {
			id := strings.TrimSpace(identity.ClientInstanceID)
			if id != "" {
				return id, nil
			}
		}
	}

	instanceID, err := generateClientInstanceID()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(m.configDir, 0700); err != nil {
		return instanceID, nil
	}

	data, err := json.MarshalIndent(clientIdentity{ClientInstanceID: instanceID}, "", "  ")
	if err != nil {
		return instanceID, nil
	}

	if err := os.WriteFile(m.clientIdentityPath(), data, 0600); err != nil {
		return instanceID, nil
	}

	return instanceID, nil
}

func generateClientInstanceID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate client instance ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func CurrentDeviceLabel() string {
	hostname, err := os.Hostname()
	if err != nil {
		return defaultDeviceLabel
	}
	return normalizeDeviceLabel(hostname)
}

func normalizeDeviceLabel(label string) string {
	label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
	if label == "" {
		return defaultDeviceLabel
	}
	if len(label) > 80 {
		label = strings.TrimSpace(label[:80])
	}
	if label == "" {
		return defaultDeviceLabel
	}
	return label
}

// GetCredentials returns the selected credentials, or nil when none are configured.
func (m *Manager) GetCredentials() (*Credentials, error) {
	resolution, err := m.ResolveCredentials()
	if err != nil {
		return nil, err
	}
	return resolution.Credentials, nil
}

// ResolveCredentials selects credentials and reports environment and Cloud context diagnostics.
func (m *Manager) ResolveCredentials() (CredentialResolution, error) {
	envKey, envKeyConfigured := os.LookupEnv("REVYL_API_KEY")
	apiKeyEnvironment := classifyAPIKeyEnvironment(envKey, envKeyConfigured)
	if apiKeyEnvironment != APIKeyEnvironmentPresent {
		envKey = ""
	}

	cloudContext, err := m.getCloudRuntimeContext()
	resolution := CredentialResolution{
		HeadlessCloud:       cloudContext != nil || err != nil,
		CloudContextInvalid: err != nil,
		APIKeyEnvironment:   apiKeyEnvironment,
	}

	if envKey != "" {
		fileCreds, err := m.GetFileCredentials()
		if err == nil && fileCreds != nil && fileCreds.LocalAuthOverride && fileCreds.HasValidAuth() {
			resolution.Credentials = fileCreds
			return resolution, nil
		}
		resolution.Credentials = &Credentials{APIKey: envKey, AuthMethod: "env"}
		return resolution, nil
	}
	if err != nil {
		return resolution, err
	}

	if cloudContext != nil && cloudContext.APIKeyConfigured {
		resolution.Credentials = &Credentials{
			APIKey:     cloudContext.APIKey,
			AuthMethod: "api_key",
		}
		return resolution, nil
	}

	fileCredentials, err := m.GetFileCredentials()
	if err != nil {
		return resolution, err
	}
	resolution.Credentials = fileCredentials
	return resolution, nil
}

// GetFileCredentials bypasses environment and Cloud selection and returns nil
// if the file is absent.
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

// getCloudRuntimeContext retrieves the isolated headless Cloud bootstrap state.
//
// Returns:
//   - *cloudRuntimeContext: Persisted Cloud state, or nil when bootstrap has not marked this VM.
//   - error: Any read, schema, or decoding error.
func (m *Manager) getCloudRuntimeContext() (*cloudRuntimeContext, error) {
	data, err := os.ReadFile(m.cloudRuntimeContextPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read Cloud runtime context: %w", err)
	}

	var context cloudRuntimeContext
	if err := json.Unmarshal(data, &context); err != nil {
		return nil, fmt.Errorf("failed to parse Cloud runtime context: %w", err)
	}
	if context.SchemaVersion != cloudRuntimeContextSchema {
		return nil, fmt.Errorf("unsupported Cloud runtime context schema: %d", context.SchemaVersion)
	}
	return &context, nil
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

	if creds.AccessToken != "" && !creds.IsExpired() {
		return creds.AccessToken, nil
	}

	return creds.APIKey, nil
}

// IsExpired applies a one-minute margin to the stored expiry hint.
// An omitted expiry is not considered expired.
func (c *Credentials) IsExpired() bool {
	if c.ExpiresAt == nil {
		return false
	}
	return time.Now().Add(time.Minute).After(*c.ExpiresAt)
}

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
	if c.AccessToken != "" && !c.IsExpired() {
		return true
	}
	return c.APIKey != ""
}

func (m *Manager) SaveCredentials(creds *Credentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	if err := m.writePrivateFile(m.credentialsPath(), data); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}
	return nil
}

// saveUserCredentialsAndClearCloudContext activates an explicit user login.
//
// Parameters:
//   - creds: Browser or API-key credentials selected by the user.
//
// Returns:
//   - error: A credential write error or a Cloud-context cleanup error.
func (m *Manager) saveUserCredentialsAndClearCloudContext(creds *Credentials) error {
	if err := m.SaveCredentials(creds); err != nil {
		return err
	}
	if err := m.ClearCloudRuntimeContext(); err != nil {
		return fmt.Errorf(
			"credentials were saved but could not become active; remove the Cloud runtime context or start a fresh Cloud session: %w",
			err,
		)
	}
	return nil
}

// writePrivateFile atomically replaces one regular file inside the owner-only config directory.
//
// Parameters:
//   - destinationPath: Final path inside the manager's config directory.
//   - content: Exact bytes to persist.
//
// Returns:
//   - error: A directory, file-type, permission, write, sync, or rename error.
func (m *Manager) writePrivateFile(destinationPath string, content []byte) (err error) {
	defer func() {
		if err == nil {
			return
		}
		if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS) {
			err = fmt.Errorf("cannot write private CLI state at %s because the location is read-only; set %s to a writable directory outside your repository and retry: %w", m.configDir, AuthDirectoryEnvironmentVariableName, err)
			return
		}
		err = fmt.Errorf("cannot write private CLI state; use a writable, owner-only directory at %s: %w", m.configDir, err)
	}()
	if err := os.MkdirAll(m.configDir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	directoryInfo, err := os.Lstat(m.configDir)
	if err != nil {
		return fmt.Errorf("inspect config directory: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("config directory must not be a symlink; use a real owner-only directory at %s", m.configDir)
	}
	if !directoryInfo.IsDir() {
		return fmt.Errorf("config path is not a directory: %s", m.configDir)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(m.configDir, 0o700); err != nil {
			return fmt.Errorf("restrict config directory permissions: %w", err)
		}
	}

	if destinationInfo, statErr := os.Lstat(destinationPath); statErr == nil {
		if destinationInfo.Mode()&os.ModeSymlink != 0 || !destinationInfo.Mode().IsRegular() {
			return fmt.Errorf("destination must be a regular file, not a symlink or special file: %s", destinationPath)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect destination: %w", statErr)
	}

	temporaryFile, err := os.CreateTemp(m.configDir, "."+filepath.Base(destinationPath)+".*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath)

	if err := temporaryFile.Chmod(0o600); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("restrict temporary file permissions: %w", err)
	}
	if _, err := temporaryFile.Write(content); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}

	if err := replacePrivateFile(temporaryPath, destinationPath); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	return nil
}

func (m *Manager) ClearCredentials() error {
	err := os.Remove(m.credentialsPath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove credentials: %w", err)
	}
	return nil
}

// ClearCloudRuntimeContext removes the bootstrap-imported headless Cloud context and key.
//
// Returns:
//   - error: Any error other than an already-absent context file.
func (m *Manager) ClearCloudRuntimeContext() error {
	err := os.Remove(m.cloudRuntimeContextPath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove Cloud runtime context: %w", err)
	}
	return nil
}

// ClearAuthenticationState removes the imported Cloud context and user credentials.
// It retains the client identity and does not unset environment variables.
func (m *Manager) ClearAuthenticationState() error {
	if err := m.ClearCloudRuntimeContext(); err != nil {
		return err
	}
	if err := m.ClearCredentials(); err != nil {
		return err
	}
	return nil
}

// SetLocalAuthOverride marks existing stored credentials to win over REVYL_API_KEY
// when they pass the local validity check.
func (m *Manager) SetLocalAuthOverride() error {
	creds, err := m.GetFileCredentials()
	if err != nil {
		return err
	}
	if creds == nil {
		return fmt.Errorf("no stored credentials to override")
	}
	creds.LocalAuthOverride = true
	return m.SaveCredentials(creds)
}

// ClearLocalAuthOverride restores the default precedence for REVYL_API_KEY.
func (m *Manager) ClearLocalAuthOverride() error {
	creds, err := m.GetFileCredentials()
	if err != nil {
		return err
	}
	if creds == nil {
		return nil
	}
	if !creds.LocalAuthOverride {
		return nil
	}
	creds.LocalAuthOverride = false
	return m.SaveCredentials(creds)
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

// SaveBrowserCredentials uses the unverified JWT expiry as a local hint, falling
// back to expiresIn if it cannot decode that hint.
func (m *Manager) SaveBrowserCredentials(result *BrowserAuthResult, expiresIn time.Duration) error {
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
	return m.saveUserCredentialsAndClearCloudContext(creds)
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

func (m *Manager) SaveAPIKeyCredentials(apiKey, email, orgID, userID string) error {
	creds := &Credentials{
		APIKey:     apiKey,
		Email:      email,
		OrgID:      orgID,
		UserID:     userID,
		AuthMethod: "api_key",
	}
	return m.saveUserCredentialsAndClearCloudContext(creds)
}

// SaveCloudRuntimeContext stores Cloud context and an optional injected API key separately.
//
// Parameters:
//   - apiKey: Exact API key injected into the trusted Cloud bootstrap process.
//   - apiKeyConfigured: Whether the Runtime Secret was configured, including an explicitly empty value.
//
// Returns:
//   - error: Any error that occurred during owner-only credential persistence.
func (m *Manager) SaveCloudRuntimeContext(apiKey string, apiKeyConfigured bool) error {
	context := cloudRuntimeContext{
		SchemaVersion:    cloudRuntimeContextSchema,
		APIKeyConfigured: apiKeyConfigured,
		APIKey:           apiKey,
	}
	data, err := json.MarshalIndent(context, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal Cloud runtime context: %w", err)
	}
	if err := m.writePrivateFile(m.cloudRuntimeContextPath(), data); err != nil {
		return fmt.Errorf("failed to write Cloud runtime context: %w", err)
	}
	return nil
}

// SaveBrowserAPIKeyCredentials stores a minted key and its provider ID without a
// local expiry. The key can still be revoked remotely.
func (m *Manager) SaveBrowserAPIKeyCredentials(result *BrowserAuthResult, apiKeyID string) error {
	creds := &Credentials{
		APIKey:     result.Token,
		Email:      result.Email,
		OrgID:      result.OrgID,
		UserID:     result.UserID,
		AuthMethod: AuthMethodBrowserAPIKey,
		APIKeyID:   apiKeyID,
	}
	return m.saveUserCredentialsAndClearCloudContext(creds)
}
