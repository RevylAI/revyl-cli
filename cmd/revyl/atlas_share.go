package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

const atlasShareMaxExpirationHours = 36500 * 24

var atlasShareRefusedFlags = []string{
	"launch", "launch-stdin", "device", "device-model", "os", "runtime", "os-version",
	"test-id", "report-id", "workflow-execution-id", "source-kind",
}

func newAtlasShareCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "share",
		Short: "Create, inspect or revoke the public Atlas share link",
		Long: `Manage the public, read-only share link for an app's Atlas map. A share
covers branch, build and time scope only: it never carries device, launch or run
filters, so those flags are refused. The link is a credential: it is printed only
by share create, and status never prints it.`,
		Args: cobra.NoArgs,
	}
	command.AddCommand(newAtlasShareCreateCommand(), newAtlasShareStatusCommand(), newAtlasShareRevokeCommand())
	return command
}

func addAtlasShareScopeFlags(command *cobra.Command) {
	addAtlasBranchFlags(command)
	command.Flags().StringVar(&atlasApp, "app", "", "App name or app id")
	command.Flags().StringVar(&atlasBuild, "build", "", "Build id, build version, latest, or all; defaults to all builds in the branch scope")
	command.Flags().StringVar(&atlasFrom, "from", "", "Start time filter (ISO timestamp with zone)")
	command.Flags().StringVar(&atlasTo, "to", "", "End time filter (ISO timestamp with zone)")
	command.Flags().StringVar(&atlasSince, "since", "", "Relative start time, such as 7d")
	command.Flags().IntVar(&atlasRecentBuildLimit, "recent-build-limit", 0, "Restrict to the latest N builds (1-100); 0 disables")
	command.Flags().StringVar(&atlasRange, "range", "", "Map time range: recent (latest 20 builds), 1d, 7d, 30d, or all")
	command.Flags().BoolVar(&atlasJSON, "json", false, "Output stable JSON")
	for _, name := range atlasShareRefusedFlags {
		if name == "launch" {
			command.Flags().StringArray(name, nil, "Refused: share links do not carry launch filters")
		} else if name == "launch-stdin" {
			command.Flags().Bool(name, false, "Refused: share links do not carry launch filters")
		} else {
			command.Flags().String(name, "", "Refused: share links do not carry device or run filters")
		}
		_ = command.Flags().MarkHidden(name)
	}
	_ = command.MarkFlagRequired("app")
}

