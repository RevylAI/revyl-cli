package device

import (
	"context"
	"testing"

	"github.com/revyl/cli/internal/api"
)

type contextArtifactResolver struct {
	selection *api.BuildSelection
	detail    api.BuildVersionDetail
}

func (r *contextArtifactResolver) ResolveBuildSelection(_ context.Context, _ string, selection api.BuildSelection) (*api.BuildVersionDetail, error) {
	r.selection = &selection
	return &r.detail, nil
}

func (r *contextArtifactResolver) GetBuildVersionDownloadURL(context.Context, string) (*api.BuildVersionDetail, error) {
	return &r.detail, nil
}

func TestStartArtifactGitBranchIsOnlyAnAutomaticHint(t *testing.T) {
	const buildID = "00000000-0000-4000-8000-000000000002"
	for _, tc := range []struct {
		name        string
		opts        StartArtifactOptions
		wantContext string
		wantResolve bool
	}{
		{"automatic", StartArtifactOptions{AppID: "app"}, "feature/source", true},
		{"latest", StartArtifactOptions{AppID: "app", BuildSelection: "latest"}, "feature/source", true},
		{"explicit branch", StartArtifactOptions{AppID: "app", BranchName: "main"}, "", true},
		{"exact build", StartArtifactOptions{AppID: "app", BuildVersionID: buildID}, "", false},
		{"raw URL", StartArtifactOptions{AppURL: "https://artifact.example/app.apk"}, "", false},
		{"blank device", StartArtifactOptions{}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &contextArtifactResolver{detail: api.BuildVersionDetail{ID: buildID, AppID: "app", DownloadURL: "https://artifact.example/app.apk"}}
			tc.opts.GitBranchName = "feature/source"
			if _, err := ResolveStartArtifact(context.Background(), resolver, tc.opts); err != nil {
				t.Fatal(err)
			}
			if (resolver.selection != nil) != tc.wantResolve {
				t.Fatalf("selection = %+v, want resolve %t", resolver.selection, tc.wantResolve)
			}
			if resolver.selection != nil && resolver.selection.GitBranchName != tc.wantContext {
				t.Fatalf("context = %q, want %q", resolver.selection.GitBranchName, tc.wantContext)
			}
		})
	}
}

func TestStartArtifactPreservesBranchSource(t *testing.T) {
	for _, source := range []api.BranchSource{"", api.BranchSourceGitBranch, api.BranchSourceDefaultBranch} {
		resolver := &contextArtifactResolver{detail: api.BuildVersionDetail{
			ID: "build", AppID: "app", BranchID: "branch", BranchName: "base",
			DownloadURL: "https://artifact.example/app.apk", BranchSource: source,
		}}
		artifact, err := ResolveStartArtifact(context.Background(), resolver, StartArtifactOptions{
			AppID: "app", GitBranchName: "feature/source",
		})
		if err != nil || artifact.BranchSource != source || artifact.BuildID != "build" || artifact.BranchName != "base" {
			t.Fatalf("artifact=%+v err=%v", artifact, err)
		}
	}
}

func TestStartArtifactPreservesDisabledAutomaticGitBranch(t *testing.T) {
	disabled := false
	resolver := &contextArtifactResolver{detail: api.BuildVersionDetail{
		ID: "build", AppID: "app", BranchID: "branch", BranchName: "base",
		DownloadURL: "https://artifact.example/app.apk", BranchSource: api.BranchSourceDefaultBranch,
		AutoGitBranchEnabled: &disabled,
	}}
	artifact, err := ResolveStartArtifact(context.Background(), resolver, StartArtifactOptions{AppID: "app", GitBranchName: "feature/source"})
	if err != nil || artifact.AutoGitBranchEnabled == nil || *artifact.AutoGitBranchEnabled || artifact.GitBranchName != "" || artifact.BranchName != "base" {
		t.Fatalf("artifact=%+v err=%v", artifact, err)
	}
}
