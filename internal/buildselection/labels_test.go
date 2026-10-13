package buildselection

import (
	"encoding/json"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestBranchDefaultBuildLabelNamesTheBuildARunUses(t *testing.T) {
	var page api.BuildBranchesPage
	if err := json.Unmarshal([]byte(`{"branches":[`+
		`{"id":"b1","name":"base","pinned_build_id":"build-12","pinned_build_version":"v1.2","pinned_build_available":true,"latest_build":{"id":"13131313-1313-4313-8313-131313131313","version":"v1.3","uploaded_at":"2026-10-11T00:00:00Z"}},`+
		`{"id":"b2","name":"main","latest_build":{"id":"20202020-2020-4020-8020-202020202020","version":"v2.0","uploaded_at":"2026-10-11T00:00:00Z"}},`+
		`{"id":"b3","name":"release","latest_build":null},`+
		`{"id":"b4","name":"legacy","pinned_build_id":"build-1"},`+
		`{"id":"b5","name":"gone","pinned_build_id":"build-2","pinned_build_version":"v0.2","pinned_build_available":false}`+
		`]}`), &page); err != nil {
		t.Fatal(err)
	}
	want := []string{"Pinned build (v1.2)", "Latest build (v2.0)", "Latest build (no builds)", "Pinned build (build-1)", "Pinned build (v0.2, unavailable)"}
	for index, branch := range page.Branches {
		if got := BranchDefaultBuildLabel(branch); got != want[index] {
			t.Fatalf("%s default build = %q, want %q", branch.Name, got, want[index])
		}
	}
}
