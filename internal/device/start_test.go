package device

import (
	"context"
	"errors"
	"testing"

	"github.com/revyl/cli/internal/api"
)

type stubArtifactResolver struct {
	latestByApp   map[string]*api.BuildVersion
	detailByBuild map[string]*api.BuildVersionDetail
	latestErr     error
	detailErr     error
}

func (s stubArtifactResolver) GetLatestBuildVersion(ctx context.Context, appID string) (*api.BuildVersion, error) {
	if s.latestErr != nil {
		return nil, s.latestErr
	}
	return s.latestByApp[appID], nil
}

func (s stubArtifactResolver) GetBuildVersionDownloadURL(ctx context.Context, versionID string) (*api.BuildVersionDetail, error) {
	if s.detailErr != nil {
		return nil, s.detailErr
	}
	return s.detailByBuild[versionID], nil
}

func TestResolveStartArtifact_UsesLatestBuildForApp(t *testing.T) {
	t.Parallel()

	resolved, err := ResolveStartArtifact(context.Background(), stubArtifactResolver{
		latestByApp: map[string]*api.BuildVersion{
			"app-1": {ID: "build-1"},
		},
		detailByBuild: map[string]*api.BuildVersionDetail{
			"build-1": {
				ID:          "build-1",
				DownloadURL: "https://artifact.example/app.ipa",
				PackageName: "com.example.app",
			},
		},
	}, StartArtifactOptions{AppID: "app-1"})
	if err != nil {
		t.Fatalf("ResolveStartArtifact returned error: %v", err)
	}
	if resolved.AppURL != "https://artifact.example/app.ipa" {
		t.Fatalf("AppURL = %q, want %q", resolved.AppURL, "https://artifact.example/app.ipa")
	}
	if resolved.AppPackage != "com.example.app" {
		t.Fatalf("AppPackage = %q, want %q", resolved.AppPackage, "com.example.app")
	}
}

func TestResolveStartArtifact_ErrorsWhenAppHasNoBuilds(t *testing.T) {
	t.Parallel()

	_, err := ResolveStartArtifact(context.Background(), stubArtifactResolver{}, StartArtifactOptions{AppID: "app-empty"})
	if err == nil {
		t.Fatal("expected error for app with no builds")
	}
	if got := err.Error(); got != "no builds found for app app-empty" {
		t.Fatalf("error = %q, want %q", got, "no builds found for app app-empty")
	}
}

func TestResolveStartArtifact_PropagatesBuildLookupFailure(t *testing.T) {
	t.Parallel()

	_, err := ResolveStartArtifact(context.Background(), stubArtifactResolver{
		detailErr: errors.New("boom"),
	}, StartArtifactOptions{BuildVersionID: "build-1"})
	if err == nil {
		t.Fatal("expected error from build lookup")
	}
	if got := err.Error(); got != "failed to resolve build version build-1: boom" {
		t.Fatalf("error = %q, want %q", got, "failed to resolve build version build-1: boom")
	}
}

func TestResolveStartArtifact_NilResponseDoesNotWrapNilError(t *testing.T) {
	t.Parallel()

	_, err := ResolveStartArtifact(context.Background(), stubArtifactResolver{}, StartArtifactOptions{AppID: "app-nil"})
	if err == nil {
		t.Fatal("expected error for app with nil response")
	}
	got := err.Error()
	if got != "no builds found for app app-nil" {
		t.Fatalf("error = %q, want %q", got, "no builds found for app app-nil")
	}
	if errors.Unwrap(err) != nil {
		t.Fatalf("error wraps a non-nil cause %v; nil-response errors must not use %%w", errors.Unwrap(err))
	}
}

func TestResolveStartArtifact_PropagatesLatestBuildAPIError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("api timeout")
	_, err := ResolveStartArtifact(context.Background(), stubArtifactResolver{
		latestErr: sentinel,
	}, StartArtifactOptions{AppID: "app-err"})
	if err == nil {
		t.Fatal("expected error from GetLatestBuildVersion failure")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain does not contain sentinel; got %q", err)
	}
}

func TestResolveStartArtifact_UsesTrimmedDirectAppURL(t *testing.T) {
	t.Parallel()

	resolved, err := ResolveStartArtifact(context.Background(), stubArtifactResolver{}, StartArtifactOptions{
		AppURL:     "  https://artifact.example/direct.ipa  ",
		AppPackage: "  com.example.direct  ",
	})
	if err != nil {
		t.Fatalf("ResolveStartArtifact returned error: %v", err)
	}
	if resolved.AppURL != "https://artifact.example/direct.ipa" {
		t.Fatalf("AppURL = %q, want %q", resolved.AppURL, "https://artifact.example/direct.ipa")
	}
	if resolved.AppPackage != "com.example.direct" {
		t.Fatalf("AppPackage = %q, want %q", resolved.AppPackage, "com.example.direct")
	}
}
