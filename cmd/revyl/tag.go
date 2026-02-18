// Package main provides the tag command for tag management.
package main

import (
	"github.com/spf13/cobra"
)

// Tag command flags
var (
	tagListJSON   bool
	tagListSearch string

	tagCreateColor string

	tagUpdateName        string
	tagUpdateColor       string
	tagUpdateDescription string

	tagDeleteForce bool
)

// tagCmd is the parent command for tag operations.
var tagCmd = &cobra.Command{
	Use:   "tag",
	Short: "Manage test tags",
	Long: `Manage tags for organizing and categorizing tests.

Tags can be created, updated, and deleted at the organization level.
Tests can have multiple tags assigned to them for filtering and grouping.

COMMANDS:
  list    - List all tags with test counts
  create  - Create a new tag
  update  - Update a tag's properties
  delete  - Delete a tag
  get     - Show tags for a specific test
  set     - Replace all tags on a test
  add     - Add tags to a test (keep existing)
  remove  - Remove tags from a test

EXAMPLES:
  revyl tag list
  revyl tag list --search regression
  revyl tag create regression --color "#22C55E"
  revyl tag get my-test
  revyl tag set my-test regression,smoke
  revyl tag add my-test login
  revyl tag remove my-test smoke`,
}

// tagListCmd lists all tags.
var tagListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tags with test counts",
	Long: `List all tags in your organization with the number of tests using each tag.

EXAMPLES:
  revyl tag list
  revyl tag list --json
  revyl tag list --search regression`,
	RunE: runTagList,
}

// tagCreateCmd creates a new tag.
var tagCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new tag",
	Long: `Create a new tag. If a tag with the same name already exists, it will be returned.

EXAMPLES:
  revyl tag create regression
  revyl tag create smoke --color "#22C55E"`,
	Args: cobra.ExactArgs(1),
	RunE: runTagCreate,
}

// tagUpdateCmd updates an existing tag.
var tagUpdateCmd = &cobra.Command{
	Use:   "update <name|id>",
	Short: "Update a tag's properties",
	Long: `Update a tag's name, color, or description.

EXAMPLES:
  revyl tag update regression --color "#22C55E"
  revyl tag update regression --name "regression-v2"
  revyl tag update regression --description "Regression test suite"`,
	Args: cobra.ExactArgs(1),
	RunE: runTagUpdate,
}

// tagDeleteCmd deletes a tag.
var tagDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a tag",
	Long: `Delete a tag. This removes it from all tests that use it.

EXAMPLES:
  revyl tag delete regression
  revyl tag delete regression --force`,
	Args: cobra.ExactArgs(1),
	RunE: runTagDelete,
}

// tagGetCmd shows tags for a specific test.
var tagGetCmd = &cobra.Command{
	Use:   "get <test-name|id>",
	Short: "Show tags for a specific test",
	Long: `Show all tags assigned to a specific test.

EXAMPLES:
  revyl tag get my-test
  revyl tag get my-test --json`,
	Args: cobra.ExactArgs(1),
	RunE: runTagGet,
}

// tagSetCmd replaces all tags on a test.
var tagSetCmd = &cobra.Command{
	Use:     "set <test-name|id> <tag1,tag2,...>",
	Short:   "Replace all tags on a test",
	PreRunE: enforceOrgBindingMatch,
	Long: `Replace all tags on a test with the given comma-separated list.
Tags are auto-created if they don't exist.

EXAMPLES:
  revyl tag set my-test regression,smoke
  revyl tag set my-test login`,
	Args: cobra.ExactArgs(2),
	RunE: runTagSet,
}

// tagAddCmd adds tags to a test.
var tagAddCmd = &cobra.Command{
	Use:     "add <test-name|id> <tag1,tag2,...>",
	Short:   "Add tags to a test (keep existing)",
	PreRunE: enforceOrgBindingMatch,
	Long: `Add tags to a test without removing existing tags.
Tags are auto-created if they don't exist.

EXAMPLES:
  revyl tag add my-test regression,smoke
  revyl tag add my-test login`,
	Args: cobra.ExactArgs(2),
	RunE: runTagAdd,
}

// tagRemoveCmd removes tags from a test.
var tagRemoveCmd = &cobra.Command{
	Use:     "remove <test-name|id> <tag1,tag2,...>",
	Short:   "Remove tags from a test",
	PreRunE: enforceOrgBindingMatch,
	Long: `Remove specific tags from a test.

EXAMPLES:
  revyl tag remove my-test smoke
  revyl tag remove my-test regression,smoke`,
	Args: cobra.ExactArgs(2),
	RunE: runTagRemove,
}

func init() {
	tagCmd.AddCommand(tagListCmd)
	tagCmd.AddCommand(tagCreateCmd)
	tagCmd.AddCommand(tagUpdateCmd)
	tagCmd.AddCommand(tagDeleteCmd)
	tagCmd.AddCommand(tagGetCmd)
	tagCmd.AddCommand(tagSetCmd)
	tagCmd.AddCommand(tagAddCmd)
	tagCmd.AddCommand(tagRemoveCmd)

	// tag list flags
	tagListCmd.Flags().BoolVar(&tagListJSON, "json", false, "Output results as JSON")
	tagListCmd.Flags().StringVar(&tagListSearch, "search", "", "Filter tags by name")

	// tag create flags
	tagCreateCmd.Flags().StringVar(&tagCreateColor, "color", "", "Tag color (hex, e.g. #22C55E)")

	// tag update flags
	tagUpdateCmd.Flags().StringVar(&tagUpdateName, "name", "", "New tag name")
	tagUpdateCmd.Flags().StringVar(&tagUpdateColor, "color", "", "New tag color (hex)")
	tagUpdateCmd.Flags().StringVar(&tagUpdateDescription, "description", "", "New tag description")

	// tag delete flags
	tagDeleteCmd.Flags().BoolVarP(&tagDeleteForce, "force", "f", false, "Skip confirmation prompt")
}
