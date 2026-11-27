package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

var computerInferenceUnavailable = map[string]string{
	"computer_inference_off": "this computer wasn't created with --inference, so it has no model to prompt; create one with revyl computer create --inference",
	"host_unsupported":       "this computer's host doesn't serve an inference endpoint yet",
	"model_unavailable":      "this computer's model isn't answering right now; retry shortly",
}

func newComputerInferenceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "inference [C1]",
		Short: "Show the OpenAI-compatible endpoint for a computer's own model",
		Long: `Show the OpenAI-compatible endpoint of a computer created with --inference.
Its base URL is reachable only from inside that computer, and its API key works
only for that computer until it stops. Inside the computer, C1 defaults to
REVYL_COMPUTER_ID.

--env prints OPENAI_BASE_URL, OPENAI_API_KEY and OPENAI_MODEL export lines for
eval. Exits 1 when the computer has no endpoint to offer.`,
		Example: `  revyl computer inference "$C1"
  eval "$(revyl computer inference --env)"   inside the computer`,
	}
	command.Flags().Bool("env", false, "Print export lines for OPENAI_BASE_URL, OPENAI_API_KEY and OPENAI_MODEL, for eval inside the computer")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) > 1 {
				return nil, errors.New("computer inference takes at most one computer UUID")
			}
			value := strings.TrimSpace(os.Getenv("REVYL_COMPUTER_ID"))
			if len(args) == 1 {
				value = args[0]
			}
			if value == "" {
				return nil, errors.New("computer inference needs a computer UUID, or REVYL_COMPUTER_ID inside a computer")
			}
			id, err := parseComputerUUID(value)
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID = id.String()
			env, _ := cmd.Flags().GetBool("env")
			if jsonOutput, _ := cmd.Flags().GetBool("json"); env && jsonOutput {
				return nil, errors.New("--env and --json can't be combined")
			}
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			endpoint, err := client.OpenComputerInferenceEndpoint(cmd.Context(), id)
			var refusal *api.APIError
			if errors.As(err, &refusal) {
				switch {
				case refusal.StatusCode == 404 && refusal.Code == "":
					refusal.Message = "this Revyl server doesn't serve computer inference endpoints yet"
				case refusal.Code == "screen_input_unavailable":
					refusal.Message = "this computer isn't running, so it has no inference endpoint"
				}
			}
			return endpoint, err
		})
	}
	return command
}

func writeComputerInference(writer io.Writer, endpoint *api.ComputerInferenceEndpoint, env bool) error {
	if env {
		quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }
		_, err := fmt.Fprintf(writer, "export OPENAI_BASE_URL=%s\nexport OPENAI_API_KEY=%s\nexport OPENAI_MODEL=%s\n", quote(*endpoint.BaseUrl), quote(*endpoint.ApiKey), quote(*endpoint.Model))
		return err
	}
	lines := []string{
		"Base URL: " + computerPrintable(*endpoint.BaseUrl) + " (reachable only from inside this computer)",
		"API key: " + computerPrintable(*endpoint.ApiKey),
		"Model: " + computerPrintable(*endpoint.Model),
	}
	if endpoint.MaxOutputTokens != nil {
		lines = append(lines, fmt.Sprintf("Max output tokens: %d", *endpoint.MaxOutputTokens))
	}
	usage := api.ComputerInferenceUsage{}
	if endpoint.Usage != nil {
		usage = *endpoint.Usage
	}
	lines = append(lines, fmt.Sprintf("Usage: %d requests, %d input tokens, %d output tokens", intValue(usage.Requests), intValue(usage.PromptTokens), intValue(usage.CompletionTokens)))
	_, err := fmt.Fprintln(writer, strings.Join(lines, "\n"))
	return err
}
