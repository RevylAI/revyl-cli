package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

func newSandboxUnitCmd(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "revyl"}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("dev", false, "")
	cmd := &cobra.Command{Use: "sandbox"}
	root.AddCommand(cmd)
	return cmd
}

func TestGetClaimedSandboxNamedSingleSandboxRespectsTargetName(t *testing.T) {
	orig := getClaimedSandboxesFn
	t.Cleanup(func() { getClaimedSandboxesFn = orig })

	getClaimedSandboxesFn = func(cmd *cobra.Command) ([]api.FleetSandbox, error) {
		return []api.FleetSandbox{
			{Id: "1", VmName: "sandbox-1"},
		}, nil
	}

	cmd := newSandboxUnitCmd(t)
	_, err := getClaimedSandboxNamed(cmd, "sandbox-2")
	if err == nil {
		t.Fatalf("expected mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestGetClaimedSandboxNamedSingleSandboxMatch(t *testing.T) {
	orig := getClaimedSandboxesFn
	t.Cleanup(func() { getClaimedSandboxesFn = orig })

	getClaimedSandboxesFn = func(cmd *cobra.Command) ([]api.FleetSandbox, error) {
		return []api.FleetSandbox{
			{Id: "1", VmName: "sandbox-1"},
		}, nil
	}

	cmd := newSandboxUnitCmd(t)
	sb, err := getClaimedSandboxNamed(cmd, "SANDBOX-1")
	if err != nil {
		t.Fatalf("expected match, got error: %v", err)
	}
	if sb == nil || sb.VmName != "sandbox-1" {
		t.Fatalf("unexpected sandbox: %+v", sb)
	}
}

func TestResolveSandboxWorktreePathInRepoUsesRepoOverride(t *testing.T) {
	orig := listSandboxWorktreesFn
	t.Cleanup(func() { listSandboxWorktreesFn = orig })

	var gotRepo string
	listSandboxWorktreesFn = func(sandbox *api.FleetSandbox, repoOverride string) ([]worktreeInfo, error) {
		gotRepo = repoOverride
		return []worktreeInfo{
			{Branch: "feature-x", Path: "/tmp/workspace/repo-a/feature-x"},
		}, nil
	}

	sb := &api.FleetSandbox{Id: "1", VmName: "sandbox-1"}
	path, err := resolveSandboxWorktreePathInRepo(sb, "feature-x", "repo-a")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if gotRepo != "repo-a" {
		t.Fatalf("expected repo override repo-a, got %q", gotRepo)
	}
	if path != "/tmp/workspace/repo-a/feature-x" {
		t.Fatalf("unexpected path: %s", path)
	}
}

func TestRunSandboxWorktreeRemoveRequiresRepo(t *testing.T) {
	origRepo := worktreeRemoveRepo
	origForce := worktreeRemoveForce
	t.Cleanup(func() {
		worktreeRemoveRepo = origRepo
		worktreeRemoveForce = origForce
	})

	worktreeRemoveRepo = ""
	worktreeRemoveForce = true

	cmd := newSandboxUnitCmd(t)
	err := runSandboxWorktreeRemove(cmd, []string{"feature-x"})
	if err == nil || !strings.Contains(err.Error(), "--repo is required") {
		t.Fatalf("expected --repo required error, got: %v", err)
	}
}

func TestRunSandboxWorktreeSetupRequiresRepo(t *testing.T) {
	origRepo := worktreeSetupRepo
	t.Cleanup(func() { worktreeSetupRepo = origRepo })

	worktreeSetupRepo = ""

	cmd := newSandboxUnitCmd(t)
	err := runSandboxWorktreeSetup(cmd, []string{"feature-x"})
	if err == nil || !strings.Contains(err.Error(), "--repo is required") {
		t.Fatalf("expected --repo required error, got: %v", err)
	}
}

func TestRunSandboxOpenRequiresRepo(t *testing.T) {
	origRepo := openRepo
	t.Cleanup(func() { openRepo = origRepo })

	openRepo = ""

	cmd := newSandboxUnitCmd(t)
	err := runSandboxOpen(cmd, []string{"feature-x"})
	if err == nil || !strings.Contains(err.Error(), "--repo is required") {
		t.Fatalf("expected --repo required error, got: %v", err)
	}
}

func TestRunSandboxWorktreeRemoveUsesRepoScopedScript(t *testing.T) {
	origRepo := worktreeRemoveRepo
	origForce := worktreeRemoveForce
	origSSHExec := sandboxSSHExec
	origSandboxes := getClaimedSandboxesFn
	t.Cleanup(func() {
		worktreeRemoveRepo = origRepo
		worktreeRemoveForce = origForce
		sandboxSSHExec = origSSHExec
		getClaimedSandboxesFn = origSandboxes
	})

	worktreeRemoveRepo = "repo-a"
	worktreeRemoveForce = true
	getClaimedSandboxesFn = func(cmd *cobra.Command) ([]api.FleetSandbox, error) {
		return []api.FleetSandbox{{Id: "1", VmName: "sandbox-1"}}, nil
	}

	var sshScript string
	sandboxSSHExec = func(sandbox *api.FleetSandbox, command string) (string, error) {
		sshScript = command
		return "ok", nil
	}

	cmd := newSandboxUnitCmd(t)
	_ = cmd.Root().PersistentFlags().Set("json", "true")
	if err := runSandboxWorktreeRemove(cmd, []string{"feature-x"}); err != nil {
		t.Fatalf("expected remove success, got error: %v", err)
	}

	if !strings.Contains(sshScript, "REPO_OVERRIDE='repo-a'") {
		t.Fatalf("expected repo override in ssh script, got:\n%s", sshScript)
	}
	if strings.Contains(sshScript, "wtrm ") {
		t.Fatalf("expected no branch-only wtrm call, got:\n%s", sshScript)
	}
}
