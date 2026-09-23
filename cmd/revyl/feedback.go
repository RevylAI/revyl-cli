package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

const (
	feedbackBodyLimit = 4000
)

var (
	feedbackSetupClient = feedbackSetupClientDefault
)

func newFeedbackCommand() *cobra.Command {
	var requestType string
	options := commandBodyOptions{}
	command := &cobra.Command{
		Use:   "feedback",
		Short: "Send a bug report, feature request, or other feedback to the Revyl team",
		Long: `Send feedback about Revyl to the Revyl team. Use it when the CLI, a device
session, or a build fails, or when something you need is missing.

--type is one of bug, feature, or other. The body is required and capped at
4000 characters. Do not include secrets, API keys, or customer content.`,
		Example: `  revyl feedback --type bug --body "revyl dev rebuild hangs after a Metro restart" --json
  revyl feedback --type feature --body-file - --json < request.md
  revyl feedback --type other --body "..." --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFeedback(cmd, requestType, options)
		},
	}
	command.Flags().StringVar(&requestType, "type", "", "Feedback type: bug, feature, or other")
	addCommandBodyFlags(command, &options, "Plain-text feedback body")
	_ = command.MarkFlagRequired("type")
	return command
}

func feedbackSetupClientDefault(cmd *cobra.Command) (*api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	return api.NewClientWithDevMode(apiKey, devMode), nil
}

func runFeedback(cmd *cobra.Command, requestType string, options commandBodyOptions) error {
	resolvedType, err := resolveFeedbackType(requestType)
	if err != nil {
		return err
	}
	body, err := readCommandBody(cmd, options, "feedback body", 0, feedbackBodyLimit)
	if err != nil {
		return err
	}
	client, err := feedbackSetupClient(cmd)
	if err != nil {
		return err
	}

	source := api.SupportContextSourceCli
	osName := runtime.GOOS
	arch := runtime.GOARCH
	cliVersion := version
	req := &api.SupportRequest{
		Type:    resolvedType,
		Message: body,
		Context: &api.SupportContext{
			Source:     &source,
			CliVersion: &cliVersion,
			Os:         &osName,
			Arch:       &arch,
		},
	}
	result, err := client.CreateSupportRequest(cmd.Context(), req)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	cmd.PrintErrln("Feedback submitted.")
	return nil
}

func resolveFeedbackType(value string) (api.SupportRequestType, error) {
	resolved := api.SupportRequestType(strings.TrimSpace(value))
	switch resolved {
	case api.SupportRequestTypeBug, api.SupportRequestTypeFeature, api.SupportRequestTypeOther:
		return resolved, nil
	default:
		return "", fmt.Errorf("invalid --type %q: use bug, feature, or other", value)
	}
}
