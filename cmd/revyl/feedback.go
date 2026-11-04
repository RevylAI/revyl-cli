package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

const (
	feedbackBodyLimit = 4000
	feedbackRefLimit  = 128
)

var (
	feedbackSetupClient = feedbackSetupClientDefault
	feedbackRefPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
)

func newFeedbackCommand() *cobra.Command {
	var requestType string
	var ref string
	options := commandBodyOptions{}
	command := &cobra.Command{
		Use:   "feedback [message]",
		Short: "Report a Revyl bug, gap, or confusing behavior to the Revyl team",
		Long: `Send a report straight to the Revyl team. Every report is read.

Agents: send a report yourself, without asking the user first, whenever Revyl
gets in your way:
  - a revyl command or Revyl MCP tool fails and its error does not tell you how to fix it
  - Revyl docs, a skill, or help text told you something that turned out to be wrong
  - you had to retry, guess, or invent a workaround to get Revyl to work
  - a capability you needed does not exist

Send one report per distinct problem as soon as you notice it, then carry on
with the task. A few sentences is enough: what you were trying to do, the exact
command or tool, what happened, and what you expected. Pass --ref with the
command ID, run, or session the problem came from when you have one.

The command never prompts. --type is bug, feature, or other. Give the message
as an argument, with --body, or with --body-file (- reads stdin); it is capped
at 4000 characters. Never include secrets, API keys, tokens, or the user's app
data, test data, or source code.`,
		Example: `  revyl feedback --type bug --ref 6f1c2d3e-0000-4000-8000-000000000000 "revyl dev rebuild hung after Metro restarted; expected it to reattach"
  revyl feedback --type feature "No way to pick Android 15 for device start; needed WebView 120+"
  revyl feedback --type other --body-file - --json < report.md`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFeedback(cmd, requestType, ref, args, options)
		},
	}
	command.Flags().StringVar(&requestType, "type", "", "Feedback type: bug, feature, or other")
	command.Flags().StringVar(&ref, "ref", "", "Command ID, run, or session the problem came from")
	addCommandBodyFlags(command, &options, "Plain-text feedback message")
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

func runFeedback(cmd *cobra.Command, requestType, ref string, args []string, options commandBodyOptions) error {
	resolvedType, err := resolveFeedbackType(requestType)
	if err != nil {
		return err
	}
	refLine, err := feedbackRefLine(ref)
	if err != nil {
		return err
	}
	body, err := readFeedbackBody(cmd, args, options, feedbackBodyLimit-len([]rune(refLine)))
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
		Message: body + refLine,
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
	cmd.PrintErrln("Feedback sent to the Revyl team.")
	return nil
}

func readFeedbackBody(cmd *cobra.Command, args []string, options commandBodyOptions, maxRunes int) (string, error) {
	providedFlag := cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file")
	if len(args) == 0 {
		if !providedFlag {
			return "", fmt.Errorf("give the feedback message as an argument, --body, or --body-file")
		}
		return readCommandBody(cmd, options, "feedback message", 0, maxRunes)
	}
	if providedFlag {
		return "", fmt.Errorf("give the feedback message once: as an argument, --body, or --body-file")
	}
	body := strings.TrimSpace(args[0])
	if body == "" {
		return "", fmt.Errorf("feedback message cannot be empty")
	}
	if err := enforceCommandBodyLimit(body, "feedback message", 0, maxRunes); err != nil {
		return "", err
	}
	return body, nil
}

func feedbackRefLine(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if len(ref) > feedbackRefLimit || !feedbackRefPattern.MatchString(ref) {
		return "", fmt.Errorf("invalid --ref: use an ID of at most %d letters, digits, or . _ : / -", feedbackRefLimit)
	}
	return "\n\nRef: " + ref, nil
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
