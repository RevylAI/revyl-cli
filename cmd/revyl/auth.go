// Package main provides auth commands for the Revyl CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

const (
	// defaultTokenExpiration is the fallback expiration for browser auth tokens.
	// Used only when JWT exp claim extraction fails (see auth/manager.go extractJWTExpiry).
	// PropelAuth access tokens typically expire in 15-30 minutes; 30 minutes is a
	// safe conservative default that matches their typical token lifetime.
	defaultTokenExpiration = 30 * time.Minute
)

// AuthExpiredError indicates the browser session has expired and the user
// needs to re-authenticate. Wraps the underlying API error for inspection.
type AuthExpiredError struct {
	// Inner is the original API error that triggered the expired-session detection.
	Inner error
}

// Error returns a user-friendly message instructing re-authentication.
func (e *AuthExpiredError) Error() string {
	return "Session expired. Run 'revyl auth login' to re-authenticate."
}

// Unwrap returns the wrapped error for errors.Is / errors.As chains.
func (e *AuthExpiredError) Unwrap() error {
	return e.Inner
}

// wrapAuthError inspects an error returned by an API call. If it is a 401
// and the stored credentials came from browser auth, it returns a friendly
// AuthExpiredError instead of the raw "Invalid API key" message.
//
// Parameters:
//   - err: The error from an API call (may be nil)
//
// Returns:
//   - error: An AuthExpiredError if the session is expired, otherwise the original error
func wrapAuthError(err error) error {
	if err == nil {
		return nil
	}

	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
		mgr := auth.NewManager()
		creds, _ := mgr.GetCredentials()
		if creds != nil && auth.IsInteractiveLoginAuthMethod(creds.AuthMethod) {
			return &AuthExpiredError{Inner: err}
		}
	}
	return err
}

// authCmd is the parent command for authentication operations.
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
	Long: `Manage authentication with Revyl.

COMMANDS:
  login              - Authenticate with Revyl (browser approval by default)
  logout             - Remove stored credentials
  status             - Show current authentication status
  persist-cloud-env  - Bridge a hosted Cloud agent's injected REVYL_API_KEY

AUTHENTICATION METHODS:
  Device approval (default): Prints a URL you approve in a browser anywhere,
    so it works the same locally, in a cloud agent, over SSH, or in a container
  API Key (--api-key): Manual API key entry for CI/CD or unattended machines
  Loopback browser (--browser): The previous local-port flow, kept for one release
  Cloud bridge (persist-cloud-env): Reuses a hosted agent's Runtime Secret when
    nobody is present to approve

CREDENTIALS:
  Credentials are stored in ~/.revyl/credentials.json`,
}

