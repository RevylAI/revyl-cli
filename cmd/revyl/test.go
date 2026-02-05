// Package main provides the test command for test management.
package main

import (
	"github.com/spf13/cobra"
)

// testCmd is the parent command for test management operations.
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Manage test definitions",
	Long: `Manage local and remote test definitions.

COMMANDS:
  list      - List tests with sync status
  remote    - List all tests in your organization
  push      - Push local test changes to remote
  pull      - Pull remote test changes to local
  diff      - Show diff between local and remote
  validate  - Validate YAML test files

To run a test, use: revyl run test <name>
To run with build:  revyl run test <name> --build

EXAMPLES:
  revyl test list                    # List tests with sync status
  revyl test push login-flow         # Push local changes to remote
  revyl test pull login-flow         # Pull remote changes to local
  revyl test diff login-flow         # Show diff between local and remote`,
}

func init() {
	// Add management subcommands
	testCmd.AddCommand(testsListCmd)
	testCmd.AddCommand(testsRemoteCmd)
	testCmd.AddCommand(testsValidateCmd)
	testCmd.AddCommand(testsPushCmd)
	testCmd.AddCommand(testsPullCmd)
	testCmd.AddCommand(testsDiffCmd)
}
