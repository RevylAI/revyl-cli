package execution

import (
	"context"
	"strings"
	"testing"
)

func TestRunTestRejectsInvalidLaunchArgumentsBeforeAPIWork(t *testing.T) {
	result, err := RunTest(context.Background(), "", nil, RunTestParams{
		TestNameOrID:    "test-id",
		LaunchArguments: []string{"--mode", ""},
	})
	if result != nil {
		t.Fatalf("RunTest() result = %#v, want nil", result)
	}
	if err == nil || !strings.Contains(err.Error(), "token 2 cannot be empty") {
		t.Fatalf("RunTest() error = %v", err)
	}
}

func TestRunWorkflowRejectsInvalidLaunchArgumentsBeforeAPIWork(t *testing.T) {
	result, err := RunWorkflow(context.Background(), "", nil, RunWorkflowParams{
		WorkflowNameOrID: "workflow-id",
		LaunchArguments:  []string{"before\x00after"},
	})
	if result != nil {
		t.Fatalf("RunWorkflow() result = %#v, want nil", result)
	}
	if err == nil || !strings.Contains(err.Error(), "token 1 cannot contain NUL bytes") {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
}