// authLoginCmd handles user authentication.
var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with Revyl",
	Long: `Authenticate with Revyl.

By default, prints an approval URL and waits for you to approve the request in a
browser. The browser can be anywhere — this machine, or your laptop while the
CLI runs in a cloud agent, over SSH, or in a container.

DEVICE APPROVAL (default):
  1. The CLI registers a request and prints a URL plus a short code
  2. Open the URL wherever you have a browser and sign in
  3. Confirm the code matches and approve
  4. The CLI receives the credential and saves it

API KEY AUTHENTICATION (--api-key):
  1. Get your API key from https://app.revyl.ai/settings/api-keys
  2. Enter the API key when prompted
  3. Credentials are saved to ~/.revyl/credentials.json

LOOPBACK BROWSER (--browser):
  The previous flow, which opens a browser on this machine and listens on a
  local port. Kept as an escape hatch for one release.

EXAMPLES:
  revyl auth login            # Device approval (works everywhere)
  revyl auth login --api-key  # Manual API key entry
  revyl auth login --browser  # Legacy loopback browser login
  revyl auth status           # Check authentication status`,
	Example: `  revyl auth login
  revyl auth login --api-key=rk_xxx
  revyl auth login --api-key
  revyl auth login --browser
  REVYL_API_KEY=rk_xxx revyl auth status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.PrintBanner(version)

		apiKeyValue, _ := cmd.Flags().GetString("api-key")
		useAPIKey := cmd.Flags().Changed("api-key")
		useLoopbackBrowser, _ := cmd.Flags().GetBool("browser")
		devMode, _ := cmd.Flags().GetBool("dev")

		mgr := auth.NewManager()
		creds, err := mgr.GetCredentials()
		if err == nil && creds != nil && creds.HasValidAuth() {
			if creds.AuthMethod == "env" {
				ui.PrintWarning("REVYL_API_KEY env var is set")
				ui.PrintDim("Browser login will override it for local interactive commands")
				ui.Println()
			} else {
				displayName := creds.GetDisplayName()
				ui.PrintWarning("Already authenticated as %s", displayName)
				ui.PrintInfo("Run 'revyl auth logout' first to re-authenticate")
				return nil
			}
		}

		if useAPIKey {
			return loginWithAPIKey(cmd, mgr, devMode, apiKeyValue)
		}
		if useLoopbackBrowser {
			return loginWithBrowser(cmd, mgr, devMode)
		}
		return loginWithDeviceApproval(cmd, mgr, devMode)
	},
}

// authPersistCloudEnvironmentOutput reports which Cloud bootstrap state was stored.
type authPersistCloudEnvironmentOutput struct {
	CloudContextPersisted bool   `json:"cloud_context_persisted"`
	CredentialPersisted   bool   `json:"credential_persisted"`
	Source                string `json:"source,omitempty"`
}

// authPersistCloudEnvCmd imports Cloud context and the injected API key into the shared credential store.
var authPersistCloudEnvCmd = &cobra.Command{
	Use:   "persist-cloud-env",
	Short: "Bridge a hosted agent's injected REVYL_API_KEY into the credential store",
	Long: `Bridge a hosted Cloud agent's injected API key into the credential store.

Hosted agents inject REVYL_API_KEY into the shell environment, but the MCP
server runs as a separate process that may not see it. This command copies the
key from this shell into ~/.revyl so the MCP server picks it up on its next
tool call. No restart is required.

This command reads an existing secret; it cannot create one. If no key is
present, add REVYL_API_KEY as a Runtime Secret and start a fresh agent, or run
'revyl auth login' on a machine with a browser.`,
	Example: `  revyl auth persist-cloud-env
  revyl auth persist-cloud-env --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := persistCloudAuthenticationContext(auth.NewManager())
		if err != nil {
			return err
		}

		jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
		if jsonOutput {
			data, err := json.Marshal(result)
			if err != nil {
				return fmt.Errorf("failed to encode Cloud credential result: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		ui.PrintSuccess("Bridged the injected API key into the credential store")
		ui.PrintDim("The MCP server picks this up on its next tool call; no restart needed")
		return nil
	},
}

// persistCloudAuthenticationContext saves the injected key without process arguments.
//
// The command is deliberately environment-agnostic: the only precondition it
// can verify is the one that matters, that REVYL_API_KEY is readable here.
// Refusing to run based on a guess about the host would block the plugin's own
// session hook, which is the caller that makes an agent work unattended.
// Persisting a context without a key would report success for a state that
// still cannot authenticate, so an absent key is a hard failure that names the
// next action.
//
// Parameters:
//   - manager: Credential manager that owns the protected local credential store.
//
// Returns:
//   - authPersistCloudEnvironmentOutput: Secret-free persistence result.
//   - error: A missing-key or persistence error; error text never contains the API key.
func persistCloudAuthenticationContext(manager *auth.Manager) (authPersistCloudEnvironmentOutput, error) {
	apiKey, apiKeyConfigured := os.LookupEnv("REVYL_API_KEY")
	if auth.IsUnresolvedAPIKeyEnvironmentValue(apiKey) {
		apiKey = ""
		apiKeyConfigured = false
	}
	if !apiKeyConfigured || strings.TrimSpace(apiKey) == "" {
		return authPersistCloudEnvironmentOutput{}, errors.New(
			"REVYL_API_KEY is not present in this shell, so there is no secret to bridge; add REVYL_API_KEY as a Runtime Secret and start a fresh agent, or run 'revyl auth login' on a machine with a browser",
		)
	}
	if err := manager.SaveCloudRuntimeContext(apiKey, true); err != nil {
		return authPersistCloudEnvironmentOutput{}, fmt.Errorf("failed to persist Cloud authentication context: %w", err)
	}
	return authPersistCloudEnvironmentOutput{
		CloudContextPersisted: true,
		CredentialPersisted:   true,
		Source:                "REVYL_API_KEY",
	}, nil
}

// loginWithBrowser performs browser-based OAuth authentication.
//
// Parameters:
//   - cmd: The cobra command (for context)
//   - mgr: The auth manager for storing credentials
//   - devMode: Whether to use local development URLs
//
// Returns:
//   - error: Any error that occurred during authentication
func loginWithBrowser(cmd *cobra.Command, mgr *auth.Manager, devMode bool) error {
	ui.PrintInfo("Opening browser to authenticate...")
	ui.Println()

	// Get the app URL based on dev mode
	appURL := config.GetAppURL(devMode)
	if devMode {
		ui.PrintInfo("Using local development server: %s", appURL)
	}

	clientInstanceID, err := mgr.GetOrCreateClientInstanceID()
	if err != nil {
		ui.PrintError("Failed to prepare local CLI identity: %v", err)
		return err
	}

	// Create browser auth handler
	browserAuth := auth.NewBrowserAuth(auth.BrowserAuthConfig{
		AppURL:           appURL,
		Timeout:          5 * time.Minute,
		ClientInstanceID: clientInstanceID,
		DeviceLabel:      auth.CurrentDeviceLabel(),
	})

	// Create context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Show waiting message
	ui.PrintInfo("Waiting for authentication (press Ctrl+C to cancel)...")

	// Perform browser authentication
	result, err := browserAuth.Authenticate(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			ui.PrintWarning("Authentication cancelled")
			return nil
		}
		ui.PrintError("Authentication failed: %v", err)
		ui.Println()
		ui.PrintInfo("If browser didn't open, try: revyl auth login --api-key")
		return err
	}

	return completeInteractiveLogin(cmd, mgr, devMode, result)
}

