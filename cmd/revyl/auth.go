// Package main provides auth commands for the Revyl CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/ui"
)

// authCmd is the parent command for authentication operations.
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
	Long: `Manage authentication with Revyl.

COMMANDS:
  login   - Authenticate with Revyl using your API key
  logout  - Remove stored credentials
  status  - Show current authentication status

CREDENTIALS:
  Credentials are stored in ~/.revyl/credentials.json
  Get your API key from https://app.revyl.ai/settings/api-keys`,
}

// authLoginCmd handles user authentication.
var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with Revyl",
	Long: `Authenticate with Revyl using your API key.

PREREQUISITES:
  - Get your API key from https://app.revyl.ai/settings/api-keys

WHAT IT DOES:
  1. Prompts for your API key
  2. Validates the key against the Revyl API
  3. Stores credentials in ~/.revyl/credentials.json

NEXT STEPS:
  - Run 'revyl init' to initialize your project
  - Run 'revyl test create <name>' to create your first test

EXAMPLES:
  revyl auth login        # Interactive login
  revyl auth status       # Check if already authenticated`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.PrintBanner(version)

		// Check if already authenticated
		mgr := auth.NewManager()
		creds, err := mgr.GetCredentials()
		if err == nil && creds != nil && creds.APIKey != "" {
			displayName := creds.Email
			if displayName == "" {
				displayName = creds.UserID
			}
			ui.PrintWarning("Already authenticated as %s", displayName)
			ui.PrintInfo("Run 'revyl auth logout' first to re-authenticate")
			return nil
		}

		ui.PrintInfo("Authenticate with Revyl")
		ui.Println()

		// Prompt for API key (visible input since users typically paste API keys)
		apiKey, err := ui.Prompt("Enter your API key:")
		if err != nil {
			return err
		}

		if apiKey == "" {
			ui.PrintError("API key cannot be empty")
			return fmt.Errorf("API key cannot be empty")
		}

		// Validate the API key by making a test request
		ui.PrintInfo("Validating API key...")

		// Get dev mode flag
		devMode, _ := cmd.Flags().GetBool("dev")
		if devMode {
			ui.PrintInfo("Using local development server")
		}

		// Create API client and validate the key
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

		// Store credentials with user metadata
		creds = &auth.Credentials{
			APIKey: apiKey,
			Email:  userInfo.Email,
			OrgID:  userInfo.OrgID,
			UserID: userInfo.UserID,
		}

		if err := mgr.SaveCredentials(creds); err != nil {
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

		return nil
	},
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
		mgr := auth.NewManager()

		creds, err := mgr.GetCredentials()
		if err != nil || creds == nil || creds.APIKey == "" {
			ui.PrintWarning("Not authenticated")
			ui.PrintInfo("Run 'revyl auth login' to authenticate")
			return nil
		}

		ui.PrintSuccess("Authenticated")

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

		// Show masked API key (handle short keys gracefully)
		if len(creds.APIKey) > 12 {
			maskedKey := creds.APIKey[:8] + "..." + creds.APIKey[len(creds.APIKey)-4:]
			ui.PrintInfo("API Key: %s", maskedKey)
		} else {
			ui.PrintInfo("API Key: ****")
		}

		return nil
	},
}

func init() {
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
}
