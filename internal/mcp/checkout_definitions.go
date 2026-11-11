package mcp

import (
	"context"
	"fmt"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/checkoutdefinitions"
	"github.com/revyl/cli/internal/config"
)

// collectCheckoutUpload returns what a run from runProject's checkout sends
// for testIDs.
func collectCheckoutUpload(ctx context.Context, runProject *config.ProjectContext, testIDs []string) (*checkoutdefinitions.Upload, error) {
	upload, err := checkoutdefinitions.CollectUpload(ctx, runProject, testIDs, "call again with from_checkout=false")
	if err != nil {
		return nil, fmt.Errorf("read this checkout's test files: %w", err)
	}
	return upload, nil
}

// testCountsByName turns a run's per-source or per-reason test counts into
// plain string keys for tool output.
func testCountsByName[Key ~string](countByKey map[Key]int) map[string]int {
	if len(countByKey) == 0 {
		return nil
	}
	countByName := make(map[string]int, len(countByKey))
	for key, count := range countByKey {
		countByName[string(key)] = count
	}
	return countByName
}

// testDefinitionSummary says what a run executed, or "" when the server
// didn't say, which a project that doesn't run tests from Git never does.
func testDefinitionSummary(upload *checkoutdefinitions.Upload, testCountBySource map[api.TestDefinitionSource]int, testCountByFallbackReason map[api.TestDefinitionFallbackReason]int, gitRead api.TestDefinitionGitRead) string {
	if len(testCountBySource) == 0 {
		return ""
	}
	return upload.DefinitionLine(testCountBySource, testCountByFallbackReason, gitRead)
}