func refuseAtlasShareFilters(cmd *cobra.Command) error {
	refused := []string{}
	for _, name := range atlasShareRefusedFlags {
		if cmd.Flags().Changed(name) {
			refused = append(refused, "--"+name)
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("atlas share does not carry device, launch or run filters (%s); a share covers only branch, build and time scope, so drop those flags", strings.Join(refused, ", "))
}

func atlasShareTarget(cmd *cobra.Command) (*api.Client, *api.App, api.AtlasShareScope, error) {
	if err := refuseAtlasShareFilters(cmd); err != nil {
		return nil, nil, api.AtlasShareScope{}, err
	}
	client, err := atlasClient(cmd)
	if err != nil {
		return nil, nil, api.AtlasShareScope{}, err
	}
	query, app, err := atlasQueryFor(cmd, client)
	if err != nil {
		return nil, nil, api.AtlasShareScope{}, err
	}
	return client, app, api.AtlasShareScope{BuildID: query.BuildID, BranchID: query.BranchID, FromTime: query.FromTime, ToTime: query.ToTime, RecentBuildLimit: query.RecentBuildLimit}, nil
}

func atlasShareScopeContract(scope api.AtlasShareScope) map[string]interface{} {
	return map[string]interface{}{
		"build_id":           optionalString(scope.BuildID),
		"branch_id":          optionalString(scope.BranchID),
		"from_time":          optionalString(scope.FromTime),
		"to_time":            optionalString(scope.ToTime),
		"recent_build_limit": scope.RecentBuildLimit,
	}
}

func atlasShareStatusContract(action string, appID string, scope api.AtlasShareScope, status *api.AtlasShareLinkStatusResponse) map[string]interface{} {
	includeAnnotations := status.IncludeAnnotations != nil && *status.IncludeAnnotations
	return map[string]interface{}{
		"contract":            "atlas_share.v1",
		"action":              action,
		"app_id":              appID,
		"scope":               atlasShareScopeContract(scope),
		"enabled":             status.Enabled,
		"share_id":            status.ShareId,
		"expires_at":          status.ExpiresAt,
		"include_annotations": includeAnnotations,
	}
}

func newAtlasShareCreateCommand() *cobra.Command {
	var expirationHours int
	var includeAnnotations bool
	command := &cobra.Command{
		Use:   "create",
		Short: "Create or refresh the public share link for a map scope",
		Long: `Create the public, read-only share link for the selected branch, build and
time scope, or refresh the active link for that exact scope. The link is printed
once, to stdout. Treat it as a credential: anyone with it can view the map.`,
		Example: `  revyl atlas share create --app <app-id> --build latest
  revyl atlas share create --app <app-id> --range 7d --expiration-hours 72 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if expirationHours < 1 || expirationHours > atlasShareMaxExpirationHours {
				return fmt.Errorf("--expiration-hours must be between 1 and %d", atlasShareMaxExpirationHours)
			}
			client, app, scope, err := atlasShareTarget(cmd)
			if err != nil {
				return err
			}
			request := &api.AtlasShareLinkRequest{
				BuildId: optionalText(scope.BuildID), BranchId: optionalText(scope.BranchID),
				FromTime: optionalText(scope.FromTime), ToTime: optionalText(scope.ToTime),
			}
			if scope.RecentBuildLimit > 0 {
				request.RecentBuildLimit = &scope.RecentBuildLimit
			}
			request.ExpirationHours = &expirationHours
			if cmd.Flags().Changed("include-annotations") {
				request.IncludeAnnotations = &includeAnnotations
			}
			status, err := client.CreateAtlasShareLink(cmd.Context(), app.ID, request)
			if err != nil {
				return atlasMemberWriteError("share create", err)
			}
			if status.ShareableLink == nil || *status.ShareableLink == "" {
				return fmt.Errorf("atlas share create: the server did not return a share link")
			}
			analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "atlas_share", DomainStatus: "created"})
			if atlasJSONOutput(cmd) {
				result := atlasShareStatusContract("create", app.ID, scope, status)
				result["shareable_link"] = *status.ShareableLink
				return writeAtlasJSON(cmd, result)
			}
			cmd.PrintErrln("Atlas share link (anyone with it can view this map; revoke with revyl atlas share revoke):")
			_, err = fmt.Fprintln(cmd.OutOrStdout(), *status.ShareableLink)
			return err
		},
	}
	addAtlasShareScopeFlags(command)
	command.Flags().IntVar(&expirationHours, "expiration-hours", 30*24, "Hours until the link expires (1-876000)")
	command.Flags().BoolVar(&includeAnnotations, "include-annotations", false, "Show annotations read-only on the shared map; omit to keep the current setting")
	return command
}

func newAtlasShareStatusCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Show whether a map scope has an active share link (never prints the link)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, app, scope, err := atlasShareTarget(cmd)
			if err != nil {
				return err
			}
			status, err := client.GetAtlasShareLinkStatus(cmd.Context(), app.ID, scope)
			if err != nil {
				return atlasMemberWriteError("share status", err)
			}
			analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "atlas_share", DomainStatus: map[bool]string{true: "active", false: "inactive"}[status.Enabled]})
			summary := "No active share link for this scope"
			if status.Enabled {
				summary = "Active share link for this scope"
				if status.ExpiresAt != nil {
					summary += ", expires " + *status.ExpiresAt
				}
			}
			return printAtlasShareResult(cmd, atlasShareStatusContract("status", app.ID, scope, status), summary)
		},
	}
	addAtlasShareScopeFlags(command)
	return command
}

func newAtlasShareRevokeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke the active share link for a map scope",
		Long: `Revoke the active public share link for the selected branch, build and time
scope. The link is resolved from the scope, so the credential never travels in
argv or shell history.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, app, scope, err := atlasShareTarget(cmd)
			if err != nil {
				return err
			}
			status, err := client.GetAtlasShareLinkStatus(cmd.Context(), app.ID, scope)
			if err != nil {
				return atlasMemberWriteError("share revoke", err)
			}
			if !status.Enabled || status.Token == nil || *status.Token == "" {
				return fmt.Errorf("no active Atlas share link for this scope; check the scope with revyl atlas share status")
			}
			revoked, err := client.RevokeAtlasShareLink(cmd.Context(), app.ID, *status.Token)
			if err != nil {
				return atlasMemberWriteError("share revoke", err)
			}
			analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "atlas_share", DomainStatus: "revoked"})
			result := atlasShareStatusContract("revoke", app.ID, scope, revoked)
			result["share_id"] = status.ShareId
			return printAtlasShareResult(cmd, result, "Revoked the Atlas share link for this scope")
		},
	}
	addAtlasShareScopeFlags(command)
	return command
}

func atlasMemberWriteError(action string, err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return fmt.Errorf("atlas %s requires a Member, Admin or Owner role in this organization: %w", action, err)
	}
	return err
}

func optionalText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func writeAtlasJSON(cmd *cobra.Command, result map[string]interface{}) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func printAtlasShareResult(cmd *cobra.Command, result map[string]interface{}, summary string) error {
	if atlasJSONOutput(cmd) {
		return writeAtlasJSON(cmd, result)
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), summary); err != nil {
		return err
	}
	for _, action := range atlasStringSlice(result["next_actions"]) {
		cmd.PrintErrf("Next: %s\n", action)
	}
	return nil
}
