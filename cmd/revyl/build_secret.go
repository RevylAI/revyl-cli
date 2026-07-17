package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

const maxBuildSecretBytes = 512 * 1024

var (
	buildSecretSetFromStdin bool
	buildSecretDeleteForce  bool
)

var buildSecretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage encrypted org build secrets",
	Long: `Manage encrypted secrets referenced by local and remote build configurations.

Secret values are stored in Revyl's encrypted organization store. Project YAML
contains names only, under build.platforms.<platform>.secrets.`,
}

var buildSecretSetCmd = &cobra.Command{
	Use:   "set <NAME>",
	Short: "Create or update an encrypted build secret",
	Long: `Create or update an encrypted organization build secret.

The value is read from a masked terminal prompt by default. Use --stdin for
non-interactive environments so the value does not appear in shell history.`,
	Args: cobra.ExactArgs(1),
	RunE: runBuildSecretSet,
}

var buildSecretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List encrypted build secret names",
	Args:  cobra.NoArgs,
	RunE:  runBuildSecretList,
}

var buildSecretDeleteCmd = &cobra.Command{
	Use:   "delete <NAME>",
	Short: "Delete an encrypted build secret",
	Args:  cobra.ExactArgs(1),
	RunE:  runBuildSecretDelete,
}

type buildSecretMetadata struct {
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	AttachedTestCount int    `json:"attached_test_count,omitempty"`
}

type buildSecretListOutput struct {
	Secrets []buildSecretMetadata `json:"secrets"`
}

func init() {
	buildCmd.AddCommand(buildSecretCmd)
	buildSecretCmd.AddCommand(buildSecretSetCmd)
	buildSecretCmd.AddCommand(buildSecretListCmd)
	buildSecretCmd.AddCommand(buildSecretDeleteCmd)

	buildSecretSetCmd.Flags().BoolVar(
		&buildSecretSetFromStdin,
		"stdin",
		false,
		"Read the secret value from stdin",
	)
	buildSecretDeleteCmd.Flags().BoolVarP(
		&buildSecretDeleteForce,
		"force",
		"f",
		false,
		"Skip confirmation prompt",
	)
}

var buildSecretSetupClient = globalVarSetupClientDefault

// runBuildSecretSet creates or updates one encrypted org build secret.
func runBuildSecretSet(cmd *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	if !isValidRemoteBuildEnvKey(name) {
		return fmt.Errorf(
			"invalid build secret name %q: name must match [A-Za-z_][A-Za-z0-9_]*",
			args[0],
		)
	}

	value, err := readBuildSecretValue(name, buildSecretSetFromStdin)
	if err != nil {
		return err
	}

	client, err := buildSecretSetupClient(cmd)
	if err != nil {
		return err
	}
	existing, exists, err := findBuildSecret(cmd, client, name)
	if err != nil {
		return err
	}

	var stored api.OrgLaunchVariable
	if exists {
		response, updateErr := client.UpdateOrgLaunchVariable(
			cmd.Context(),
			existing.ID,
			nil,
			&value,
			nil,
		)
		if updateErr != nil {
			return fmt.Errorf("failed to update build secret %q: %w", name, updateErr)
		}
		stored = response.Result
	} else {
		response, createErr := client.AddOrgLaunchVariable(
			cmd.Context(),
			name,
			value,
			nil,
		)
		if createErr != nil {
			return fmt.Errorf("failed to create build secret %q: %w", name, createErr)
		}
		stored = response.Result
	}

	if buildSecretJSONEnabled(cmd) {
		return encodeJSON(buildSecretMetadataFromVariable(stored))
	}
	ui.PrintSuccess("Stored encrypted build secret '%s'", stored.Key)
	return nil
}

// runBuildSecretList lists org build secret metadata without printing values.
func runBuildSecretList(cmd *cobra.Command, args []string) error {
	client, err := buildSecretSetupClient(cmd)
	if err != nil {
		return err
	}
	response, err := client.ListOrgLaunchVariables(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list build secrets: %w", err)
	}

	secrets := make([]buildSecretMetadata, 0, len(response.Result))
	for _, variable := range response.Result {
		secrets = append(secrets, buildSecretMetadataFromVariable(variable))
	}
	sort.Slice(secrets, func(i, j int) bool {
		return secrets[i].Name < secrets[j].Name
	})

	if buildSecretJSONEnabled(cmd) {
		return encodeJSON(buildSecretListOutput{Secrets: secrets})
	}
	if len(secrets) == 0 {
		ui.PrintInfo("No encrypted build secrets found")
		return nil
	}

	table := ui.NewTable("NAME", "DESCRIPTION", "ATTACHED TESTS", "UPDATED")
	for _, secret := range secrets {
		description := secret.Description
		if description == "" {
			description = "-"
		}
		updatedAt := secret.UpdatedAt
		if updatedAt == "" {
			updatedAt = "-"
		}
		table.AddRow(
			secret.Name,
			description,
			fmt.Sprintf("%d", secret.AttachedTestCount),
			updatedAt,
		)
	}
	table.Render()
	return nil
}