// loginWithDeviceApproval authenticates through a server-brokered approval.
//
// Needs no local listening port and no browser on this machine, so it is the
// one path that behaves the same on a laptop, in a cloud agent, over SSH, and
// in a container. The approval URL is opened here when a browser exists and
// always printed, because printing it is what makes the headless case work.
//
// Parameters:
//   - cmd: The cobra command, used for context and next-step output.
//   - mgr: The auth manager for storing credentials.
//   - devMode: Whether to use local development URLs.
//
// Returns:
//   - error: Any error during authorization, denial, or expiry. Cancellation is
//     reported as a warning rather than an error.
func loginWithDeviceApproval(cmd *cobra.Command, mgr *auth.Manager, devMode bool) error {
	backendURL := config.GetBackendURL(devMode)
	if devMode {
		ui.PrintInfo("Using local development server: %s", backendURL)
	}

	clientInstanceID, err := mgr.GetOrCreateClientInstanceID()
	if err != nil {
		ui.PrintError("Failed to prepare local CLI identity: %v", err)
		return err
	}

	deviceAuth := auth.NewDeviceAuth(auth.DeviceAuthConfig{
		BackendURL:       backendURL,
		ClientInstanceID: clientInstanceID,
		DeviceLabel:      auth.CurrentDeviceLabel(),
	})

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	authorization, err := deviceAuth.CreateAuthorization(ctx)
	if err != nil {
		ui.PrintError("Could not start authorization: %v", err)
		ui.Println()
		ui.PrintInfo("For an unattended machine, use: revyl auth login --api-key")
		return err
	}

	ui.PrintInfo("Open this URL to approve, then confirm the code below:")
	ui.Println()
	ui.PrintInfo("  %s", authorization.VerificationURIComplete)
	ui.PrintInfo("  Code: %s", authorization.UserCode)
	ui.Println()
	// Opening the browser is a convenience on a desktop and impossible
	// elsewhere, so a failure here is not a failure of the login.
	_ = ui.OpenBrowser(authorization.VerificationURIComplete)

	ui.PrintInfo("Waiting for approval (press Ctrl+C to cancel)...")

	result, err := deviceAuth.WaitForApproval(ctx, authorization)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			ui.PrintWarning("Authentication cancelled")
			return nil
		case errors.Is(err, auth.ErrDeviceAuthorizationDenied):
			ui.PrintError("The request was denied")
			return err
		case errors.Is(err, auth.ErrDeviceAuthorizationExpired):
			ui.PrintError("The request expired before it was approved")
			ui.PrintInfo("Run 'revyl auth login' again to start a new one")
			return err
		default:
			ui.PrintError("Authentication failed: %v", err)
			return err
		}
	}

	return completeInteractiveLogin(cmd, mgr, devMode, result)
}

