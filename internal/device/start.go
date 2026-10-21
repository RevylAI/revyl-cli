package device

import (
	"context"
	"fmt"
	"strings"

	"github.com/revyl/cli/internal/api"
)

// ArtifactResolver resolves app and build metadata needed to provision a device.
type ArtifactResolver interface {
	ResolveBuildSelection(ctx context.Context, appID string, selection api.BuildSelection) (*api.BuildVersionDetail, error)
	GetBuildVersionDownloadURL(ctx context.Context, versionID string) (*api.BuildVersionDetail, error)
}

// PlatformResolver resolves an app's target platform for inference.
type PlatformResolver interface {
	GetApp(ctx context.Context, appID string) (*api.App, error)
	GetBuildVersionDownloadURL(ctx context.Context, versionID string) (*api.BuildVersionDetail, error)
}

// LaunchVarResolver resolves org-scoped launch variables by listing the library.
type LaunchVarResolver interface {
	ListOrgLaunchVariables(ctx context.Context) (*api.OrgLaunchVariablesResponse, error)
}

// StartArtifactOptions contains optional app-selection inputs for device start.
type StartArtifactOptions struct {
	AppID          string
	BranchID       string
	BranchName     string
	GitBranchName  string
	BuildSelection string
	BuildVersionID string
	AppURL         string
	AppPackage     string
}

// ResolvedStartArtifact is the concrete app artifact payload sent to start_device.
type ResolvedStartArtifact struct {
	AutoGitBranchEnabled *bool
	BranchSource         api.BranchSource
	GitBranchName        string
	AppID                string
	BuildID              string
	BranchID             string
	BranchName           string
	AppURL               string
	AppPackage           string
}

// ResolveStartArtifact resolves app-selection inputs into a concrete artifact URL.
func ResolveStartArtifact(
	ctx context.Context,
	resolver ArtifactResolver,
	opts StartArtifactOptions,
) (ResolvedStartArtifact, error) {
	artifact := ResolvedStartArtifact{
		AppID:      strings.TrimSpace(opts.AppID),
		BuildID:    strings.TrimSpace(opts.BuildVersionID),
		AppURL:     strings.TrimSpace(opts.AppURL),
		AppPackage: strings.TrimSpace(opts.AppPackage),
	}

	selection := api.BuildSelection{
		BranchID: opts.BranchID, BranchName: opts.BranchName,
		Mode: api.BuildSelectionMode(opts.BuildSelection), BuildID: artifact.BuildID,
	}
	if artifact.BuildID == "" && selection.BranchID == "" && selection.BranchName == "" && selection.Mode != api.BuildSelectionModeLatestAllBranches {
		selection.GitBranchName = opts.GitBranchName
	}
	if selection.BranchID != "" || selection.BranchName != "" || selection.Mode != "" {
		if err := selection.Validate(); err != nil {
			return ResolvedStartArtifact{}, err
		}
	}
	if artifact.BuildID == "" && artifact.AppID == "" {
		if selection.BranchID != "" || selection.BranchName != "" || selection.Mode != "" {
			return ResolvedStartArtifact{}, fmt.Errorf("branch and build selection require an app ID or build version ID")
		}
		return artifact, nil
	}
	if artifact.AppURL != "" && artifact.BuildID == "" {
		if selection.BranchID != "" || selection.BranchName != "" || selection.Mode != "" {
			return ResolvedStartArtifact{}, fmt.Errorf("a raw app URL cannot be combined with branch or build selection")
		}
		return artifact, nil
	}
	var detail *api.BuildVersionDetail
	var err error
	if artifact.BuildID != "" {
		detail, err = resolver.GetBuildVersionDownloadURL(ctx, artifact.BuildID)
		if err != nil {
			return ResolvedStartArtifact{}, fmt.Errorf("failed to resolve build version %s: %w", artifact.BuildID, err)
		}
		if detail == nil {
			return ResolvedStartArtifact{}, fmt.Errorf("build version %s has no download URL", artifact.BuildID)
		}
		if artifact.AppID != "" && detail.AppID != artifact.AppID {
			return ResolvedStartArtifact{}, fmt.Errorf("selected build does not belong to app %s", artifact.AppID)
		}
		artifact.AppID = detail.AppID
		if selection.Mode != "" && selection.Mode != "pinned" {
			return ResolvedStartArtifact{}, fmt.Errorf("an exact build can't be combined with Automatic, Latest build, or All branches · Latest build")
		}
	}
	if detail == nil || selection.BranchID != "" || selection.BranchName != "" {
		detail, err = resolver.ResolveBuildSelection(ctx, artifact.AppID, selection)
		if err != nil {
			return ResolvedStartArtifact{}, fmt.Errorf("failed to resolve build: %w", err)
		}
	}
	if detail == nil || strings.TrimSpace(detail.ID) == "" {
		return ResolvedStartArtifact{}, fmt.Errorf("no builds found for app %s", artifact.AppID)
	}
	if strings.TrimSpace(detail.DownloadURL) == "" {
		return ResolvedStartArtifact{}, fmt.Errorf("selected build for app %s has no download URL", artifact.AppID)
	}
	artifact.BuildID = detail.ID
	artifact.AppID = detail.AppID
	artifact.BranchID = detail.BranchID
	artifact.BranchName = detail.BranchName
	artifact.BranchSource, artifact.GitBranchName = detail.BranchSource, detail.GitBranchName
	artifact.AutoGitBranchEnabled = detail.AutoGitBranchEnabled
	artifact.AppURL = strings.TrimSpace(detail.DownloadURL)
	if artifact.AppPackage == "" {
		artifact.AppPackage = strings.TrimSpace(detail.PackageName)
	}

	return artifact, nil
}

