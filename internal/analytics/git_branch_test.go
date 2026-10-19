package analytics

import (
	"context"
	"testing"
)

func TestGitBranchOriginIsBoundedAndPreservesCompletion(t *testing.T) {
	for _, origin := range []string{"none", "local_git", "github_actions", "buildkite", "submitted_git", "private/customer-branch"} {
		t.Run(origin, func(t *testing.T) {
			recorder := testRecorder()
			run := testCommandRun(recorder)
			ctx := ContextWithCommandRun(context.Background(), run)
			SetCommandCompletion(ctx, CommandCompletion{Domain: "test_run", DomainStatus: "passed", Properties: map[string]interface{}{"test_task_id": "task-123"}})
			SetGitBranchOrigin(ctx, origin)
			SetCommandCompletion(ctx, CommandCompletion{Properties: map[string]interface{}{"build_id": "build-123"}})
			run.Complete(nil)
			event := lastEvent(t, recorder)
			if origin == "private/customer-branch" {
				origin = "none"
			}
			if event.Properties["git_branch_origin"] != origin || event.Properties["domain"] != "test_run" || event.Properties["domain_status"] != "passed" || event.Properties["test_task_id"] != "task-123" || event.Properties["build_id"] != "build-123" {
				t.Fatalf("completion metadata changed: %#v", event.Properties)
			}
			if len(recorder.events) != 1 {
				t.Fatalf("expected one terminal event, got %d", len(recorder.events))
			}
		})
	}
	SetGitBranchOrigin(context.Background(), "local_git")
}
