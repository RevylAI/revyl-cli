package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

const shiptokVideoMaxBytes int64 = 100 * 1024 * 1024

var shiptokRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,161}$`)

func newShiptokCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "shiptok",
		Short: "Host videos for ShipTok",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(newShiptokUploadCommand())
	return command
}

func newShiptokUploadCommand() *cobra.Command {
	var repository string
	var pullRequestNumber, issueNumber int
	command := &cobra.Command{
		Use:   "upload <file>",
		Short: "Upload a video and post it on a GitHub pull request or issue",
		Long: `Upload an MP4 or MOV video up to 100 MB (104857600 bytes).

The returned video link does not expire. Anyone with the link can view it.
Supply --repo and exactly one of --pr or --issue. Revyl posts a tagged
ShipTok comment or replaces its existing comment on that pull request or issue.`,
		Example: `  revyl shiptok upload ./demo.mov --repo owner/repository --pr 42
  revyl shiptok upload ./weekly.mp4 --repo owner/repository --issue 7 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pullRequestSet := cmd.Flags().Changed("pr")
			issueSet := cmd.Flags().Changed("issue")
			if !shiptokRepositoryPattern.MatchString(repository) {
				return usageErrorf("--repo is required and must name a GitHub repository in OWNER/REPO form")
			}
			if pullRequestSet == issueSet {
				return usageErrorf("supply exactly one of --pr or --issue")
			}
			target := api.ShiptokVideoUploadTarget{GithubRepository: repository}
			if pullRequestSet {
				target.GithubNumber = pullRequestNumber
				target.GithubTargetKind = "pull_request"
			} else {
				target.GithubNumber = issueNumber
				target.GithubTargetKind = "issue"
			}
			if target.GithubNumber < 1 || target.GithubNumber > 2_147_483_647 {
				return usageErrorf("--pr and --issue must be between 1 and 2147483647")
			}
			extension := strings.ToLower(filepath.Ext(args[0]))
			if extension != ".mp4" && extension != ".mov" {
				return usageErrorf("ShipTok uploads require an MP4 or MOV file")
			}
			info, err := os.Stat(args[0])
			if err != nil {
				return fmt.Errorf("inspect video: %w", err)
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return usageErrorf("ShipTok uploads require a non-empty regular file")
			}
			if info.Size() > shiptokVideoMaxBytes {
				return usageErrorf("ShipTok videos must be at most 100 MB (104857600 bytes)")
			}
			file, err := os.Open(args[0])
			if err != nil {
				return fmt.Errorf("open video: %w", err)
			}
			defer file.Close()
			apiKey, err := getAPIKey()
			if err != nil {
				return err
			}
			jsonOutput, _ := cmd.Flags().GetBool("json")
			quiet, _ := cmd.Flags().GetBool("quiet")
			if !jsonOutput && !quiet {
				cmd.PrintErrln("Uploading ShipTok video...")
			}
			client := api.NewClientWithDevMode(apiKey, commandDevMode(cmd))
			video, err := client.UploadShiptokVideo(cmd.Context(), file, target)
			if err != nil {
				return fmt.Errorf("upload ShipTok video: %w", err)
			}
			uploadStatus := "uploaded"
			if video.CommentError != nil {
				uploadStatus = "comment_failed"
			}
			analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
				Domain: "shiptok_upload", DomainStatus: uploadStatus,
			})
			if jsonOutput {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(video); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintln(cmd.OutOrStdout(), video.Url); err != nil {
				return err
			}
			if video.CommentError != nil {
				return analytics.WithSafeDiagnostic(
					fmt.Errorf("video uploaded, but GitHub comment failed: %s", *video.CommentError),
					"video uploaded, but GitHub comment failed",
				)
			}
			return nil
		},
	}
	command.Flags().StringVar(&repository, "repo", "", "GitHub repository in OWNER/REPO form")
	command.Flags().IntVar(&pullRequestNumber, "pr", 0, "Pull request number to post the video on")
	command.Flags().IntVar(&issueNumber, "issue", 0, "Issue number to post the video on")
	_ = command.MarkFlagRequired("repo")
	command.MarkFlagsMutuallyExclusive("pr", "issue")
	return command
}
