// Package main provides the `revyl app storekit` subcommand for managing the
// iOS StoreKit config attached to an app. The app-level config is the default
// for every iOS test that runs against the app; tests can override it via
// `revyl test storekit`.
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

var appStoreKitCmd = &cobra.Command{
	Use:   "storekit",
	Short: "Manage the iOS StoreKit config attached to an app",
	Long: `Manage the iOS StoreKit Testing configuration attached to an app.

An app-level StoreKit config is the default for every iOS test that runs
against the app. Individual tests can override or disable it
(see ` + "`revyl test storekit`" + `).

EXAMPLES:
  revyl app storekit set "My App" ./MyApp.storekit          # upload + attach in one step
  revyl app storekit set "My App" --file-id MyApp.storekit  # attach an already-uploaded file (name or ID)
  revyl app storekit show "My App"
  revyl app storekit disable "My App"                # explicitly turn StoreKit off
  revyl app storekit clear "My App"                  # remove the config (inherit default)`,
}

var appStoreKitFileID string

var appStoreKitSetCmd = &cobra.Command{
	Use:   "set <app-name|id> [file.storekit]",
	Short: "Upload and attach a .storekit config to an app",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runAppStoreKitSet,
}

var appStoreKitShowCmd = &cobra.Command{
	Use:   "show <app-name|id>",
	Short: "Show the StoreKit config attached to an app",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppStoreKitShow,
}

var appStoreKitDisableCmd = &cobra.Command{
	Use:   "disable <app-name|id>",
	Short: "Explicitly disable StoreKit for an app",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppStoreKitDisable,
}

var appStoreKitClearCmd = &cobra.Command{
	Use:   "clear <app-name|id>",
	Short: "Remove the StoreKit config from an app",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppStoreKitClear,
}

func init() {
	appStoreKitSetCmd.Flags().StringVar(&appStoreKitFileID, "file-id", "", "Attach an already-uploaded org file by name or ID instead of uploading a local file")
	appStoreKitCmd.AddCommand(appStoreKitSetCmd, appStoreKitShowCmd, appStoreKitDisableCmd, appStoreKitClearCmd)
}

// appStoreKitScope resolves the app name/ID and returns a client plus the
// scope identity used by the shared storeKit* helpers.
func appStoreKitScope(cmd *cobra.Command, nameOrID string) (client *api.Client, scopeID, scopeLabel string, err error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, "", "", err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client = api.NewClientWithDevMode(apiKey, devMode)

	appID, appName, err := resolveAppNameOrID(cmd, client, nameOrID)
	if err != nil {
		return nil, "", "", err
	}
	return client, appID, fmt.Sprintf("app %q", appName), nil
}

func runAppStoreKitSet(cmd *cobra.Command, args []string) error {
	filePath := ""
	if len(args) > 1 {
		filePath = args[1]
	}
	if filePath == "" && appStoreKitFileID == "" {
		return fmt.Errorf("provide a .storekit file path or --file-id")
	}
	client, scopeID, label, err := appStoreKitScope(cmd, args[0])
	if err != nil {
		return err
	}
	return storeKitSet(cmd, client, "app", scopeID, label, filePath, appStoreKitFileID)
}

func runAppStoreKitShow(cmd *cobra.Command, args []string) error {
	client, scopeID, label, err := appStoreKitScope(cmd, args[0])
	if err != nil {
		return err
	}
	return storeKitShow(cmd, client, "app", scopeID, label)
}

func runAppStoreKitDisable(cmd *cobra.Command, args []string) error {
	client, scopeID, label, err := appStoreKitScope(cmd, args[0])
	if err != nil {
		return err
	}
	return storeKitDisable(cmd, client, "app", scopeID, label)
}

func runAppStoreKitClear(cmd *cobra.Command, args []string) error {
	client, scopeID, label, err := appStoreKitScope(cmd, args[0])
	if err != nil {
		return err
	}
	return storeKitClear(cmd, client, "app", scopeID, label)
}
