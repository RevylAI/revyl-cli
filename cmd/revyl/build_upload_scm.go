package main

import (
	"fmt"

	"github.com/revyl/cli/internal/build"
	"github.com/spf13/cobra"
)

func registerBuildUploadSCMFlags(cmd *cobra.Command) {
	cmd.Flags().String("commit", "", "Full PR head commit SHA; requires --repo and overrides detected SCM identity")
	cmd.Flags().String("repo", "", "Base GitHub repository (owner/name); requires --commit")
	cmd.Flags().Int("pr", 0, "PR number; requires --repo and --commit (omit to match by head SHA)")
}

func applyBuildUploadSCMFlags(cmd *cobra.Command) error {
	flags := cmd.Flags()
	if !flags.Changed("commit") && !flags.Changed("repo") && !flags.Changed("pr") {
		return nil
	}
	if !flags.Changed("commit") || !flags.Changed("repo") {
		return fmt.Errorf("explicit SCM metadata requires both --repo owner/name and --commit <full-pr-head-sha>")
	}
	commitSHA, err := flags.GetString("commit")
	if err != nil {
		return err
	}
	repository, err := flags.GetString("repo")
	if err != nil {
		return err
	}
	reviewNumber, err := flags.GetInt("pr")
	if err != nil {
		return err
	}
	if flags.Changed("pr") && reviewNumber <= 0 {
		return fmt.Errorf("invalid --pr: supply a positive PR number or omit the flag to match by commit")
	}
	ctx, err := build.WithUploadSCMContext(cmd.Context(), repository, commitSHA, reviewNumber)
	if err != nil {
		return err
	}
	cmd.SetContext(ctx)
	return nil
}
