package main

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func TestRemoteBuildRequestPreservesRawContextSeparately(t *testing.T) {
	var source api.RemoteBuildRequest_Source
	if err := source.FromRemoteBuildArchiveSource(api.RemoteBuildArchiveSource{Key: "source-key"}); err != nil {
		t.Fatal(err)
	}
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	resolved := remoteBuildPlatformConfigFromProject(projectBuildInvocation{
		AppID: appID.String(), Platform: "android", Recipe: config.EffectiveBuildRecipe{},
	})
	for _, hint := range []string{"feature/Écran\u00a0", " invalid but irrelevant to explicit branch "} {
		request, err := newRemoteBuildTriggerRequest(source, appID, resolved, remoteBuildOptions{BranchName: "main", ContextBranchName: hint})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded api.RemoteBuildRequest
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.ContextBranchName == nil || *decoded.ContextBranchName != hint || decoded.BranchName == nil || *decoded.BranchName != "main" {
			t.Fatalf("raw context changed: %+v", decoded)
		}
	}
}
