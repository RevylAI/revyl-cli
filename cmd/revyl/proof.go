// Package main provides the proof command group.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var (
	proofCommentProblem string
	proofCommentBlocked string
)

// proofCmd is the parent command for proof-of-changes operations. Hidden
// because an ordinary API key cannot use it: the proof sandbox agent is told
// about it by the prompt, and `revyl proof comment --help` still works there.
var proofCmd = &cobra.Command{
	Use:    "proof",
	Short:  "Report on a Revyl proof-of-changes run",
	Hidden: true,
	Long: `Report on a Revyl proof-of-changes run.

These commands work only inside a Revyl proof sandbox, where REVYL_API_KEY is a
credential minted for one pull request.

COMMANDS:
  comment - Publish your write-up onto the pull request under proof`,
}

// proofCommentCmd relays a proof agent's markdown onto the pull request.
var proofCommentCmd = &cobra.Command{
	Use:   "comment <file>",
	Short: "Publish your write-up onto the pull request under proof",
	Long: `Publish your write-up onto the pull request being proved.

Revyl keeps one comment per pull request and renders your markdown inside it,
so this replaces your previous write-up rather than adding another comment.
Post early with what you have and post again as you learn more.

Which pull request you are writing to comes from your credential, so there is
nothing to point this at.

Say what you did and what the app did. Use --problem only when the pull request
itself is broken, and --blocked when you never got to see it run at all, each in
one sentence a reviewer can act on; the write-up carries the detail. They are
mutually exclusive, and sending neither says you watched the change work.

Images must be URLs from ` + "`revyl session publish`" + `; Revyl drops any
other image.

Examples:
  revyl proof comment write-up.md
  revyl proof comment write-up.md --problem "Saving a note clears the title field"
  revyl proof comment write-up.md --blocked "No device could be started, so nothing was exercised"`,
	Args: cobra.ExactArgs(1),
	RunE: runProofComment,
}

func init() {
	proofCommentCmd.Flags().StringVar(
		&proofCommentProblem,
		"problem",
		"",
		"One sentence naming what is broken, if the pull request is broken",
	)
	proofCommentCmd.Flags().StringVar(
		&proofCommentBlocked,
		"blocked",
		"",
		"One sentence naming what stopped you observing the change, if nothing was observed",
	)
	proofCommentCmd.MarkFlagsMutuallyExclusive("problem", "blocked")

	proofCmd.AddCommand(proofCommentCmd)
}

// runProofComment reads the write-up and relays it to the pull request.
func runProofComment(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	path := strings.TrimSpace(args[0])
	raw, err := os.ReadFile(path)
	if err != nil {
		ui.PrintError("Could not read %s: %v", path, err)
		return err
	}
	body := strings.TrimSpace(string(raw))
	if body == "" {
		ui.PrintError("%s is empty; there is nothing to publish", path)
		return fmt.Errorf("empty write-up")
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	} else {
		ui.StartSpinner("Publishing write-up...")
	}

	err = client.PublishProofComment(
		cmd.Context(),
		body,
		strings.TrimSpace(proofCommentProblem),
		strings.TrimSpace(proofCommentBlocked),
	)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to publish the write-up: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := marshalPrettyJSON(map[string]interface{}{})
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Write-up published to the pull request")
	return nil
}
