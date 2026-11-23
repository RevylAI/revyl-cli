package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

func addComputerStepCommands(parent *cobra.Command) {
	for _, step := range []struct{ name, short, example string }{
		{"instruction", "Click the one thing a plain-language instruction names", `revyl computer instruction C1 "Open the File menu"`},
		{"validation", "Check a plain-language statement against the computer's screen", `revyl computer validation C1 "The cart shows 2 items"`},
		{"extract", "Read a value off the computer's screen", `revyl computer extract C1 "the order number"`},
	} {
		command := &cobra.Command{
			Use:     step.name + " C1 DESCRIPTION",
			Short:   step.short,
			Example: "  " + step.example,
			Args:    cobra.ExactArgs(2),
		}
		command.Flags().String("engine", "auto", "Who answers: local (the computer's own model), hosted (Revyl's hosted grounding), or auto (local first, hosted as the fallback)")
		command.RunE = func(cmd *cobra.Command, args []string) error {
			return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
				id, err := parseComputerUUID(args[0])
				if err != nil {
					return nil, err
				}
				invocation.ComputerSessionID = id.String()
				engine, _ := cmd.Flags().GetString("engine")
				request := api.ComputerStepRequest{StepType: step.name, Description: args[1], Engine: engine, Via: "cli"}
				if err := api.ValidateComputerStep(request); err != nil {
					return nil, err
				}
				client, err := computerClient(cmd)
				if err != nil {
					return nil, err
				}
				result, err := client.RunComputerStep(cmd.Context(), id, request)
				var refusal *api.APIError
				if errors.As(err, &refusal) && refusal.StatusCode == 404 && refusal.Code == "" {
					refusal.Message = "this Revyl server does not run computer steps yet"
				}
				return result, err
			})
		}
		parent.AddCommand(command)
	}
}

func writeComputerStep(cmd *cobra.Command, result *api.ComputerStepResult, jsonOutput bool) error {
	if jsonOutput {
		return writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON)
	}
	summary := fmt.Sprintf("%s %s engine=%s attempts=%d seconds=%.2f", result.StepType, result.Status, result.Engine, result.Attempts, result.Seconds)
	if result.LocalEngine != nil {
		summary += " local_engine=" + computerPrintable(*result.LocalEngine)
	}
	if result.Point != nil {
		summary += fmt.Sprintf(" point=(%d,%d)", result.Point.X, result.Point.Y)
	}
	fmt.Fprintln(cmd.OutOrStdout(), summary)
	if result.ExtractedData != nil {
		fmt.Fprintln(cmd.OutOrStdout(), computerPrintable(result.ExtractedData.Information))
	}
	if result.StatusReason != nil && *result.StatusReason != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "reason: "+computerPrintable(*result.StatusReason))
	}
	if result.Status == "uncertain" {
		fmt.Fprintln(cmd.ErrOrStderr(), "Revyl could not confirm this step, so it is not a pass and nothing was clicked; take a screenshot before acting again.")
	}
	return nil
}