// runBuildSecretDelete deletes one encrypted org build secret by name.
func runBuildSecretDelete(cmd *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	if !isValidRemoteBuildEnvKey(name) {
		return fmt.Errorf(
			"invalid build secret name %q: name must match [A-Za-z_][A-Za-z0-9_]*",
			args[0],
		)
	}

	client, err := buildSecretSetupClient(cmd)
	if err != nil {
		return err
	}
	variable, exists, err := findBuildSecret(cmd, client, name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("build secret %q not found", name)
	}

	if !buildSecretDeleteForce {
		if variable.AttachedTestCount > 0 {
			ui.PrintWarning(
				"Deleting this shared secret also detaches it from %d test(s)",
				variable.AttachedTestCount,
			)
		}
		confirmed, promptErr := ui.PromptConfirm(
			fmt.Sprintf("Delete encrypted build secret %q?", name),
			false,
		)
		if promptErr != nil {
			return promptErr
		}
		if !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	response, err := client.DeleteOrgLaunchVariable(cmd.Context(), variable.ID)
	if err != nil {
		return fmt.Errorf("failed to delete build secret %q: %w", name, err)
	}
	if buildSecretJSONEnabled(cmd) {
		return encodeJSON(buildSecretMetadataFromVariable(response.Result))
	}
	ui.PrintSuccess("Deleted encrypted build secret '%s'", name)
	if response.DetachedTestCount > 0 {
		ui.PrintDim("Detached from %d test(s)", response.DetachedTestCount)
	}
	return nil
}

// readBuildSecretValue reads a non-empty secret from a prompt or stdin.
func readBuildSecretValue(name string, fromStdin bool) (string, error) {
	var value string
	var err error
	if fromStdin {
		value, err = readBuildSecretFromReader(os.Stdin)
	} else {
		value, err = ui.PromptSecret(fmt.Sprintf("Value for %s:", name))
		if err != nil {
			return "", fmt.Errorf("%w; use --stdin in non-interactive environments", err)
		}
	}
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("build secret value cannot be empty")
	}
	if len([]byte(value)) > maxBuildSecretBytes {
		return "", fmt.Errorf("build secret value must be %d KB or smaller", maxBuildSecretBytes/1024)
	}
	return value, nil
}

// readBuildSecretFromReader reads a bounded secret and removes one line ending.
func readBuildSecretFromReader(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxBuildSecretBytes+1))
	if err != nil {
		return "", fmt.Errorf("failed to read build secret from stdin: %w", err)
	}
	if len(data) > maxBuildSecretBytes {
		return "", fmt.Errorf("build secret value must be %d KB or smaller", maxBuildSecretBytes/1024)
	}
	value := strings.TrimSuffix(string(data), "\n")
	value = strings.TrimSuffix(value, "\r")
	return value, nil
}

// findBuildSecret finds one org secret by its exact environment-variable name.
func findBuildSecret(
	cmd *cobra.Command,
	client *api.Client,
	name string,
) (api.OrgLaunchVariable, bool, error) {
	response, err := client.ListOrgLaunchVariables(cmd.Context())
	if err != nil {
		return api.OrgLaunchVariable{}, false, fmt.Errorf(
			"failed to resolve build secret %q: %w",
			name,
			err,
		)
	}
	for _, variable := range response.Result {
		if variable.Key == name {
			return variable, true, nil
		}
	}
	return api.OrgLaunchVariable{}, false, nil
}

// buildSecretMetadataFromVariable removes the plaintext value from CLI output.
func buildSecretMetadataFromVariable(variable api.OrgLaunchVariable) buildSecretMetadata {
	return buildSecretMetadata{
		Name:              variable.Key,
		Description:       variable.Description,
		UpdatedAt:         variable.UpdatedAt,
		AttachedTestCount: variable.AttachedTestCount,
	}
}

// buildSecretJSONEnabled reports whether root-level JSON output is enabled.
func buildSecretJSONEnabled(cmd *cobra.Command) bool {
	enabled, _ := cmd.Root().PersistentFlags().GetBool("json")
	return enabled
}
