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