// completeInteractiveLogin validates, stores, and reports a fresh credential.
//
// Shared by both interactive paths so a device approval and a loopback login
// produce identical stored credentials and identical next steps.
//
// Parameters:
//   - cmd: The cobra command, used for next-step output.
//   - mgr: The auth manager for storing credentials.
//   - devMode: Whether to use local development URLs.
//   - result: The credential the flow obtained.
//
// Returns:
//   - error: A validation or persistence failure.
func completeInteractiveLogin(
	cmd *cobra.Command,
	mgr *auth.Manager,
	devMode bool,
	result *auth.BrowserAuthResult,
) error {
	// Validate the token by making a test request
	ui.PrintInfo("Validating credentials...")

	client := api.NewClientWithDevMode(result.Token, devMode)
	validateCtx, validateCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer validateCancel()

	userInfo, err := client.ValidateAPIKey(validateCtx)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
			ui.PrintError("Authentication token is invalid")
			return fmt.Errorf("invalid authentication token")
		}
		ui.PrintError("Failed to validate credentials: %v", err)
		return err
	}

	// Save credentials — persistent API key or short-lived access token
	isPersistentKey := result.AuthMethod == "api_key" && result.APIKeyID != ""

	if isPersistentKey {
		// Backend generated a long-lived API key — store it without expiry
		if err := mgr.SaveBrowserAPIKeyCredentials(result, result.APIKeyID); err != nil {
			ui.PrintError("Failed to save credentials: %v", err)
			return err
		}
	} else {
		// Fallback: short-lived access token (original behaviour)
		if err := mgr.SaveBrowserCredentials(result, defaultTokenExpiration); err != nil {
			ui.PrintError("Failed to save credentials: %v", err)
			return err
		}
	}

	// Update with validated user info (may have more details than callback)
	// Use GetFileCredentials to bypass env var and get the just-saved credentials
	creds, err := mgr.GetFileCredentials()
	if err != nil {
		// Log warning but don't fail - credentials are saved, just not enriched
		ui.PrintWarning("Could not enrich credentials with user info: %v", err)
	} else if creds != nil {
		updated := false
		if userInfo.Email != "" && creds.Email != userInfo.Email {
			creds.Email = userInfo.Email
			updated = true
		}
		if userInfo.OrgID != "" && creds.OrgID != userInfo.OrgID {
			creds.OrgID = userInfo.OrgID
			updated = true
		}
		if userInfo.UserID != "" && creds.UserID != userInfo.UserID {
			creds.UserID = userInfo.UserID
			updated = true
		}
		if updated {
			if err := mgr.SaveCredentials(creds); err != nil {
				ui.PrintWarning("Could not save enriched credentials: %v", err)
			}
		}
	}

	// When REVYL_API_KEY is set, mark the file credentials as the local
	// interactive override so subsequent commands resolve the browser account.
	if os.Getenv("REVYL_API_KEY") != "" {
		if err := mgr.SetLocalAuthOverride(); err != nil {
			ui.PrintWarning("Could not persist local auth override: %v", err)
		}
	}

	ui.Println()
	if userInfo.Email != "" {
		ui.PrintSuccess("Successfully authenticated as %s", userInfo.Email)
	} else {
		ui.PrintSuccess("Successfully authenticated!")
	}
	if userInfo.OrgID != "" {
		ui.PrintInfo("Organization: %s", userInfo.OrgID)
	}
	if os.Getenv("REVYL_API_KEY") != "" {
		ui.PrintDim("  Browser login is now active (overriding REVYL_API_KEY for local commands)")
	}
	ui.PrintInfo("Credentials saved to ~/.revyl/credentials.json")
	warnIfOrgMismatchAfterLogin(cmd)

	printAuthNextSteps(cmd)

	return nil
}