// ResolveLaunchVar resolves a launch variable by key or ID.
func ResolveLaunchVar(
	ctx context.Context,
	resolver LaunchVarResolver,
	keyOrID string,
) (api.OrgLaunchVariable, error) {
	needle := strings.TrimSpace(keyOrID)
	if needle == "" {
		return api.OrgLaunchVariable{}, fmt.Errorf("launch variable key or ID cannot be empty")
	}

	resp, err := resolver.ListOrgLaunchVariables(ctx)
	if err != nil {
		return api.OrgLaunchVariable{}, fmt.Errorf("failed to list launch variables: %w", err)
	}

	for _, v := range resp.Result {
		if strings.TrimSpace(v.ID) == needle {
			return v, nil
		}
	}
	for _, v := range resp.Result {
		if v.Key == needle {
			return v, nil
		}
	}

	var folded *api.OrgLaunchVariable
	for _, v := range resp.Result {
		if strings.EqualFold(v.Key, needle) {
			if folded != nil {
				return api.OrgLaunchVariable{}, fmt.Errorf("multiple launch variables match %q; use the UUID instead", keyOrID)
			}
			match := v
			folded = &match
		}
	}
	if folded != nil {
		return *folded, nil
	}

	return api.OrgLaunchVariable{}, fmt.Errorf("launch variable %q not found", keyOrID)
}

// InferPlatform infers the device platform ("ios" or "android") from app
// selection inputs. Priority:
//  1. AppURL extension (.apk → android, .ipa → ios)
//  2. BuildVersionID → app_id → app.platform
//  3. AppID → app.platform
//
// Returns ("", nil) when no input is sufficient to infer (e.g. .zip URL with
// no app/build context). API failures propagate as errors.
func InferPlatform(
	ctx context.Context,
	resolver PlatformResolver,
	opts StartArtifactOptions,
) (string, error) {
	if p := platformFromAppURL(opts.AppURL); p != "" {
		return p, nil
	}

	buildVersionID := strings.TrimSpace(opts.BuildVersionID)
	if buildVersionID != "" {
		detail, err := resolver.GetBuildVersionDownloadURL(ctx, buildVersionID)
		if err != nil {
			return "", fmt.Errorf("failed to resolve build version %s for platform inference: %w", buildVersionID, err)
		}
		if detail != nil {
			if p := platformFromAppURL(detail.DownloadURL); p != "" {
				return p, nil
			}
			if appID := strings.TrimSpace(detail.AppID); appID != "" {
				return platformFromApp(ctx, resolver, appID)
			}
		}
	}

	appID := strings.TrimSpace(opts.AppID)
	if appID != "" {
		return platformFromApp(ctx, resolver, appID)
	}

	return "", nil
}

