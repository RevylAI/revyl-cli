// Package main provides the test command for the full build->upload->run workflow.
package main

import (
	"encoding/json"
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

// TestResult represents the JSON output for the test command.
type TestResult struct {
	Success      bool   `json:"success"`
	TaskID       string `json:"task_id"`
	TestID       string `json:"test_id"`
	TestName     string `json:"test_name"`
	Status       string `json:"status"`
	ReportURL    string `json:"report_url"`
	Duration     string `json:"duration,omitempty"`
	Error        string `json:"error,omitempty"`
	BuildVersion string `json:"build_version,omitempty"`
}

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
  revyl test login-flow --skip-build    # Skip build, use existing artifact
  revyl test login-flow --json          # Output results as JSON`,
	Args: cobra.ExactArgs(1),
	RunE: runFullTest,
}

var (
	testVariant    string
	testSkipBuild  bool
	testRetries    int
	testNoWait     bool
	testOpen       bool
	testTimeout    int
	testOutputJSON bool
)

func init() {
	testCmd.Flags().StringVar(&testVariant, "variant", "", "Build variant to use")
	testCmd.Flags().BoolVar(&testSkipBuild, "skip-build", false, "Skip build step")
	testCmd.Flags().IntVarP(&testRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	testCmd.Flags().BoolVar(&testNoWait, "no-wait", false, "Exit after test starts")
	testCmd.Flags().BoolVar(&testOpen, "open", true, "Open report in browser when complete")
	testCmd.Flags().IntVarP(&testTimeout, "timeout", "t", 3600, "Timeout in seconds")
	testCmd.Flags().BoolVar(&testOutputJSON, "json", false, "Output results as JSON")
}

// runFullTest executes the full build->upload->run workflow.
func runFullTest(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	// Check if --json flag is set (either local or global)
	jsonOutput := testOutputJSON
	if globalJSON, _ := cmd.Flags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		if jsonOutput {
			outputTestResult(TestResult{Success: false, Error: "Not authenticated. Run 'revyl auth login' first."})
			return fmt.Errorf("not authenticated")
		}
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
		if jsonOutput {
			outputTestResult(TestResult{Success: false, Error: "Project not initialized. Run 'revyl init' first."})
			return err
		}
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	// Resolve test ID from alias
	testID := testNameOrID
	_, isAlias := cfg.Tests[testNameOrID]
	if isAlias {
		testID = cfg.Tests[testNameOrID]
	}

	// Validate test exists before building (fail fast)
	if !isAlias {
		if !isValidUUID(testNameOrID) {
			// Not an alias and not a UUID - likely a typo or wrong command
			availableTests := getTestNames(cfg.Tests)
			errMsg := fmt.Sprintf("test '%s' not found in config", testNameOrID)
			if len(availableTests) > 0 {
				errMsg += fmt.Sprintf(". Available tests: %v", availableTests)
			}
			errMsg += "\n\nHint: Did you mean 'revyl tests list' to list all tests?"

			if jsonOutput {
				outputTestResult(TestResult{Success: false, Error: errMsg})
				return fmt.Errorf("test not found")
			}
			ui.PrintError(errMsg)
			return fmt.Errorf("test not found")
		}
		// It's a UUID format - verify it exists via API before building (unless skipping build)
		if !testSkipBuild {
			// Create client early to validate test exists
			devMode, _ := cmd.Flags().GetBool("dev")
			validationClient := api.NewClientWithDevMode(creds.APIKey, devMode)
			_, err := validationClient.GetTest(cmd.Context(), testID)
			if err != nil {
				errMsg := fmt.Sprintf("test '%s' not found: %v", testNameOrID, err)
				if jsonOutput {
					outputTestResult(TestResult{Success: false, Error: errMsg})
					return fmt.Errorf("test not found")
				}
				ui.PrintError(errMsg)
				return fmt.Errorf("test not found")
			}
		}
	}

	// Determine build config
	buildCfg := cfg.Build
	var variant config.BuildVariant
	if testVariant != "" {
		var ok bool
		variant, ok = cfg.Build.Variants[testVariant]
		if !ok {
			if jsonOutput {
				outputTestResult(TestResult{Success: false, Error: fmt.Sprintf("Unknown build variant: %s", testVariant)})
				return fmt.Errorf("unknown variant: %s", testVariant)
			}
			ui.PrintError("Unknown build variant: %s", testVariant)
			return fmt.Errorf("unknown variant: %s", testVariant)
		}
		buildCfg.Command = variant.Command
		buildCfg.Output = variant.Output
	}

	if !jsonOutput {
		ui.PrintBanner(version)
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	if devMode && !jsonOutput {
		ui.PrintInfo("Mode: Development (localhost)")
		ui.Println()
	}

	client := api.NewClientWithDevMode(creds.APIKey, devMode)
	var buildVersionID string
	var buildVersionStr string

	// Step 1: Build (if not skipped)
	if !testSkipBuild && buildCfg.Command != "" {
		if !jsonOutput {
			ui.PrintBox("Building", buildCfg.Command)
		}

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err = runner.Run(buildCfg.Command, func(line string) {
			if !jsonOutput {
				ui.PrintDim("  %s", line)
			}
		})

		buildDuration := time.Since(startTime)

		if err != nil {
			if jsonOutput {
				outputTestResult(TestResult{Success: false, Error: fmt.Sprintf("Build failed: %v", err)})
				return err
			}
			ui.Println()
			ui.PrintError("Build failed: %v", err)
			return err
		}

		if !jsonOutput {
			ui.PrintSuccess("Build completed in %s", buildDuration.Round(time.Second))
			ui.Println()
		}

		// Step 2: Upload
		artifactPath := filepath.Join(cwd, buildCfg.Output)
		if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
			if jsonOutput {
				outputTestResult(TestResult{Success: false, Error: fmt.Sprintf("Build artifact not found: %s", buildCfg.Output)})
				return fmt.Errorf("artifact not found")
			}
			ui.PrintError("Build artifact not found: %s", buildCfg.Output)
			return fmt.Errorf("artifact not found")
		}

		buildVersionStr = build.GenerateVersionString()
		metadata := build.CollectMetadata(cwd, buildCfg.Command, testVariant, buildDuration)

		if !jsonOutput {
			ui.PrintBox("Uploading", filepath.Base(buildCfg.Output))
		}

		result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
			BuildVarID: variant.BuildVarID,
			Version:    buildVersionStr,
			FilePath:   artifactPath,
			Metadata:   metadata,
		})

		if err != nil {
			if jsonOutput {
				outputTestResult(TestResult{Success: false, Error: fmt.Sprintf("Upload failed: %v", err)})
				return err
			}
			ui.PrintError("Upload failed: %v", err)
			return err
		}

		buildVersionID = result.VersionID
		if !jsonOutput {
			ui.PrintSuccess("Uploaded: %s", result.Version)
			ui.Println()
		}
	} else {
		if !jsonOutput {
			ui.PrintInfo("Skipping build step")
			ui.Println()
		}
	}

	// Step 3: Run test
	if !jsonOutput {
		ui.PrintBox("Running", testNameOrID)
	}

	response, err := client.ExecuteTest(cmd.Context(), &api.ExecuteTestRequest{
		TestID:         testID,
		Retries:        testRetries,
		BuildVersionID: buildVersionID,
	})

	if err != nil {
		if jsonOutput {
			outputTestResult(TestResult{Success: false, Error: fmt.Sprintf("Failed to start test: %v", err)})
			return err
		}
		ui.PrintError("Failed to start test: %v", err)
		return err
	}

	taskID := response.TaskID
	reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)

	// Show report link immediately so user can follow along
	if !jsonOutput {
		ui.PrintLink("Report", reportURL)
		ui.Println()
	}

	if testNoWait {
		if jsonOutput {
			outputTestResult(TestResult{
				Success:      true,
				TaskID:       taskID,
				TestID:       testID,
				TestName:     testNameOrID,
				Status:       "queued",
				ReportURL:    reportURL,
				BuildVersion: buildVersionStr,
			})
		} else {
			ui.PrintSuccess("Test queued: %s", taskID)
			if testOpen {
				ui.OpenBrowser(reportURL)
			}
		}
		return nil
	}

	// Monitor execution with progress bar
	monitor := sse.NewMonitorWithDevMode(creds.APIKey, testTimeout, devMode)
	finalStatus, err := monitor.MonitorTest(cmd.Context(), taskID, testID, func(s *sse.TestStatus) {
		if !jsonOutput {
			ui.UpdateProgress(s.Progress, s.CurrentStep)
		}
	})

	if err != nil {
		if jsonOutput {
			outputTestResult(TestResult{Success: false, TaskID: taskID, TestID: testID, ReportURL: reportURL, Error: fmt.Sprintf("Monitoring failed: %v", err)})
			return err
		}
		ui.PrintError("Monitoring failed: %v", err)
		return err
	}

	if !jsonOutput {
		ui.Println()
	}

	// Show final result using the shared status package for consistent success determination
	testPassed := status.IsSuccess(finalStatus.Status, finalStatus.Success, finalStatus.ErrorMessage)

	if jsonOutput {
		result := TestResult{
			Success:      testPassed,
			TaskID:       taskID,
			TestID:       testID,
			TestName:     testNameOrID,
			Status:       finalStatus.Status,
			ReportURL:    reportURL,
			Duration:     finalStatus.Duration,
			BuildVersion: buildVersionStr,
		}
		if !testPassed {
			result.Error = finalStatus.ErrorMessage
		}
		outputTestResult(result)
	} else {
		if testPassed {
			ui.PrintResultBox("Passed", reportURL, finalStatus.Duration)
		} else {
			ui.PrintResultBox("Failed", reportURL, finalStatus.Duration)
			ui.PrintError(finalStatus.ErrorMessage)
		}

		if testOpen {
			ui.OpenBrowser(reportURL)
		}
	}

	if !testPassed {
		return fmt.Errorf("test failed")
	}

	return nil
}

// outputTestResult outputs the test result as JSON.
func outputTestResult(result TestResult) {
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}
