// Package main provides the `revyl test storekit` subcommand for managing the
// iOS StoreKit config attached to a single test. A test-level config overrides
// the app-level default at run time (resolution precedence: test > build > app).
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

var testStoreKitCmd = &cobra.Command{
	Use:   "storekit",
	Short: "Manage the iOS StoreKit config attached to a test",
	Long: `Manage the iOS StoreKit Testing configuration attached to a single test.

A test-level StoreKit config overrides the app-level default when the test
runs. Use ` + "`disable`" + ` to force StoreKit off for one test even if its app
sets a default, or ` + "`clear`" + ` to fall back to the app default.

EXAMPLES:
  revyl test storekit set login-flow ./Premium.storekit          # upload + attach in one step
  revyl test storekit set login-flow --file-id Premium.storekit  # attach an already-uploaded file (name or ID)
  revyl test storekit show login-flow
  revyl test storekit disable login-flow                  # force StoreKit off for this test
  revyl test storekit clear login-flow                    # fall back to the app default`,
}

var testStoreKitFileID string

var testStoreKitSetCmd = &cobra.Command{
	Use:   "set <test-name|id> [file.storekit]",
	Short: "Upload and attach a .storekit config to a test",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runTestStoreKitSet,
}

var testStoreKitShowCmd = &cobra.Command{
	Use:   "show <test-name|id>",
	Short: "Show the StoreKit config attached to a test",
	Args:  cobra.ExactArgs(1),
	RunE:  runTestStoreKitShow,
}

var testStoreKitDisableCmd = &cobra.Command{
	Use:   "disable <test-name|id>",
	Short: "Explicitly disable StoreKit for a test",
	Args:  cobra.ExactArgs(1),
	RunE:  runTestStoreKitDisable,
}

var testStoreKitClearCmd = &cobra.Command{
	Use:   "clear <test-name|id>",
	Short: "Remove the StoreKit config from a test (inherit app default)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTestStoreKitClear,
}

func init() {
	testStoreKitSetCmd.Flags().StringVar(&testStoreKitFileID, "file-id", "", "Attach an already-uploaded org file by name or ID instead of uploading a local file")
	testStoreKitCmd.AddCommand(testStoreKitSetCmd, testStoreKitShowCmd, testStoreKitDisableCmd, testStoreKitClearCmd)
}

// testStoreKitScope resolves the test name/ID to a client and scope identity,
// mirroring appStoreKitScope. The label prefers the canonical test name.
func testStoreKitScope(cmd *cobra.Command, nameOrID string) (client *api.Client, testID, label string, err error) {
	testID, client, err = resolveTestClient(cmd, nameOrID)
	if err != nil {
		return nil, "", "", err
	}
	label = fmt.Sprintf("test %q", nameOrID)
	if t, getErr := client.GetTest(cmd.Context(), testID); getErr == nil && t.Name != "" {
		label = fmt.Sprintf("test %q", t.Name)
	}
	return client, testID, label, nil
}

func runTestStoreKitSet(cmd *cobra.Command, args []string) error {
	filePath := ""
	if len(args) > 1 {
		filePath = args[1]
	}
	if filePath == "" && testStoreKitFileID == "" {
		return fmt.Errorf("provide a .storekit file path or --file-id")
	}
	client, testID, label, err := testStoreKitScope(cmd, args[0])
	if err != nil {
		return err
	}
	return storeKitSet(cmd, client, "test", testID, label, filePath, testStoreKitFileID)
}

func runTestStoreKitShow(cmd *cobra.Command, args []string) error {
	client, testID, label, err := testStoreKitScope(cmd, args[0])
	if err != nil {
		return err
	}
	return storeKitShow(cmd, client, "test", testID, label)
}

func runTestStoreKitDisable(cmd *cobra.Command, args []string) error {
	client, testID, label, err := testStoreKitScope(cmd, args[0])
	if err != nil {
		return err
	}
	return storeKitDisable(cmd, client, "test", testID, label)
}

func runTestStoreKitClear(cmd *cobra.Command, args []string) error {
	client, testID, label, err := testStoreKitScope(cmd, args[0])
	if err != nil {
		return err
	}
	return storeKitClear(cmd, client, "test", testID, label)
}
