// Package main provides the `revyl test config` subcommand for editing
// a test's persisted run_config (TestRunConfig).
//
// The values written here live in tests.run_config (JSONB) and apply to
// every future run of the test that doesn't pass a per-run override.
package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

// testConfigCmd is the parent command for run_config edits.
var testConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "View and edit a test's persisted run configuration",
	Long: `View and edit a test's persisted run configuration (run_config).

Values set here apply to every future run of the test unless overridden
on the command line (e.g. ` + "`--fail-fast`" + `, ` + "`--location`" + `).

SUPPORTED FIELDS:
  fail-fast       Halt on first failed step or validation (true|false)
  location        Initial GPS location as lat,lng (e.g. 37.7749,-122.4194)
  orientation     Initial device orientation (portrait|landscape)

EXAMPLES:
  revyl test config show login-flow
  revyl test config set login-flow fail-fast true
  revyl test config set login-flow location 37.7749,-122.4194
  revyl test config unset login-flow location`,
}

var testConfigShowCmd = &cobra.Command{
	Use:   "show <test-name|id>",
	Short: "Print the test's persisted run_config",
	Args:  cobra.ExactArgs(1),
	RunE:  runTestConfigShow,
}

var testConfigSetCmd = &cobra.Command{
	Use:   "set <test-name|id> <field> <value>",
	Short: "Set a run_config field on the test",
	Args:  cobra.ExactArgs(3),
	RunE:  runTestConfigSet,
}

var testConfigUnsetCmd = &cobra.Command{
	Use:   "unset <test-name|id> <field>",
	Short: "Clear a run_config field on the test",
	Args:  cobra.ExactArgs(2),
	RunE:  runTestConfigUnset,
}

func init() {
	testConfigCmd.AddCommand(testConfigShowCmd)
	testConfigCmd.AddCommand(testConfigSetCmd)
	testConfigCmd.AddCommand(testConfigUnsetCmd)
}

func runTestConfigShow(cmd *cobra.Command, args []string) error {
	testID, client, err := resolveTestClient(cmd, args[0])
	if err != nil {
		return err
	}
	test, err := client.GetTest(cmd.Context(), testID)
	if err != nil {
		return fmt.Errorf("get test: %w", err)
	}
	rc := test.RunConfig
	if rc == nil {
		rc = map[string]interface{}{}
	}
	out, err := json.MarshalIndent(rc, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func runTestConfigSet(cmd *cobra.Command, args []string) error {
	testNameOrID, field, value := args[0], args[1], args[2]
	testID, client, err := resolveTestClient(cmd, testNameOrID)
	if err != nil {
		return err
	}
	test, err := client.GetTest(cmd.Context(), testID)
	if err != nil {
		return fmt.Errorf("get test: %w", err)
	}
	rc := test.RunConfig
	if rc == nil {
		rc = map[string]interface{}{}
	}
	if err := applyConfigSet(rc, field, value); err != nil {
		return err
	}
	if _, err := client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{
		TestID:    testID,
		RunConfig: rc,
	}); err != nil {
		return fmt.Errorf("update test: %w", err)
	}
	ui.PrintSuccess("Set %s = %s on test %s", field, value, testID)
	return nil
}

func runTestConfigUnset(cmd *cobra.Command, args []string) error {
	testNameOrID, field := args[0], args[1]
	testID, client, err := resolveTestClient(cmd, testNameOrID)
	if err != nil {
		return err
	}
	test, err := client.GetTest(cmd.Context(), testID)
	if err != nil {
		return fmt.Errorf("get test: %w", err)
	}
	rc := test.RunConfig
	if rc == nil {
		ui.PrintInfo("Field %s is already unset (no run_config on test)", field)
		return nil
	}
	if err := applyConfigUnset(rc, field); err != nil {
		return err
	}
	if _, err := client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{
		TestID:    testID,
		RunConfig: rc,
	}); err != nil {
		return fmt.Errorf("update test: %w", err)
	}
	ui.PrintSuccess("Cleared %s on test %s", field, testID)
	return nil
}

// applyConfigSet mutates rc in-place to set the named field. Knows the
// shape of TestRunConfig — top-level fields like fail_fast vs nested ones
// under execution_mode (location, orientation).
func applyConfigSet(rc map[string]interface{}, field, value string) error {
	switch field {
	case "fail-fast", "fail_fast":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("fail-fast must be true or false, got %q", value)
		}
		rc["fail_fast"] = b
	case "location":
		lat, lng, err := parseLocation(value)
		if err != nil {
			return err
		}
		em := nestedExecutionMode(rc)
		em["initial_location"] = map[string]interface{}{
			"latitude":  lat,
			"longitude": lng,
		}
	case "orientation":
		v := strings.ToLower(value)
		if v != "portrait" && v != "landscape" {
			return fmt.Errorf("orientation must be portrait or landscape, got %q", value)
		}
		em := nestedExecutionMode(rc)
		em["initial_orientation"] = v
	default:
		return unknownFieldError(field)
	}
	return nil
}

// applyConfigUnset clears the named field. For nested fields, leaves
// execution_mode in place if it still holds other keys.
func applyConfigUnset(rc map[string]interface{}, field string) error {
	switch field {
	case "fail-fast", "fail_fast":
		// fail_fast has a non-nullable default of false on the backend,
		// so "unset" means restore the default.
		rc["fail_fast"] = false
	case "location":
		if em, ok := rc["execution_mode"].(map[string]interface{}); ok {
			delete(em, "initial_location")
		}
	case "orientation":
		if em, ok := rc["execution_mode"].(map[string]interface{}); ok {
			delete(em, "initial_orientation")
		}
	default:
		return unknownFieldError(field)
	}
	return nil
}

// nestedExecutionMode returns rc["execution_mode"] as a map, creating
// it if absent. Lets callers write nested keys without nil checks.
func nestedExecutionMode(rc map[string]interface{}) map[string]interface{} {
	if existing, ok := rc["execution_mode"].(map[string]interface{}); ok {
		return existing
	}
	em := map[string]interface{}{}
	rc["execution_mode"] = em
	return em
}

func unknownFieldError(field string) error {
	return fmt.Errorf("unknown field %q (supported: fail-fast, location, orientation)", field)
}
