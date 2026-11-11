package tui

import (
	"context"
	"strings"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/checkoutdefinitions"
	"github.com/revyl/cli/internal/config"
)

// collectCheckoutUpload returns what a run from the current checkout sends for
// testIDs, or nil when project doesn't run tests from Git, as revyl test run
// decides without --from-checkout.
func collectCheckoutUpload(ctx context.Context, project *config.ProjectContext, testIDs []string) (*checkoutdefinitions.Upload, error) {
	runProject, _ := checkoutdefinitions.RunProject(project, nil)
	if runProject == nil {
		return nil, nil
	}
	return checkoutdefinitions.CollectUpload(ctx, runProject, testIDs, "run it with revyl test run --from-checkout=false")
}

// noticeWithTestDefinition adds to notice the warnings a checkout upload found
// and what the run executed, when the server said.
func noticeWithTestDefinition(notice string, upload *checkoutdefinitions.Upload, testCountBySource map[api.TestDefinitionSource]int, testCountByFallbackReason map[api.TestDefinitionFallbackReason]int, gitRead api.TestDefinitionGitRead) string {
	lines := []string{}
	if notice != "" {
		lines = append(lines, notice)
	}
	if upload != nil {
		lines = append(lines, upload.Warnings...)
	}
	if len(testCountBySource) > 0 {
		lines = append(lines, "Definition: "+upload.DefinitionLine(testCountBySource, testCountByFallbackReason, gitRead))
	}
	return strings.Join(lines, "\n")
}
