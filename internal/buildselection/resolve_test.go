package buildselection

import (
	"context"
	"errors"
	"testing"

	"github.com/revyl/cli/internal/api"
)

type fakeClient struct {
	pages      map[int]*api.BuildVersionsPage
	errByPage  map[int]error
	emptyPages bool
}

func (f *fakeClient) ListBuildVersionsPage(_ context.Context, _ string, page int, _ int) (*api.BuildVersionsPage, error) {
	if err, ok := f.errByPage[page]; ok {
		return nil, err
	}
	if result, ok := f.pages[page]; ok {
		return result, nil
	}
	if f.emptyPages {
		return &api.BuildVersionsPage{Items: []api.BuildVersion{}}, nil
	}
	return &api.BuildVersionsPage{Items: []api.BuildVersion{}, Page: page}, nil
}

func TestSelectPreferredBuildVersionForBranch_PrefersBranchMatchFromLaterPage(t *testing.T) {
	client := &fakeClient{
		pages: map[int]*api.BuildVersionsPage{
			1: {
				Items: []api.BuildVersion{
					{
						ID:      "latest-overall",
						Version: "v2",
						Metadata: map[string]interface{}{
							"git": map[string]interface{}{
								"branch": "main",
							},
						},
					},
				},
				Page:       1,
				TotalPages: 2,
				HasNext:    true,
			},
			2: {
				Items: []api.BuildVersion{
					{
						ID:      "branch-match",
						Version: "v1",
						Metadata: map[string]interface{}{
							"git": map[string]interface{}{
								"branch": "feature/new-login",
							},
						},
					},
				},
				Page:       2,
				TotalPages: 2,
				HasNext:    false,
			},
		},
	}

	selected, source, warnings, err := SelectPreferredBuildVersionForBranch(
		context.Background(),
		client,
		"app-id",
		"feature/new-login",
	)
	if err != nil {
		t.Fatalf("SelectPreferredBuildVersionForBranch() error = %v", err)
	}
	if selected == nil {
		t.Fatal("selected build is nil")
	}
	if selected.ID != "branch-match" {
		t.Fatalf("selected.ID = %q, want %q", selected.ID, "branch-match")
	}
	if source != "branch:feature/new-login" {
		t.Fatalf("source = %q, want %q", source, "branch:feature/new-login")
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestSelectPreferredBuildVersionForBranch_FallsBackToLatestAcrossPages(t *testing.T) {
	client := &fakeClient{
		pages: map[int]*api.BuildVersionsPage{
			1: {
				Items: []api.BuildVersion{
					{
						ID:      "latest-overall",
						Version: "v2",
						Metadata: map[string]interface{}{
							"git": map[string]interface{}{
								"branch": "main",
							},
						},
					},
				},
				Page:       1,
				TotalPages: 2,
				HasNext:    true,
			},
			2: {
				Items: []api.BuildVersion{
					{
						ID:      "other-branch",
						Version: "v1",
						Metadata: map[string]interface{}{
							"git": map[string]interface{}{
								"branch": "release/candidate",
							},
						},
					},
				},
				Page:       2,
				TotalPages: 2,
				HasNext:    false,
			},
		},
	}

	selected, source, warnings, err := SelectPreferredBuildVersionForBranch(
		context.Background(),
		client,
		"app-id",
		"feature/new-login",
	)
	if err != nil {
		t.Fatalf("SelectPreferredBuildVersionForBranch() error = %v", err)
	}
	if selected == nil {
		t.Fatal("selected build is nil")
	}
	if selected.ID != "latest-overall" {
		t.Fatalf("selected.ID = %q, want %q", selected.ID, "latest-overall")
	}
	if source != "latest" {
		t.Fatalf("source = %q, want %q", source, "latest")
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one warning", warnings)
	}
}

func TestSelectPreferredBuildVersionForBranch_ErrorsWhenLaterPageFails(t *testing.T) {
	client := &fakeClient{
		pages: map[int]*api.BuildVersionsPage{
			1: {
				Items: []api.BuildVersion{
					{
						ID:      "latest-overall",
						Version: "v2",
						Metadata: map[string]interface{}{
							"git": map[string]interface{}{
								"branch": "main",
							},
						},
					},
				},
				Page:       1,
				TotalPages: 2,
				HasNext:    true,
			},
		},
		errByPage: map[int]error{
			2: errors.New("page 2 fetch failed"),
		},
	}

	selected, source, warnings, err := SelectPreferredBuildVersionForBranch(
		context.Background(),
		client,
		"app-id",
		"feature/new-login",
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if selected != nil || source != "" || warnings != nil {
		t.Fatalf("expected no result tuple on error, got selected=%v source=%q warnings=%v", selected, source, warnings)
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
