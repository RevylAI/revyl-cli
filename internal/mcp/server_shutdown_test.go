package mcp

import (
	"context"
	"testing"

	"github.com/revyl/cli/internal/devloop"
)

type shutdownTrackingRunner struct {
	stopCalled bool
}

func (r *shutdownTrackingRunner) Start(
	ctx context.Context,
	workDir string,
	request devloop.StartRequest,
) (devloop.StartResult, error) {
	return devloop.StartResult{}, nil
}

func (r *shutdownTrackingRunner) Status(
	ctx context.Context,
	workDir string,
	contextName string,
) (devloop.StatusResult, error) {
	return devloop.StatusResult{}, nil
}

func (r *shutdownTrackingRunner) Rebuild(
	ctx context.Context,
	workDir string,
	request devloop.RebuildRequest,
) (devloop.RebuildResult, error) {
	return devloop.RebuildResult{}, nil
}

func (r *shutdownTrackingRunner) TriggerRebuild(
	ctx context.Context,
	workDir string,
	request devloop.TriggerRebuildRequest,
) (devloop.RebuildHandle, error) {
	return devloop.RebuildHandle{}, nil
}

func (r *shutdownTrackingRunner) WaitForRebuild(
	ctx context.Context,
	workDir string,
	request devloop.WaitForRebuildRequest,
) (devloop.RebuildResult, error) {
	return devloop.RebuildResult{}, nil
}

func (r *shutdownTrackingRunner) Stop(
	ctx context.Context,
	workDir string,
	contextName string,
) (devloop.StopResult, error) {
	r.stopCalled = true
	return devloop.StopResult{}, nil
}

func TestShutdownPreservesDetachedDevLoop(t *testing.T) {
	runner := &shutdownTrackingRunner{}
	server := &Server{
		devLoopRunner:       runner,
		delegatedDevWorkDir: t.TempDir(),
		delegatedDevContext: "default",
	}

	server.Shutdown()

	if runner.stopCalled {
		t.Fatal("Shutdown stopped a detached dev loop owned by the canonical CLI")
	}
}