// loginWithAPIKey performs API key-based authentication.
//
// Parameters:
//   - cmd: The cobra command (for context)
//   - mgr: The auth manager for storing credentials
//   - devMode: Whether to use local development URLs
//   - providedKey: API key value from --api-key=<value>; empty or "prompt" triggers interactive prompt
//
// Returns:
//   - error: Any error that occurred during authentication
func loginWithAPIKey(cmd *cobra.Command, mgr *auth.Manager, devMode bool, providedKey string) error {
	ui.PrintInfo("Authenticate with API Key")
	ui.Println()

	if devMode {
		ui.PrintInfo("Using local development server")
	}

	apiKey := providedKey
	if apiKey == "" || apiKey == "prompt" {
		var err error
		apiKey, err = ui.Prompt("Enter your API key:")
		if err != nil {
			return err
		}
	}

	if apiKey == "" {
		ui.PrintError("API key cannot be empty")
		return fmt.Errorf("API key cannot be empty")
	}

	// Validate the API key
	ui.PrintInfo("Validating API key...")

	client := api.NewClientWithDevMode(apiKey, devMode)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	userInfo, err := client.ValidateAPIKey(ctx)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
			ui.PrintError("Invalid API key")
			ui.PrintInfo("Get your API key from https://app.revyl.ai/settings/api-keys")
			return fmt.Errorf("invalid API key")
		}
		ui.PrintError("Failed to validate API key: %v", err)
		return err
	}

	// Save credentials
	if err := mgr.SaveAPIKeyCredentials(apiKey, userInfo.Email, userInfo.OrgID, userInfo.UserID); err != nil {
		ui.PrintError("Failed to save credentials: %v", err)
		return err
	}

	ui.Println()
	if userInfo.Email != "" {
		ui.PrintSuccess("Successfully authenticated as %s", userInfo.Email)
	} else {
		ui.PrintSuccess("Successfully authenticated!")
	}
	if userInfo.OrgID != "" {
		ui.PrintInfo("Organization: %s", userInfo.OrgID)
	}
	ui.PrintInfo("Credentials saved to ~/.revyl/credentials.json")
	warnIfOrgMismatchAfterLogin(cmd)

	printAuthNextSteps(cmd)

	return nil
}