func platformFromApp(ctx context.Context, resolver PlatformResolver, appID string) (string, error) {
	app, err := resolver.GetApp(ctx, appID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve app %s for platform inference: %w", appID, err)
	}
	if app == nil {
		return "", nil
	}
	return normalizeInferredPlatform(app.Platform), nil
}

func platformFromAppURL(rawURL string) string {
	url := strings.ToLower(strings.TrimSpace(rawURL))
	if url == "" {
		return ""
	}
	// Strip query/fragment so presigned URLs don't mask the extension.
	if idx := strings.IndexAny(url, "?#"); idx >= 0 {
		url = url[:idx]
	}
	switch {
	case strings.HasSuffix(url, ".apk"), strings.HasSuffix(url, ".aab"):
		return "android"
	case strings.HasSuffix(url, ".ipa"), strings.HasSuffix(url, ".app"):
		return "ios"
	default:
		return ""
	}
}

func normalizeInferredPlatform(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ios":
		return "ios"
	case "android":
		return "android"
	default:
		return ""
	}
}

// ResolveLaunchVarIDs resolves and de-duplicates launch variable references.
func ResolveLaunchVarIDs(
	ctx context.Context,
	resolver LaunchVarResolver,
	keyOrIDs []string,
) ([]string, error) {
	return resolveLaunchConfigurationIDs(ctx, resolver, keyOrIDs, "key_value")
}

// ResolveLaunchConfigurationSelection resolves inherited configurations of
// either kind, followed by explicit key/value variables and explicit iOS
// argument sets. The returned IDs preserve that precedence order and are
// de-duplicated across all three sources.
func ResolveLaunchConfigurationSelection(
	ctx context.Context,
	resolver LaunchVarResolver,
	inheritedIDs []string,
	launchVars []string,
	launchArgumentSets []string,
) ([]string, error) {
	inherited, err := resolveLaunchConfigurationIDs(ctx, resolver, inheritedIDs, "")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve inherited launch configurations: %w", err)
	}
	explicitVars, err := ResolveLaunchVarIDs(ctx, resolver, launchVars)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve launch variables: %w", err)
	}
	explicitArgumentSets, err := resolveLaunchConfigurationIDs(
		ctx,
		resolver,
		launchArgumentSets,
		"ios_arguments",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve iOS argument sets: %w", err)
	}

	ordered := make([]string, 0, len(inherited)+len(explicitVars)+len(explicitArgumentSets))
	seen := make(map[string]struct{}, cap(ordered))
	for _, ids := range [][]string{inherited, explicitVars, explicitArgumentSets} {
		for _, id := range ids {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ordered = append(ordered, id)
		}
	}
	return ordered, nil
}

func resolveLaunchConfigurationIDs(
	ctx context.Context,
	resolver LaunchVarResolver,
	nameOrIDs []string,
	expectedKind string,
) ([]string, error) {
	ids := make([]string, 0, len(nameOrIDs))
	seen := make(map[string]struct{}, len(nameOrIDs))
	for _, nameOrID := range nameOrIDs {
		configuration, err := ResolveLaunchVar(ctx, resolver, nameOrID)
		if err != nil {
			return nil, err
		}
		kind := configuration.Kind
		if kind == "" {
			kind = "key_value"
		}
		switch {
		case expectedKind == "key_value" && kind != expectedKind:
			return nil, fmt.Errorf(
				"launch configuration %q is an iOS argument set; use --launch-arg-set",
				configuration.Key,
			)
		case expectedKind == "ios_arguments" && kind != expectedKind:
			return nil, fmt.Errorf(
				"launch configuration %q is an environment variable; use --launch-var",
				configuration.Key,
			)
		case kind != "key_value" && kind != "ios_arguments":
			return nil, fmt.Errorf(
				"launch configuration %q has unsupported kind %q",
				configuration.Key,
				kind,
			)
		}
		if configuration.HasValue != nil && !*configuration.HasValue {
			if kind == "ios_arguments" {
				return nil, fmt.Errorf(
					"iOS argument set %q has no stored arguments",
					configuration.Key,
				)
			}
			return nil, fmt.Errorf(
				"launch variable %q has no stored value",
				configuration.Key,
			)
		}
		if _, exists := seen[configuration.ID]; exists {
			continue
		}
		seen[configuration.ID] = struct{}{}
		ids = append(ids, configuration.ID)
	}
	return ids, nil
}
