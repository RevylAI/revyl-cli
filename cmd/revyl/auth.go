// Package main provides auth commands for the Revyl CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		if creds != nil && (creds.AuthMethod == "browser" || creds.AuthMethod == "browser_api_key") {
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
  login   - Authenticate with Revyl (opens browser by default)
  logout  - Remove stored credentials
  status  - Show current authentication status

AUTHENTICATION METHODS:
  Browser (default): Opens your browser to sign in with your Revyl account
  API Key (--api-key): Manual API key entry for CI/CD or headless environments

CREDENTIALS:
  Credentials are stored in ~/.revyl/credentials.json`,
}

// authLoginCmd handles user authentication.
var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with Revyl",
	Long: `Authenticate with Revyl.

By default, opens your browser to sign in with your Revyl account.
Use --api-key flag for manual API key entry (useful for CI/CD).

BROWSER AUTHENTICATION (default):
  1. Opens your browser to the Revyl login page
  2. Sign in with your email/password or SSO
  3. Authorize the CLI to access your account
  4. Credentials are automatically saved

API KEY AUTHENTICATION (--api-key):
  1. Get your API key from https://app.revyl.ai/settings/api-keys
  2. Enter the API key when prompted
  3. Credentials are saved to ~/.revyl/credentials.json

EXAMPLES:
  revyl auth login            # Browser-based login (recommended)
  revyl auth login --api-key  # Manual API key entry
  revyl auth status           # Check authentication status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.PrintBanner(version)

		// Get flags
		useAPIKey, _ := cmd.Flags().GetBool("api-key")
		devMode, _ := cmd.Flags().GetBool("dev")

		// Check if already authenticated
		mgr := auth.NewManager()
		creds, err := mgr.GetCredentials()
		if err == nil && creds != nil && creds.HasValidAuth() {
			displayName := creds.GetDisplayName()
			ui.PrintWarning("Already authenticated as %s", displayName)
			ui.PrintInfo("Run 'revyl auth logout' first to re-authenticate")
			return nil
		}

		if useAPIKey {
			return loginWithAPIKey(cmd, mgr, devMode)
		}
		return loginWithBrowser(cmd, mgr, devMode)
	},
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

	// Create browser auth handler
	browserAuth := auth.NewBrowserAuth(auth.BrowserAuthConfig{
		AppURL:  appURL,
		Timeout: 5 * time.Minute,
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

	printAuthNextSteps(cmd)

	return nil
}

// loginWithAPIKey performs API key-based authentication.
//
// Parameters:
//   - cmd: The cobra command (for context)
//   - mgr: The auth manager for storing credentials
//   - devMode: Whether to use local development URLs
//
// Returns:
//   - error: Any error that occurred during authentication
func loginWithAPIKey(cmd *cobra.Command, mgr *auth.Manager, devMode bool) error {
	ui.PrintInfo("Authenticate with API Key")
	ui.Println()

	if devMode {
		ui.PrintInfo("Using local development server")
	}

	// Prompt for API key
	apiKey, err := ui.Prompt("Enter your API key:")
	if err != nil {
		return err
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

		if err := mgr.ClearCredentials(); err != nil {
			ui.PrintError("Failed to clear credentials: %v", err)
			return err
		}

		ui.PrintSuccess("Successfully logged out")
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

		if jsonOutput {
			devMode, _ := cmd.Flags().GetBool("dev")
			result := map[string]interface{}{
				"authenticated": true,
			}
			if config.HasURLOverride() {
				result["backend_url"] = config.GetBackendURL(devMode)
				result["app_url"] = config.GetAppURL(devMode)
			}
			if creds.Email != "" {
				result["email"] = creds.Email
			}
			if creds.UserID != "" {
				result["user_id"] = creds.UserID
			}
			if creds.OrgID != "" {
				result["org_id"] = creds.OrgID
			}
			if creds.AuthMethod != "" {
				result["auth_method"] = creds.AuthMethod
			}
			if creds.AuthMethod == "browser_api_key" || creds.AuthMethod == "api_key" || creds.AuthMethod == "env" {
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

		// Show user information
		if creds.Email != "" {
			ui.PrintInfo("Email: %s", creds.Email)
		}
		if creds.UserID != "" {
			ui.PrintInfo("User ID: %s", creds.UserID)
		}
		if creds.OrgID != "" {
			ui.PrintInfo("Organization: %s", creds.OrgID)
		}

		// Show auth method
		if creds.AuthMethod != "" {
			displayMethod := creds.AuthMethod
			if creds.AuthMethod == "browser_api_key" {
				displayMethod = "browser (persistent key)"
			}
			ui.PrintInfo("Auth Method: %s", displayMethod)
		}

		// Show token expiration for browser auth
		if creds.AuthMethod == "browser_api_key" || creds.AuthMethod == "api_key" || creds.AuthMethod == "env" {
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
			if creds.AuthMethod == "api_key" || creds.AuthMethod == "env" || creds.AuthMethod == "browser_api_key" {
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
	for alias := range cfg.Tests {
		steps = append(steps, ui.NextStep{Label: "Run a test:", Command: fmt.Sprintf("revyl run %s", alias)})
		break
	}

	// If no tests, suggest creating one or uploading a build
	if len(cfg.Tests) == 0 {
		steps = append(steps, ui.NextStep{Label: "Upload a build:", Command: "revyl build upload"})
		steps = append(steps, ui.NextStep{Label: "Create a test:", Command: "revyl test create <name>"})
	}

	ui.PrintNextSteps(steps)
}

func init() {
	// Add --api-key flag to login command
	authLoginCmd.Flags().Bool("api-key", false, "Use API key authentication instead of browser")

	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
}