// authLogoutCmd removes stored credentials.
var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored credentials",
	Long:  `Remove stored credentials from ~/.revyl/credentials.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := auth.NewManager()
		creds, _ := mgr.GetFileCredentials()
		devMode, _ := cmd.Flags().GetBool("dev")

		if err := mgr.ClearAuthenticationState(); err != nil {
			ui.PrintError("Failed to clear authentication state: %v", err)
			return err
		}

		if creds != nil && auth.IsInteractiveLoginAuthMethod(creds.AuthMethod) && creds.APIKeyID != "" && creds.APIKey != "" {
			client := api.NewClientWithDevMode(creds.APIKey, devMode)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			err := client.RevokeCLIAPIKey(ctx, creds.APIKeyID)
			if err != nil {
				var apiErr *api.APIError
				switch {
				case errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 404):
					// Already revoked or no longer valid. Continue clearing local credentials.
				default:
					ui.PrintWarning("Could not revoke browser auth key remotely: %v", err)
				}
			}
		}

		ui.PrintSuccess("Cleared stored credentials")
		if apiKey := os.Getenv("REVYL_API_KEY"); apiKey != "" && !auth.IsUnresolvedAPIKeyEnvironmentValue(apiKey) {
			ui.PrintWarning("REVYL_API_KEY env var is still set — it will continue to authenticate.")
			ui.PrintInfo("To fully log out: unset REVYL_API_KEY")
		}
		return nil
	},
}

// authStatusCmd shows current authentication status.
var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication status",
	Long:  `Show current authentication status and user information.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

		mgr := auth.NewManager()

		creds, err := mgr.GetCredentials()
		if err != nil || creds == nil || !creds.HasValidAuth() {
			if jsonOutput {
				data, _ := json.MarshalIndent(map[string]interface{}{
					"authenticated": false,
				}, "", "  ")
				fmt.Println(string(data))
				return nil
			}
			ui.PrintWarning("Not authenticated")
			ui.PrintInfo("Run 'revyl auth login' to authenticate")
			return nil
		}

		// Enrich with live org/user info when local creds lack it
		// (always the case for REVYL_API_KEY env auth).
		email := creds.Email
		orgID := creds.OrgID
		orgName := ""
		userID := creds.UserID

		if orgName == "" {
			token, _ := mgr.GetActiveToken()
			if token != "" {
				devMode, _ := cmd.Flags().GetBool("dev")
				client := api.NewClientWithDevMode(token, devMode)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				if info, err := client.ValidateAPIKey(ctx); err == nil {
					if info.OrgID != "" {
						orgID = info.OrgID
					}
					if info.OrgName != "" {
						orgName = info.OrgName
					}
					if email == "" {
						email = info.Email
					}
					if userID == "" {
						userID = info.UserID
					}
				}
			}
		}

		if jsonOutput {
			devMode, _ := cmd.Flags().GetBool("dev")
			result := map[string]interface{}{
				"authenticated": true,
			}
			if config.HasURLOverride() {
				result["backend_url"] = config.GetBackendURL(devMode)
				result["app_url"] = config.GetAppURL(devMode)
			}
			if email != "" {
				result["email"] = email
			}
			if userID != "" {
				result["user_id"] = userID
			}
			if orgID != "" {
				result["org_id"] = orgID
			}
			if orgName != "" {
				result["org_name"] = orgName
			}
			if creds.AuthMethod != "" {
				result["auth_method"] = creds.AuthMethod
			}
			if auth.IsPersistentAPIKeyAuthMethod(creds.AuthMethod) {
				result["expires_in_seconds"] = nil
				result["expired"] = false
			} else if creds.ExpiresAt != nil {
				remaining := time.Until(*creds.ExpiresAt)
				result["expires_in_seconds"] = int(remaining.Seconds())
				result["expired"] = remaining <= 0
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		ui.PrintSuccess("Authenticated")

		// Show custom environment URLs if overrides are active
		if config.HasURLOverride() {
			devMode, _ := cmd.Flags().GetBool("dev")
			ui.PrintInfo("Backend: %s", config.GetBackendURL(devMode))
			ui.PrintInfo("App: %s", config.GetAppURL(devMode))
		}

		if email != "" {
			ui.PrintInfo("Email: %s", email)
		}
		if userID != "" {
			ui.PrintInfo("User ID: %s", userID)
		}
		if orgName != "" {
			ui.PrintInfo("Organization: %s (%s)", orgName, orgID)
		} else if orgID != "" {
			ui.PrintInfo("Organization: %s", orgID)
		}

		// Show auth method
		if creds.AuthMethod != "" {
			displayMethod := creds.AuthMethod
			if creds.AuthMethod == auth.AuthMethodBrowserAPIKey {
				displayMethod = "browser (persistent key)"
			}
			ui.PrintInfo("Auth Method: %s", displayMethod)
		}

		// Show token expiration for browser auth
		if auth.IsPersistentAPIKeyAuthMethod(creds.AuthMethod) {
			ui.PrintInfo("Token expires: never")
		} else if creds.ExpiresAt != nil {
			remaining := time.Until(*creds.ExpiresAt)
			if remaining > 0 {
				ui.PrintInfo("Token expires in: %s", formatDuration(remaining))
			} else {
				ui.PrintWarning("Token expired - run 'revyl auth login' to re-authenticate")
			}
		}

		// Show masked token/key
		token, _ := mgr.GetActiveToken()
		if len(token) > 12 {
			maskedToken := token[:8] + "..." + token[len(token)-4:]
			if auth.IsPersistentAPIKeyAuthMethod(creds.AuthMethod) {
				ui.PrintInfo("API Key: %s", maskedToken)
			} else {
				ui.PrintInfo("Token: %s", maskedToken)
			}
		} else if token != "" {
			ui.PrintInfo("Token: ****")
		}

		return nil
	},
}

