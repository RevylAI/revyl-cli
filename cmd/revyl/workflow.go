// Package main provides the workflow command for the full build->upload->run workflow.
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
	"github.com/revyl/cli/internal/ui"
)

// WorkflowResult represents the JSON output for the workflow command.
type WorkflowResult struct {
	Success      bool   `json:"success"`
	TaskID       string `json:"task_id"`
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	Status       string `json:"status"`
	ReportURL    string `json:"report_url"`
	TotalTests   int    `json:"total_tests,omitempty"`
	PassedTests  int    `json:"passed_tests,omitempty"`
	FailedTests  int    `json:"failed_tests,omitempty"`
	Duration     string `json:"duration,omitempty"`
	Error        string `json:"error,omitempty"`
	BuildVersion string `json:"build_version,omitempty"`
}

// workflowCmd runs the full workflow: build -> upload -> run workflow.
var workflowCmd = &cobra.Command{
	Use:   "workflow <name|id>",
	Short: "Build, upload, and run a workflow",
	Long: `Build the app, upload it, and run a workflow.

This is the main command for running multiple tests after a build.
It combines 'revyl build upload' and 'revyl run workflow' into one command.

Examples:
  revyl workflow smoke-tests              # Full workflow with default build
  revyl workflow smoke-tests --variant release
  revyl workflow smoke-tests --skip-build # Skip build, use existing artifact
  revyl workflow smoke-tests --json       # Output results as JSON`,
	Args: cobra.ExactArgs(1),
	RunE: runFullWorkflow,
}

var (
	workflowVariant    string
	workflowSkipBuild  bool
	workflowRetries    int
	workflowNoWait     bool
	workflowOpen       bool
	workflowTimeout    int
	workflowOutputJSON bool
)

