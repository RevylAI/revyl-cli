package buildselection

import (
	"context"
	"errors"
	"testing"

	"github.com/revyl/cli/internal/api"
)

type fakeClient struct {
	selection api.BuildSelection
	err       error
}

func (f *fakeClient) ResolveBuildSelection(_ context.Context, _ string, s api.BuildSelection) (*api.BuildVersionDetail, error) {
	f.selection = s
	return &api.BuildVersionDetail{ID: "resolved", BranchID: "branch", BranchName: "main"}, f.err
}

func TestSelectPreferredUsesAppDefaultWithoutGitInference(t *testing.T) {
	client := &fakeClient{}
	selected, source, warnings, err := SelectPreferredBuildVersion(context.Background(), client, "app-id", t.TempDir())
	if err != nil || selected.ID != "resolved" || source != "default_branch" || len(warnings) != 0 || client.selection.BranchName != "" {
		t.Fatalf("unexpected result: %v %s %v %v", selected, source, warnings, err)
	}
}
func TestExplicitBranchDoesNotFallback(t *testing.T) {
	for _, name := range []string{"main", "HEAD", "all"} {
		client := &fakeClient{err: api.ErrBranchHasNoBuilds}
		selected, _, _, err := SelectPreferredBuildVersionForBranch(context.Background(), client, "app-id", name)
		if !errors.Is(err, api.ErrBranchHasNoBuilds) || selected != nil || client.selection.BranchName != name {
			t.Fatalf("unexpected result for %s", name)
		}
	}
}

type fakeBranchBuildsClient struct {
	fakeClient
	resolved   api.BuildVersionDetail
	pages      []api.BuildVersionsPage
	listedWith []api.BuildListOptions
}

func (f *fakeBranchBuildsClient) ResolveBuildSelection(_ context.Context, _ string, s api.BuildSelection) (*api.BuildVersionDetail, error) {
	f.selection = s
	if f.err != nil {
		return nil, f.err
	}
	resolved := f.resolved
	return &resolved, nil
}

func (f *fakeBranchBuildsClient) ListBuildVersionsWithOptions(_ context.Context, _ string, opts api.BuildListOptions) (*api.BuildVersionsPage, error) {
	f.listedWith = append(f.listedWith, opts)
	return &f.pages[opts.Page-1], nil
}

func gitBuild(id, gitBranch string) api.BuildVersion {
	return api.BuildVersion{ID: id, Metadata: map[string]interface{}{"git": map[string]interface{}{"branch": gitBranch}}}
}

var latestOnBase = api.BuildSelection{BranchID: "base-id", Mode: api.BuildSelectionModeLatest}

func TestSelectLatestPreferringGitBranchPicksNewestMatchingBuildOnTheBranch(t *testing.T) {
	client := &fakeBranchBuildsClient{
		resolved: api.BuildVersionDetail{ID: "newest", BranchName: "base"},
		pages: []api.BuildVersionsPage{
			{Items: []api.BuildVersion{gitBuild("newest", "main"), gitBuild("other", "release")}, HasNext: true},
			{Items: []api.BuildVersion{gitBuild("feature-new", "feature/login"), gitBuild("feature-old", "feature/login")}},
		},
	}
	selected, source, warnings, err := SelectLatestBuildVersionPreferringGitBranch(context.Background(), client, "app-id", "base-id", "feature/login")
	if err != nil || selected.ID != "feature-new" || source != "git_branch" || len(warnings) != 0 {
		t.Fatalf("unexpected result: %v %s %v %v", selected, source, warnings, err)
	}
	if len(client.listedWith) != 2 || client.listedWith[0].BranchID != "base-id" || client.listedWith[1].Page != 2 {
		t.Fatalf("scanned %+v", client.listedWith)
	}
}

func TestSelectLatestPreferringGitBranchFallsBackToTheBranchLatestBuild(t *testing.T) {
	client := &fakeBranchBuildsClient{
		resolved: api.BuildVersionDetail{ID: "newest-usable", BranchName: "base"},
		pages:    []api.BuildVersionsPage{{Items: []api.BuildVersion{gitBuild("preview", "main"), gitBuild("newest-usable", "main"), gitBuild("pinned", "main")}}},
	}
	selected, _, warnings, err := SelectLatestBuildVersionPreferringGitBranch(context.Background(), client, "app-id", "base-id", "feature/login")
	if err != nil || selected.ID != "newest-usable" || client.selection != latestOnBase {
		t.Fatalf("unexpected result: %v %+v %v", selected, client.selection, err)
	}
	if len(warnings) != 1 || warnings[0] != `No builds found for current branch "feature/login"; using latest available build instead.` {
		t.Fatalf("warnings = %q", warnings)
	}
}

func TestSelectLatestPreferringGitBranchUsesLatestWithoutGitBranch(t *testing.T) {
	client := &fakeBranchBuildsClient{resolved: api.BuildVersionDetail{ID: "resolved"}}
	selected, _, warnings, err := SelectLatestBuildVersionPreferringGitBranch(context.Background(), client, "app-id", "base-id", "")
	if err != nil || selected.ID != "resolved" || len(warnings) != 0 || len(client.listedWith) != 0 || client.selection != latestOnBase {
		t.Fatalf("unexpected result: %v %v %v %+v %+v", selected, warnings, err, client.listedWith, client.selection)
	}
}

func TestSelectLatestPreferringGitBranchReportsNothingToInstallWithoutWarning(t *testing.T) {
	for _, missing := range []error{api.ErrBranchHasNoBuilds, api.ErrBranchHasNoCompatibleBuild} {
		client := &fakeBranchBuildsClient{fakeClient: fakeClient{err: missing}, pages: []api.BuildVersionsPage{{}}}
		selected, _, warnings, err := SelectLatestBuildVersionPreferringGitBranch(context.Background(), client, "app-id", "base-id", "feature/login")
		if !errors.Is(err, missing) || selected != nil || len(warnings) != 0 {
			t.Fatalf("unexpected result for %v: %v %v %v", missing, selected, warnings, err)
		}
	}
}

func TestExtractBranch(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]interface{}
		want     string
	}{
		{
			name: "git branch",
			metadata: map[string]interface{}{
				"git": map[string]interface{}{
					"branch": "feature/a",
				},
			},
			want: "feature/a",
		},
		{
			name: "source metadata branch",
			metadata: map[string]interface{}{
				"source_metadata": map[string]interface{}{
					"branch": "feature/b",
				},
			},
			want: "feature/b",
		},
		{
			name: "top level branch",
			metadata: map[string]interface{}{
				"branch": "feature/c",
			},
			want: "feature/c",
		},
		{
			name:     "missing",
			metadata: map[string]interface{}{},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractBranch(tt.metadata)
			if got != tt.want {
				t.Fatalf("ExtractBranch() = %q, want %q", got, tt.want)
			}
		})
	}
}
