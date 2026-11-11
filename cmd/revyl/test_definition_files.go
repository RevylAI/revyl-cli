package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/checkoutdefinitions"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/util"
)

const (
	fromCheckoutFlagName  = "from-checkout"
	fromCheckoutFlagUsage = "Run this checkout's test, module and script files instead of the saved copies (default: run_test_definitions_from_git in .revyl/config.yaml)"
)

// resolveFromCheckoutProject returns the project whose checkout files a run
// sends, or nil when it sends none. An explicit --from-checkout needs a config
// found walking up from the working directory; without the flag, the config's
// run_test_definitions_from_git decides, and no usable config sends nothing.
func resolveFromCheckoutProject(cmd *cobra.Command) (project *config.ProjectContext, err error) {
	defer func() {
		if err == nil {
			analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
				Properties: map[string]interface{}{"from_checkout": project != nil},
			})
		}
	}()
	var fromCheckout *bool
	if cmd.Flags().Changed(fromCheckoutFlagName) {
		fromCheckout = &runFromCheckout
	}
	if fromCheckout != nil && !*fromCheckout {
		return nil, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}
	project, err = config.ResolveProjectContext(cwd, "")
	if err != nil {
		code := "config_unavailable"
		var configErr *config.ConfigError
		if errors.As(err, &configErr) {
			code = configErr.Code
		}
		if fromCheckout == nil {
			if code != "config_not_found" && code != "git_worktree_unavailable" {
				ui.PrintWarning("Running the saved tests, because .revyl/config.yaml can't be read (%s): %v", code, actionableLocalConfigMessage(err))
			}
			return nil, nil
		}
		return nil, analytics.WithSafeDiagnostic(
			fmt.Errorf("--from-checkout needs this checkout's .revyl/config.yaml (%s): %w", code, actionableLocalConfigMessage(err)),
			"from-checkout project config unavailable: "+code,
		)
	}
	return checkoutdefinitions.RunProject(project, fromCheckout)
}

// collectCheckoutUpload gathers what a run from the checkout sends for testIDs
// and prints the warnings it found.
func collectCheckoutUpload(cmd *cobra.Command, project *config.ProjectContext, testIDs []string) (*checkoutdefinitions.Upload, error) {
	upload, err := checkoutdefinitions.CollectUpload(cmd.Context(), project, testIDs, "rerun with --from-checkout=false")
	if err != nil {
		return nil, err
	}
	for _, warning := range upload.Warnings {
		ui.PrintWarning("%s", warning)
	}
	return upload, nil
}

// printDefinitionLine writes what a run of an opted-in project executed to
// stderr even in quiet and JSON modes, on its own line so CI logs and
// `grep '^Definition:'` find every saved copy and fallback. A nil upload is a
// run that sent no files.
func printDefinitionLine(cmd *cobra.Command, upload *checkoutdefinitions.Upload, testCountBySource map[api.TestDefinitionSource]int, testCountByFallbackReason map[api.TestDefinitionFallbackReason]int, gitRead api.TestDefinitionGitRead) {
	if len(testCountBySource) == 0 {
		return
	}
	ui.EndStatusLine()
	fmt.Fprintf(cmd.ErrOrStderr(), "Definition: %s\n", upload.DefinitionLine(testCountBySource, testCountByFallbackReason, gitRead))
}

// addTestDefinitionGitRead adds to a run's JSON output where the server read
// its test definition files in Git, when it said.
func addTestDefinitionGitRead(output map[string]interface{}, gitRead api.TestDefinitionGitRead) {
	for key, value := range map[string]string{
		"test_definition_git_branch_name":         gitRead.TestDefinitionGitBranchName,
		"test_definition_git_commit_sha":          gitRead.TestDefinitionGitCommitSHA,
		"test_definition_missing_git_branch_name": gitRead.TestDefinitionMissingGitBranchName,
	} {
		if value != "" {
			output[key] = value
		}
	}
}

// ensureCheckoutTestFileLinked gives a checkout test file without
// _meta.remote_id its saved test before a --from-checkout run, which needs the
// test's id. Only an interactive terminal creates it; anywhere else the run
// stops with the push command to run.
func ensureCheckoutTestFileLinked(cmd *cobra.Command, client *api.Client, project *config.ProjectContext, alias string) error {
	path, err := util.SafeTestPath(project.TestsDir, alias)
	if err != nil {
		return nil
	}
	localTest, err := config.LoadLocalTest(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	relativePath := checkoutdefinitions.ProjectRelativePath(project, path)
	if err != nil {
		return fmt.Errorf("%s: %w", relativePath, err)
	}
	if strings.TrimSpace(localTest.Meta.RemoteID) != "" {
		return nil
	}
	testName := strings.TrimSuffix(filepath.Base(path), ".yaml")
	pushCommand := "revyl test push " + testName
	if runOutputJSON || runGitHubActions || !ui.IsInteractive() {
		return analytics.WithSafeDiagnostic(
			fmt.Errorf("%s has no _meta.remote_id, so its test doesn't exist in Revyl yet; run `%s`, commit the id it writes into the file, then rerun", relativePath, pushCommand),
			"from-checkout test file has no remote id",
		)
	}
	create, err := ui.PromptConfirm(fmt.Sprintf("%s has no saved test yet. Create it now?", relativePath), true)
	if err != nil {
		return err
	}
	if !create {
		return fmt.Errorf("create the test with `%s`, then rerun", pushCommand)
	}
	localTests, err := config.LoadLocalTests(project.TestsDir)
	if err != nil {
		return err
	}
	plan, err := planModuleAndScriptFilePush(cmd.Context(), client, project, localTests, []string{testName}, false)
	if err != nil {
		return err
	}
	if err := pushLocalTests(cmd, client, project, sync.NewResolver(client, nil, localTests), plan, testName, []string{path}, false); err != nil {
		return fmt.Errorf("the run didn't start, because creating the test for %s failed: %w", relativePath, err)
	}
	return nil
}