func init() {
	workflowCmd.Flags().StringVar(&workflowVariant, "variant", "", "Build variant to use")
	workflowCmd.Flags().BoolVar(&workflowSkipBuild, "skip-build", false, "Skip build step")
	workflowCmd.Flags().IntVarP(&workflowRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	workflowCmd.Flags().BoolVar(&workflowNoWait, "no-wait", false, "Exit after workflow starts")
	workflowCmd.Flags().BoolVar(&workflowOpen, "open", true, "Open report in browser when complete")
	workflowCmd.Flags().IntVarP(&workflowTimeout, "timeout", "t", 3600, "Timeout in seconds")
	workflowCmd.Flags().BoolVar(&workflowOutputJSON, "json", false, "Output results as JSON")
}

// runFullWorkflow executes the full build->upload->run workflow.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (workflow name or ID)
//
// Returns:
//   - error: Any error that occurred, or nil on success
func runFullWorkflow(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]

	// Check if --json flag is set (either local or global)
	jsonOutput := workflowOutputJSON
	if globalJSON, _ := cmd.Flags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		if jsonOutput {
			outputWorkflowResult(WorkflowResult{Success: false, Error: "Not authenticated. Run 'revyl auth login' first."})
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
			outputWorkflowResult(WorkflowResult{Success: false, Error: "Project not initialized. Run 'revyl init' first."})
			return err
		}
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	// Resolve workflow ID from alias
	workflowID := workflowNameOrID
	_, isAlias := cfg.Workflows[workflowNameOrID]
	if isAlias {
		workflowID = cfg.Workflows[workflowNameOrID]
	}

	// Get dev mode flag early for validation
	devMode, _ := cmd.Flags().GetBool("dev")

	// Validate workflow exists before building (fail fast)
	if !isAlias {
		if !isValidUUID(workflowNameOrID) {
			// Not an alias and not a UUID - likely a typo or wrong command
			availableWorkflows := getWorkflowNames(cfg.Workflows)
			errMsg := fmt.Sprintf("workflow '%s' not found in config", workflowNameOrID)
			if len(availableWorkflows) > 0 {
				errMsg += fmt.Sprintf(". Available workflows: %v", availableWorkflows)
			}
			errMsg += "\n\nHint: Run 'revyl tests remote' to see all available tests/workflows."

			if jsonOutput {
				outputWorkflowResult(WorkflowResult{Success: false, Error: errMsg})
				return fmt.Errorf("workflow not found")
			}
			ui.PrintError(errMsg)
			return fmt.Errorf("workflow not found")
		}
		// It's a UUID format - verify it exists via API before building (unless skipping build)
		if !workflowSkipBuild {
			validationClient := api.NewClientWithDevMode(creds.APIKey, devMode)
			_, err := validationClient.GetWorkflow(cmd.Context(), workflowID)
			if err != nil {
				errMsg := fmt.Sprintf("workflow '%s' not found: %v", workflowNameOrID, err)
				if jsonOutput {
					outputWorkflowResult(WorkflowResult{Success: false, Error: errMsg})
					return fmt.Errorf("workflow not found")
				}
				ui.PrintError(errMsg)
				return fmt.Errorf("workflow not found")
			}
		}
	}

	// Determine build config
	buildCfg := cfg.Build
	var variant config.BuildVariant
	if workflowVariant != "" {
		var ok bool
		variant, ok = cfg.Build.Variants[workflowVariant]
		if !ok {
			if jsonOutput {
				outputWorkflowResult(WorkflowResult{Success: false, Error: fmt.Sprintf("Unknown build variant: %s", workflowVariant)})
				return fmt.Errorf("unknown variant: %s", workflowVariant)
			}
			ui.PrintError("Unknown build variant: %s", workflowVariant)
			return fmt.Errorf("unknown variant: %s", workflowVariant)
		}
		buildCfg.Command = variant.Command
		buildCfg.Output = variant.Output
	}

	if !jsonOutput {
		ui.PrintBanner(version)
	}

	if devMode && !jsonOutput {
		ui.PrintInfo("Mode: Development (localhost)")
		ui.Println()
	}

	client := api.NewClientWithDevMode(creds.APIKey, devMode)
	var buildVersionID string
	var buildVersionStr string

	// Step 1: Build (if not skipped)
	if !workflowSkipBuild && buildCfg.Command != "" {
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
				outputWorkflowResult(WorkflowResult{Success: false, Error: fmt.Sprintf("Build failed: %v", err)})
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
				outputWorkflowResult(WorkflowResult{Success: false, Error: fmt.Sprintf("Build artifact not found: %s", buildCfg.Output)})
				return fmt.Errorf("artifact not found")
			}
			ui.PrintError("Build artifact not found: %s", buildCfg.Output)
			return fmt.Errorf("artifact not found")
		}

		buildVersionStr = build.GenerateVersionString()
		metadata := build.CollectMetadata(cwd, buildCfg.Command, workflowVariant, buildDuration)

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
				outputWorkflowResult(WorkflowResult{Success: false, Error: fmt.Sprintf("Upload failed: %v", err)})
				return err
			}
			ui.PrintError("Upload failed: %v", err)
			return err
		}

		buildVersionID = result.VersionID
		// Suppress unused variable warning - buildVersionID is used for future workflow execution with specific builds
		_ = buildVersionID
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

	// Step 3: Run workflow
	if !jsonOutput {
		ui.PrintBox("Running Workflow", workflowNameOrID)
	}

	response, err := client.ExecuteWorkflow(cmd.Context(), &api.ExecuteWorkflowRequest{
		WorkflowID: workflowID,
		Retries:    workflowRetries,
	})

	if err != nil {
		if jsonOutput {
			outputWorkflowResult(WorkflowResult{Success: false, Error: fmt.Sprintf("Failed to start workflow: %v", err)})
			return err
		}
		ui.PrintError("Failed to start workflow: %v", err)
		return err
	}

	taskID := response.TaskID
	reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(devMode), taskID)

	// Show report link immediately so user can follow along
	if !jsonOutput {
		ui.PrintLink("Report", reportURL)
		ui.Println()
	}

	if workflowNoWait {
		if jsonOutput {
			outputWorkflowResult(WorkflowResult{
				Success:      true,
				TaskID:       taskID,
				WorkflowID:   workflowID,
				WorkflowName: workflowNameOrID,
				Status:       "queued",
				ReportURL:    reportURL,
				BuildVersion: buildVersionStr,
			})
		} else {
			ui.PrintSuccess("Workflow queued: %s", taskID)
			if workflowOpen {
				ui.OpenBrowser(reportURL)
			}
		}
		return nil
	}

	// Monitor execution with progress
	monitor := sse.NewMonitorWithDevMode(creds.APIKey, workflowTimeout, devMode)
	finalStatus, err := monitor.MonitorWorkflow(cmd.Context(), taskID, workflowID, func(s *sse.WorkflowStatus) {
		if !jsonOutput {
			ui.PrintBasicWorkflowStatus(s.Status, s.CompletedTests, s.TotalTests)
		}
	})

	if err != nil {
		if jsonOutput {
			outputWorkflowResult(WorkflowResult{Success: false, TaskID: taskID, WorkflowID: workflowID, ReportURL: reportURL, Error: fmt.Sprintf("Monitoring failed: %v", err)})
			return err
		}
		ui.PrintError("Monitoring failed: %v", err)
		return err
	}

	if !jsonOutput {
		ui.Println()
	}

	// Determine success based on status and failed tests
	workflowPassed := finalStatus.Status == "completed" && finalStatus.FailedTests == 0

	if jsonOutput {
		result := WorkflowResult{
			Success:      workflowPassed,
			TaskID:       taskID,
			WorkflowID:   workflowID,
			WorkflowName: workflowNameOrID,
			Status:       finalStatus.Status,
			ReportURL:    reportURL,
			TotalTests:   finalStatus.TotalTests,
			PassedTests:  finalStatus.PassedTests,
			FailedTests:  finalStatus.FailedTests,
			Duration:     finalStatus.Duration,
			BuildVersion: buildVersionStr,
		}
		if !workflowPassed {
			if finalStatus.ErrorMessage != "" {
				result.Error = finalStatus.ErrorMessage
			} else if finalStatus.FailedTests > 0 {
				result.Error = fmt.Sprintf("%d tests failed", finalStatus.FailedTests)
			}
		}
		outputWorkflowResult(result)
	} else {
		if workflowPassed {
			ui.PrintSuccess("Workflow completed: %d/%d tests passed", finalStatus.PassedTests, finalStatus.TotalTests)
		} else {
			switch finalStatus.Status {
			case "cancelled":
				ui.PrintWarning("Workflow cancelled: %d passed, %d failed", finalStatus.PassedTests, finalStatus.FailedTests)
			case "timeout":
				ui.PrintWarning("Workflow timed out: %d passed, %d failed", finalStatus.PassedTests, finalStatus.FailedTests)
			default:
				ui.PrintError("Workflow finished: %d passed, %d failed", finalStatus.PassedTests, finalStatus.FailedTests)
			}
		}

		ui.PrintLink("Report", reportURL)

		if workflowOpen {
			ui.OpenBrowser(reportURL)
		}
	}

	if !workflowPassed {
		return fmt.Errorf("workflow failed")
	}

	return nil
}

// outputWorkflowResult outputs the workflow result as JSON.
//
// Parameters:
//   - result: The workflow result to output
func outputWorkflowResult(result WorkflowResult) {
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}
