// Package main provides the test command for the full build->upload->run workflow.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/status"
	"github.com/revyl/cli/internal/ui"
)

// testCmd runs the full workflow: build -> upload -> run test.
var testCmd = &cobra.Command{
	Use:   "test <name|id>",
	Short: "Build, upload, and run a test",
	Long: `Build the app, upload it, and run a test.

This is the main command for the typical development workflow.
It combines 'revyl build upload' and 'revyl run test' into one command.

Examples:
  revyl test login-flow                 # Full workflow with default build
  revyl test login-flow --variant release
  revyl test login-flow --skip-build    # Skip build, use existing artifact`,
	Args: cobra.ExactArgs(1),
	RunE: runFullTest,
}

var (
	testVariant   string
	testSkipBuild bool
	testRetries   int
	testNoWait    bool
	testOpen      bool
	testTimeout   int
)

func init() {
	testCmd.Flags().StringVar(&testVariant, "variant", "", "Build variant to use")
	testCmd.Flags().BoolVar(&testSkipBuild, "skip-build", false, "Skip build step")
	testCmd.Flags().IntVarP(&testRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	testCmd.Flags().BoolVar(&testNoWait, "no-wait", false, "Exit after test starts")
	testCmd.Flags().BoolVar(&testOpen, "open", true, "Open report in browser when complete")
	testCmd.Flags().IntVarP(&testTimeout, "timeout", "t", 3600, "Timeout in seconds")
}

// runFullTest executes the full build->upload->run workflow.
func runFullTest(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	// Resolve test ID from alias
	testID := testNameOrID
	if id, ok := cfg.Tests[testNameOrID]; ok {
		testID = id
	}

	// Determine build config
	buildCfg := cfg.Build
	var variant config.BuildVariant
	if testVariant != "" {
		var ok bool
		variant, ok = cfg.Build.Variants[testVariant]
		if !ok {
			ui.PrintError("Unknown build variant: %s", testVariant)
			return fmt.Errorf("unknown variant: %s", testVariant)
		}
		buildCfg.Command = variant.Command
		buildCfg.Output = variant.Output
	}

	ui.PrintBanner(version)

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	if devMode {
		ui.PrintInfo("Mode: Development (localhost)")
		ui.Println()
	}

	client := api.NewClientWithDevMode(creds.APIKey, devMode)
	var buildVersionID string

	// Step 1: Build (if not skipped)
	if !testSkipBuild && buildCfg.Command != "" {
		ui.PrintBox("Building", buildCfg.Command)

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err = runner.Run(buildCfg.Command, func(line string) {
			ui.PrintDim("  %s", line)
		})

		buildDuration := time.Since(startTime)

		if err != nil {
			ui.Println()
			ui.PrintError("Build failed: %v", err)
			return err
		}

		ui.PrintSuccess("Build completed in %s", buildDuration.Round(time.Second))
		ui.Println()

		// Step 2: Upload
		artifactPath := filepath.Join(cwd, buildCfg.Output)
		if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
			ui.PrintError("Build artifact not found: %s", buildCfg.Output)
			return fmt.Errorf("artifact not found")
		}

		buildVersion := build.GenerateVersionString()
		metadata := build.CollectMetadata(cwd, buildCfg.Command, testVariant, buildDuration)

		ui.PrintBox("Uploading", filepath.Base(buildCfg.Output))

		result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
			BuildVarID: variant.BuildVarID,
			Version:    buildVersion,
			FilePath:   artifactPath,
			Metadata:   metadata,
		})

		if err != nil {
			ui.PrintError("Upload failed: %v", err)
			return err
		}

		buildVersionID = result.VersionID
		ui.PrintSuccess("Uploaded: %s", result.Version)
		ui.Println()
	} else {
		ui.PrintInfo("Skipping build step")
		ui.Println()
	}

	// Step 3: Run test
	ui.PrintBox("Running", testNameOrID)

	response, err := client.ExecuteTest(cmd.Context(), &api.ExecuteTestRequest{
		TestID:         testID,
		Retries:        testRetries,
		BuildVersionID: buildVersionID,
	})

	if err != nil {
		ui.PrintError("Failed to start test: %v", err)
		return err
	}

	taskID := response.TaskID
	reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)

	// Show report link immediately so user can follow along
	ui.PrintLink("Report", reportURL)
	ui.Println()

	if testNoWait {
		ui.PrintSuccess("Test queued: %s", taskID)
		if testOpen {
			ui.OpenBrowser(reportURL)
		}
		return nil
	}

	// Monitor execution with progress bar
	monitor := sse.NewMonitorWithDevMode(creds.APIKey, testTimeout, devMode)
	finalStatus, err := monitor.MonitorTest(cmd.Context(), taskID, testID, func(status *sse.TestStatus) {
		ui.UpdateProgress(status.Progress, status.CurrentStep)
	})

	if err != nil {
		ui.PrintError("Monitoring failed: %v", err)
		return err
	}

	ui.Println()

	// Show final result using the shared status package for consistent success determination
	testPassed := status.IsSuccess(finalStatus.Status, finalStatus.Success, finalStatus.ErrorMessage)

	if testPassed {
		ui.PrintResultBox("Passed", reportURL, finalStatus.Duration)
	} else {
		ui.PrintResultBox("Failed", reportURL, finalStatus.Duration)
		ui.PrintError(finalStatus.ErrorMessage)
	}

	if testOpen {
		ui.OpenBrowser(reportURL)
	}

	if !testPassed {
		return fmt.Errorf("test failed")
	}

	return nil
}
