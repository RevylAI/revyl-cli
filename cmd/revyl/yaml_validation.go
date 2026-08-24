package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var testValidateCmd = &cobra.Command{
	Use:          "validate <file> [file...]",
	Short:        "Validate YAML test files with backend semantics",
	SilenceUsage: true,
	Long: `Validate one or more YAML test files using the same backend validator
used by test create --from-file and test push.

This checks canonical YAML, legacy compatibility forms, script/module/file
references, and returns structured diagnostics with paths and line numbers
when available.`,
	Example: `  revyl test validate .revyl/tests/login.yaml
  revyl test validate tests/*.yaml --dev`,
	Args: cobra.MinimumNArgs(1),
	RunE: runTestValidate,
}

func runTestValidate(cmd *cobra.Command, files []string) error {
	if err := validateYAMLFilesWithBackend(cmd, files); err != nil {
		return err
	}
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if !jsonOutput {
		label := "files"
		if len(files) == 1 {
			label = "file"
		}
		ui.PrintSuccess("Validated %d YAML test %s", len(files), label)
	}
	return nil
}

func validateYAMLFilesWithBackend(cmd *cobra.Command, files []string) error {
	if len(files) == 0 {
		return nil
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	hasErrors := false
	invalidFiles := 0
	totalErrors := 0
	totalWarnings := 0
	results := make([]yamlValidationFileResult, 0, len(files))
	for _, file := range files {
		result, err := validateYAMLFileWithBackend(cmd.Context(), client, file)
		if err != nil {
			return err
		}
		totalErrors += result.Errors
		totalWarnings += result.Warnings
		if jsonOutput {
			results = append(results, yamlValidationFileResult{
				File:     file,
				IsValid:  result.IsValid,
				Errors:   result.Errors,
				Warnings: result.Warnings,
				Messages: result.Messages,
			})
		} else {
			printBackendYAMLDiagnostics(file, result)
		}
		if !result.IsValid {
			hasErrors = true
			invalidFiles++
		}
	}

	if jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"is_valid": !hasErrors,
			"files":    results,
		}); err != nil {
			return err
		}
	}

	if hasErrors {
		return analytics.CompletedWithExitCode(fmt.Errorf("YAML validation failed"), analytics.CommandCompletion{
			ExitCode:     1,
			Domain:       "test_validation",
			DomainStatus: "invalid",
			Properties: map[string]interface{}{
				"validation_file_count":    len(files),
				"validation_invalid_files": invalidFiles,
				"validation_error_count":   totalErrors,
				"validation_warning_count": totalWarnings,
			},
		})
	}
	return nil
}

type yamlValidationFileResult struct {
	File     string                      `json:"file"`
	IsValid  bool                        `json:"is_valid"`
	Errors   int                         `json:"errors"`
	Warnings int                         `json:"warnings"`
	Messages []api.YAMLValidationMessage `json:"messages"`
}

func validateYAMLFileWithBackend(ctx context.Context, client *api.Client, file string) (*api.ValidateYAMLResponse, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file %s: %w", file, err)
	}

	result, err := client.ValidateYAML(ctx, &api.ValidateYAMLRequest{
		YAMLContent:    string(content),
		ValidationType: "full_test",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to validate YAML file %s: %w", file, err)
	}
	return result, nil
}

func printBackendYAMLDiagnostics(file string, result *api.ValidateYAMLResponse) {
	if result == nil || len(result.Messages) == 0 {
		return
	}

	for _, message := range result.Messages {
		location := yamlDiagnosticLocation(file, message)
		text := strings.TrimSpace(message.Message)
		if message.Code != "" {
			text = fmt.Sprintf("%s: %s", message.Code, text)
		}
		if strings.EqualFold(message.Severity, "warning") {
			ui.PrintWarning("%s - %s", location, text)
		} else {
			ui.PrintError("%s - %s", location, text)
		}
		if strings.TrimSpace(message.Suggestion) != "" {
			ui.PrintInfo("  %s", strings.TrimSpace(message.Suggestion))
		}
	}
}

func yamlDiagnosticLocation(file string, message api.YAMLValidationMessage) string {
	line := message.Line
	if line == 0 {
		line = message.LineNumber
	}
	column := message.Column
	if column == 0 {
		column = message.ColumnNumber
	}

	location := file
	if line > 0 {
		location = fmt.Sprintf("%s:%d", location, line)
		if column > 0 {
			location = fmt.Sprintf("%s:%d", location, column)
		}
	}
	if strings.TrimSpace(message.FieldPath) != "" {
		location = fmt.Sprintf("%s %s", location, strings.TrimSpace(message.FieldPath))
	}
	return location
}
