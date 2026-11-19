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
	"github.com/revyl/cli/internal/gitremote"
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
	var pullRequestNumber int
	command := &cobra.Command{
		Use:   "upload <file>",
		Short: "Upload a video and print the #shiptok line to paste on a pull request",
		Long: `Upload an MP4 or MOV video up to 100 MB (104857600 bytes) and print
"#shiptok <link>". Paste it in a pull request's description or a comment, and
ShipTok plays that video first for the pull request.

The video belongs to the GitHub repository of this checkout's origin remote,
or the one --repo names, and the Revyl GitHub App must be installed on it. The
link does not expire. Anyone with it can watch a video on a public repository;
on a private repository, only people GitHub lets read it can.

With --pr, the Revyl GitHub App also posts the video in a tagged comment on
that pull request, or updates its own earlier ShipTok comment there.`,
		Example: `  revyl shiptok upload ./demo.mp4
  revyl shiptok upload ./demo.mov --pr 42
  revyl shiptok upload ./demo.mp4 --repo owner/repository --pr 42 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("repo") {
				origin, err := gitremote.OriginURL(".")
				if err != nil || origin == "" {
					return usageErrorf("no origin remote here: run this in a checkout of the GitHub repository, or pass --repo OWNER/REPO")
				}
				owner, name, ok := gitremote.ParseGithubRemote(origin)
				if !ok {
					return usageErrorf("the origin remote isn't a github.com repository: pass --repo OWNER/REPO")
				}
				repository = owner + "/" + name
			}
			if !shiptokRepositoryPattern.MatchString(repository) {
				return usageErrorf("--repo must name a GitHub repository in OWNER/REPO form")
			}
			target := api.ShiptokVideoUploadTarget{GithubRepository: repository}
			if cmd.Flags().Changed("pr") {
				if pullRequestNumber < 1 || pullRequestNumber > 2_147_483_647 {
					return usageErrorf("--pr must be between 1 and 2147483647")
				}
				target.GithubPullRequestNumber = pullRequestNumber
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
			} else if _, err := fmt.Fprintln(cmd.OutOrStdout(), "#shiptok "+video.Url); err != nil {
				return err
			}
			if video.CommentUrl != nil && !jsonOutput && !quiet {
				cmd.PrintErrf("Revyl's GitHub App %s its ShipTok comment: %s\n", video.CommentAction, *video.CommentUrl)
			}
			if video.CommentError != nil {
				return analytics.WithSafeDiagnostic(
					fmt.Errorf("video uploaded, but GitHub comment failed: %s", *video.CommentError),
					"video uploaded, but GitHub comment failed",
				)
			}
			commentPublished := video.CommentAction == api.ShiptokVideoUploadResponseCommentActionPosted ||
				video.CommentAction == api.ShiptokVideoUploadResponseCommentActionEdited
			if target.GithubPullRequestNumber != 0 && (!commentPublished || video.CommentUrl == nil) {
				return analytics.WithSafeDiagnostic(
					fmt.Errorf("video uploaded, but Revyl didn't confirm its GitHub comment on pull request #%d", target.GithubPullRequestNumber),
					"video uploaded, but Revyl didn't confirm its GitHub comment",
				)
			}
			return nil
		},
	}
	command.Flags().StringVar(&repository, "repo", "", "GitHub repository in OWNER/REPO form (default: this checkout's origin remote)")
	command.Flags().IntVar(&pullRequestNumber, "pr", 0, "Pull request number the Revyl GitHub App also posts the video on")
	return command
}