// formatDuration formats a duration in a human-readable way with proper rounding.
//
// Parameters:
//   - d: The duration to format
//
// Returns:
//   - string: A human-readable duration string (e.g., "1 hour 30 minutes")
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	if d < time.Hour {
		minutes := int(d.Minutes() + 0.5) // Round to nearest minute
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}

	// For durations >= 1 hour, show hours and remaining minutes
	totalMinutes := int(d.Minutes() + 0.5) // Round total to nearest minute
	hours := totalMinutes / 60
	minutes := totalMinutes % 60

	if minutes == 0 {
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}

	hourStr := "hours"
	if hours == 1 {
		hourStr = "hour"
	}
	minuteStr := "minutes"
	if minutes == 1 {
		minuteStr = "minute"
	}
	return fmt.Sprintf("%d %s %d %s", hours, hourStr, minutes, minuteStr)
}

// printAuthNextSteps prints context-aware next steps after a successful login.
// Checks for project config and suggests the most relevant next action.
// Respects JSON output mode -- suppressed when --json is set.
//
// Parameters:
//   - cmd: The cobra command being executed (used to check --json flag)
func printAuthNextSteps(cmd *cobra.Command) {
	// Guard against JSON output mode per PrintNextSteps contract
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	if err != nil || cfg == nil {
		// No config found - suggest init
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Initialize project:", Command: "revyl init"},
		})
		return
	}

	// Config exists - suggest based on state
	var steps []ui.NextStep

	// If tests exist, suggest running one
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	aliases := config.ListLocalTestAliases(testsDir)
	if len(aliases) > 0 {
		steps = append(steps, ui.NextStep{Label: "Run a test:", Command: fmt.Sprintf("revyl test run %s", aliases[0])})
	} else {
		steps = append(steps, ui.NextStep{Label: "Build and upload:", Command: "revyl build"})
		steps = append(steps, ui.NextStep{Label: "Create a test:", Command: "revyl test create <name>"})
	}

	ui.PrintNextSteps(steps)
}

// authBillingCmd opens the billing settings page so the user can add a payment
// method or manage their plan.
var authBillingCmd = &cobra.Command{
	Use:   "billing",
	Short: "Manage billing and payment method",
	Long: `Open the Revyl billing page in your browser.

Use this to add a payment method for more free device time, choose a plan,
or manage billing.

EXAMPLES:
  revyl auth billing          # Open billing page
  revyl auth billing --dev    # Open local dev billing page`,
	RunE: func(cmd *cobra.Command, args []string) error {
		devMode, _ := cmd.Flags().GetBool("dev")
		mgr := auth.NewManager()

		// Require authentication first.
		creds, err := mgr.GetCredentials()
		if err != nil || creds == nil || !creds.HasValidAuth() {
			ui.PrintWarning("Not authenticated")
			ui.PrintInfo("Run 'revyl auth login' first, then 'revyl auth billing'")
			return nil
		}

		// Check current plan status.
		token, _ := mgr.GetActiveToken()
		client := api.NewClientWithDevMode(token, devMode)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		plan, err := client.GetBillingPlan(ctx)
		if err != nil {
			// If we can't check the plan, just open the page anyway.
			ui.PrintWarning("Could not check billing status: %v", err)
		} else if plan.BillingExempt {
			ui.PrintSuccess("Your organization has an enterprise plan — no action needed")
			return nil
		} else if plan.Plan != "none" && plan.Plan != "" {
			ui.PrintSuccess("Plan active: %s", plan.DisplayName)
			ui.PrintInfo("Opening billing settings to manage your plan...")
		} else {
			ui.PrintInfo("Opening billing page...")
			ui.PrintInfo("Add a payment method for more free device time, or choose a plan when you're ready.")
		}

		appURL := config.GetAppURL(devMode)
		billingURL := fmt.Sprintf("%s/settings/billing", appURL)

		if openErr := ui.OpenBrowser(billingURL); openErr != nil {
			ui.PrintInfo("Open this URL in your browser:")
			ui.PrintInfo("  %s", billingURL)
		} else {
			ui.PrintSuccess("Opened billing page in browser")
		}

		return nil
	},
}

func init() {
	authLoginCmd.Flags().String("api-key", "", "API key for headless auth (omit value to be prompted)")
	authLoginCmd.Flags().Lookup("api-key").NoOptDefVal = "prompt"
	authLoginCmd.Flags().Bool("browser", false, "Use the legacy loopback browser login instead of device approval")

	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authBillingCmd)
	authCmd.AddCommand(authPersistCloudEnvCmd)
}
