package main

import (
	"os"

	"github.com/revyl/cli/internal/buildselection"
	"github.com/spf13/cobra"
)

func commandGitBranch(cmd *cobra.Command, workDir string) (string, error) {
	if commandBuildBranch(cmd) != "" {
		return "", nil
	}
	opts := buildselection.GitBranchOptions{WorkDir: workDir}
	if cmd.Flags().Changed("repo") || cmd.Flags().Changed("commit") {
		repository, _ := cmd.Flags().GetString("repo")
		commit, _ := cmd.Flags().GetString("commit")
		review, _ := cmd.Flags().GetInt("pr")
		opts.ArtifactIdentity = &buildselection.ArtifactSourceIdentity{Repository: repository, CommitSHA: commit, ReviewNumber: review}
	}
	result, err := buildselection.DetectGitBranch(cmd.Context(), opts)
	return result.Name, err
}

func commandWorkingGitBranch(cmd *cobra.Command) (string, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return commandGitBranch(cmd, workDir)
}

func resolveLocalGitBranch(cmd *cobra.Command, invocation projectBuildInvocation) (projectBuildInvocation, error) {
	if invocation.GitBranchResolved || invocation.BranchID != "" || invocation.BranchName != "" {
		return invocation, nil
	}
	invocation.BranchName = commandBuildBranch(cmd)
	hint, err := commandGitBranch(cmd, invocation.ProjectRoot)
	if err != nil {
		return invocation, err
	}
	invocation.GitBranchName = hint
	invocation.GitBranchResolved = true
	return invocation, nil
}
