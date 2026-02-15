// Package main provides env var management commands for tests.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// testEnvForce skips the confirmation prompt for clear.
var testEnvForce bool

// testEnvCmd is the parent command for test env var operations.
var testEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage test environment variables",
	Long: `Manage app launch environment variables for a test.

Env vars are encrypted at rest and automatically injected when the app
launches during test execution.

COMMANDS:
  list    - List all env vars for a test
  set     - Add or update an env var (KEY=VALUE)
  delete  - Delete an env var by key
  clear   - Delete ALL env vars for a test

EXAMPLES:
  revyl test env list my-test
  revyl test env set my-test API_URL=https://staging.example.com
  revyl test env set my-test DEBUG=true
  revyl test env delete my-test API_URL
  revyl test env clear my-test`,
}

// testEnvListCmd lists all env vars for a test.
var testEnvListCmd = &cobra.Command{
	Use:   "list <test-name>",
	Short: "List all env vars for a test",
	Args:  cobra.ExactArgs(1),
	RunE:  runTestEnvList,
}

// testEnvSetCmd adds or updates an env var.
var testEnvSetCmd = &cobra.Command{
	Use:   "set <test-name> KEY=VALUE",
	Short: "Add or update an env var",
	Long: `Add or update an environment variable for a test.

If the key already exists, its value is updated. Otherwise a new var is created.

EXAMPLES:
  revyl test env set my-test API_URL=https://staging.example.com
  revyl test env set my-test "SECRET_KEY=my secret value"`,
	Args: cobra.ExactArgs(2),
	RunE: runTestEnvSet,
}

// testEnvDeleteCmd deletes an env var by key.
var testEnvDeleteCmd = &cobra.Command{
	Use:   "delete <test-name> <KEY>",
	Short: "Delete an env var by key",
	Args:  cobra.ExactArgs(2),
	RunE:  runTestEnvDelete,
}

// testEnvClearCmd deletes all env vars for a test.
var testEnvClearCmd = &cobra.Command{
	Use:   "clear <test-name>",
	Short: "Delete ALL env vars for a test",
	Long: `Delete all environment variables for a test.

Requires --force or interactive confirmation.`,
	Args: cobra.ExactArgs(1),
	RunE: runTestEnvClear,
}

func init() {
	testEnvCmd.AddCommand(testEnvListCmd)
	testEnvCmd.AddCommand(testEnvSetCmd)
	testEnvCmd.AddCommand(testEnvDeleteCmd)
	testEnvCmd.AddCommand(testEnvClearCmd)

	testEnvClearCmd.Flags().BoolVarP(&testEnvForce, "force", "f", false, "Skip confirmation prompt")
}

// envSetupClient creates an API client and resolves test ID from name/alias.
// Returns testID, client, and error.
// This is a package-level var so it can be overridden in tests.
var envSetupClient = envSetupClientDefault

func envSetupClientDefault(cmd *cobra.Command, testNameOrID string) (string, *api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return "", nil, err
	}

	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	testID, _, err := resolveTestID(cmd.Context(), testNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return "", nil, fmt.Errorf("test not found")
	}

	return testID, client, nil
}

func runTestEnvList(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	testID, client, err := envSetupClient(cmd, testNameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Fetching env vars...")
	resp, err := client.ListEnvVars(cmd.Context(), testID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to list env vars: %v", err)
		return err
	}

	if len(resp.Result) == 0 {
		ui.PrintInfo("No environment variables set for test '%s'", testNameOrID)
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Set an env var:", Command: fmt.Sprintf("revyl test env set %s KEY=VALUE", testNameOrID)},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Environment Variables for '%s' (%d)", testNameOrID, len(resp.Result))
	ui.Println()

	table := ui.NewTable("KEY", "VALUE", "UPDATED")
	table.SetMinWidth(0, 20) // KEY
	table.SetMinWidth(1, 30) // VALUE
	table.SetMinWidth(2, 20) // UPDATED

	for _, ev := range resp.Result {
		maskedValue := maskValue(ev.Value)
		updated := ev.UpdatedAt
		if updated == "" {
			updated = ev.CreatedAt
		}
		table.AddRow(ev.Key, maskedValue, updated)
	}

	table.Render()
	return nil
}

// maskValue masks an env var value for display.
func maskValue(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

func runTestEnvSet(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]
	kvArg := args[1]

	// Parse KEY=VALUE
	idx := strings.Index(kvArg, "=")
	if idx <= 0 {
		ui.PrintError("Invalid format. Use KEY=VALUE (e.g. API_URL=https://example.com)")
		return fmt.Errorf("invalid KEY=VALUE format")
	}
	key := kvArg[:idx]
	value := kvArg[idx+1:]

	if key == "" {
		ui.PrintError("Key cannot be empty")
		return fmt.Errorf("empty key")
	}

	testID, client, err := envSetupClient(cmd, testNameOrID)
	if err != nil {
		return err
	}

	// Check if key already exists (upsert logic)
	ui.StartSpinner("Checking existing env vars...")
	existing, err := client.ListEnvVars(cmd.Context(), testID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to check existing env vars: %v", err)
		return err
	}

	var existingVar *api.EnvVar
	for _, ev := range existing.Result {
		if ev.Key == key {
			existingVar = &ev
			break
		}
	}

	if existingVar != nil {
		// Update existing
		ui.StartSpinner("Updating env var...")
		_, err = client.UpdateEnvVar(cmd.Context(), existingVar.ID, key, value)
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to update env var: %v", err)
			return err
		}
		ui.PrintSuccess("Updated '%s' for test '%s'", key, testNameOrID)
	} else {
		// Add new
		ui.StartSpinner("Adding env var...")
		_, err = client.AddEnvVar(cmd.Context(), testID, key, value)
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to add env var: %v", err)
			return err
		}
		ui.PrintSuccess("Added '%s' for test '%s'", key, testNameOrID)
	}

	return nil
}

func runTestEnvDelete(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]
	key := args[1]

	testID, client, err := envSetupClient(cmd, testNameOrID)
	if err != nil {
		return err
	}

	// Find env var by key
	ui.StartSpinner("Finding env var...")
	existing, err := client.ListEnvVars(cmd.Context(), testID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to list env vars: %v", err)
		return err
	}

	var found *api.EnvVar
	for _, ev := range existing.Result {
		if ev.Key == key {
			found = &ev
			break
		}
	}

	if found == nil {
		ui.PrintError("Env var '%s' not found for test '%s'", key, testNameOrID)
		return fmt.Errorf("env var not found")
	}

	ui.StartSpinner("Deleting env var...")
	err = client.DeleteEnvVar(cmd.Context(), found.ID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to delete env var: %v", err)
		return err
	}

	ui.PrintSuccess("Deleted '%s' from test '%s'", key, testNameOrID)
	return nil
}

func runTestEnvClear(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	if !testEnvForce {
		ui.PrintWarning("This will delete ALL env vars for test '%s'", testNameOrID)
		confirmed, promptErr := ui.PromptConfirm("Continue?", false)
		if promptErr != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	testID, client, err := envSetupClient(cmd, testNameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Clearing all env vars...")
	err = client.DeleteAllEnvVars(cmd.Context(), testID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to clear env vars: %v", err)
		return err
	}

	ui.PrintSuccess("Cleared all env vars for test '%s'", testNameOrID)
	return nil
}
