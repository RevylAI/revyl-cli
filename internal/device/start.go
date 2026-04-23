package device

import (
	"context"
	"fmt"
	"strings"

	"github.com/revyl/cli/internal/api"
)

// ArtifactResolver resolves app and build metadata needed to provision a device.
type ArtifactResolver interface {
	GetLatestBuildVersion(ctx context.Context, appID string) (*api.BuildVersion, error)
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
	BuildVersionID string
	AppURL         string
	AppPackage     string
}

// ResolvedStartArtifact is the concrete app artifact payload sent to start_device.
type ResolvedStartArtifact struct {
	AppURL     string
	AppPackage string
}

// ResolveStartArtifact resolves app-selection inputs into a concrete artifact URL.
func ResolveStartArtifact(
	ctx context.Context,
	resolver ArtifactResolver,
	opts StartArtifactOptions,
) (ResolvedStartArtifact, error) {
	artifact := ResolvedStartArtifact{
		AppURL:     strings.TrimSpace(opts.AppURL),
		AppPackage: strings.TrimSpace(opts.AppPackage),
	}

	buildVersionID := strings.TrimSpace(opts.BuildVersionID)
	if artifact.AppURL == "" && buildVersionID != "" {
		detail, err := resolver.GetBuildVersionDownloadURL(ctx, buildVersionID)
		if err != nil {
			return ResolvedStartArtifact{}, fmt.Errorf("failed to resolve build version %s: %w", buildVersionID, err)
		}
		if detail == nil || strings.TrimSpace(detail.DownloadURL) == "" {
			return ResolvedStartArtifact{}, fmt.Errorf("build version %s has no download URL", buildVersionID)
		}
		artifact.AppURL = strings.TrimSpace(detail.DownloadURL)
		if artifact.AppPackage == "" {
			artifact.AppPackage = strings.TrimSpace(detail.PackageName)
		}
	}

	appID := strings.TrimSpace(opts.AppID)
	if artifact.AppURL == "" && appID != "" {
		latest, err := resolver.GetLatestBuildVersion(ctx, appID)
		if err != nil {
			return ResolvedStartArtifact{}, fmt.Errorf("failed to resolve latest build for app %s: %w", appID, err)
		}
		if latest == nil || strings.TrimSpace(latest.ID) == "" {
			return ResolvedStartArtifact{}, fmt.Errorf("no builds found for app %s", appID)
		}

		detail, err := resolver.GetBuildVersionDownloadURL(ctx, strings.TrimSpace(latest.ID))
		if err != nil {
			return ResolvedStartArtifact{}, fmt.Errorf("failed to resolve latest build artifact for app %s: %w", appID, err)
		}
		if detail == nil || strings.TrimSpace(detail.DownloadURL) == "" {
			return ResolvedStartArtifact{}, fmt.Errorf("latest build for app %s has no download URL", appID)
		}
		artifact.AppURL = strings.TrimSpace(detail.DownloadURL)
		if artifact.AppPackage == "" {
			artifact.AppPackage = strings.TrimSpace(detail.PackageName)
		}
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
	if len(keyOrIDs) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(keyOrIDs))
	seen := make(map[string]struct{}, len(keyOrIDs))
	for _, keyOrID := range keyOrIDs {
		variable, err := ResolveLaunchVar(ctx, resolver, keyOrID)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[variable.ID]; ok {
			continue
		}
		seen[variable.ID] = struct{}{}
		ids = append(ids, variable.ID)
	}
	return ids, nil
}
