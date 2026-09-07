package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/testutil"
	"github.com/spf13/cobra"
)

func TestLegacyDetachedRuntimeHelper(t *testing.T) {
	root := os.Getenv("REVYL_TEST_DETACHED_RUNTIME_ROOT")
	if root == "" || !isDetachedDevChild() {
		return
	}
	ctx, err := loadDevContext(root, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	ctx.PID = os.Getpid()
	ctx.StartedAtNano = time.Now().UnixNano()
	ctx.SessionID = "fixture-session"
	ctx.State = devContextStateRunning
	if err := writeDevCtxPIDFile(devCtxPIDPath(root, ctx.Name), ctx.PID, ctx.StartedAtNano); err != nil {
		t.Fatal(err)
	}
	if err := writeDevStatusFile(devCtxStatusPath(root, ctx.Name), []byte(`{"state":"idle","session_id":"fixture-session"}`)); err != nil {
		t.Fatal(err)
	}
	fmt.Println("fixture dev loop ready")
	if err := saveDevContext(root, ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("parent did not release fixture dev loop")
		case <-poll.C:
			if _, err := os.Stat(filepath.Join(root, "release-helper")); err == nil {
				return
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
}

func TestLegacyDevRuntimeUpgradeAcrossDetachedLifecycle(t *testing.T) {
	root := setupTestRepo(t)
	contextPath := filepath.Join(devCtxDir(root, "legacy"), devContextMetaFile)
	for path, contents := range map[string]string{
		contextPath:                               `{"name":"legacy","platform":"android","profile":"development","state":"stopped"}`,
		devCtxPIDPath(root, "legacy").String():    "12345",
		devCtxStatusPath(root, "legacy").String(): `{"state":"idle","session_id":"old-session"}`,
		devDetachLogPath(root).String():           "legacy detach log\n",
		filepath.Join(root, ".revyl", devContextsDir, devContextCurrentFile): "legacy\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	createTestContext(t, root, &DevContext{Name: "other", Platform: "ios"})
	for _, name := range []string{"other", "legacy"} {
		if err := setCurrentDevContext(root, name); err != nil {
			t.Fatal(err)
		}
		if current, err := readCurrentDevContext(root); err != nil || current != name {
			t.Fatalf("current context = %q, error=%v", current, err)
		}
	}
	t.Setenv("REVYL_TEST_DETACHED_RUNTIME_ROOT", root)
	t.Setenv("CI", "true")
	previousJSON := devStartJSON
	devStartJSON = true
	t.Cleanup(func() { devStartJSON = previousJSON })
	var childPID int
	var childNonce int64
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(root, "release-helper"), nil, 0o600)
		deadline := time.Now().Add(2 * time.Second)
		for childPID != 0 {
			alive, _ := isDevCtxProcessAlive(childPID, childNonce, devCtxPIDPath(root, "legacy"))
			if !alive {
				break
			}
			if time.Now().After(deadline) {
				t.Error("fixture dev loop did not terminate during cleanup")
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	var spawnErr error
	output := captureStdout(t, func() {
		spawnErr = spawnDetachedDevLoopWithArgs(cmd, root, []string{"-test.run=^TestLegacyDetachedRuntimeHelper$", "--"})
	})
	if spawnErr != nil {
		t.Fatalf("detach upgrade: %v\n%s", spawnErr, output)
	}
	var handshake devDetachHandshake
	if err := json.Unmarshal([]byte(output), &handshake); err != nil {
		t.Fatalf("invalid detach JSON: %v\n%s", err, output)
	}
	childPID = handshake.PID
	runtimeContext, err := loadDevContext(root, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	childNonce = runtimeContext.StartedAtNano
	if handshake.Context != "legacy" || handshake.SessionID != "fixture-session" || handshake.OpenedBrowser {
		t.Fatalf("unexpected handshake: %+v", handshake)
	}
	if status := collectDevStatusOutput(root, "legacy"); !devStatusOutputReady(status) {
		t.Fatalf("upgraded context is not ready: %v", status)
	}
	log, err := readManagedRuntimeFile(devDetachLogPath(root))
	if err != nil || !strings.HasPrefix(string(log), "legacy detach log\n") || !strings.Contains(string(log), "fixture dev loop ready") {
		t.Fatalf("detach log did not preserve legacy and new output: %q, error=%v", log, err)
	}
	statusPath := devCtxStatusPath(root, "legacy")
	writeDevStatusRebuildStarted(statusPath, &mcppkg.DeviceSession{SessionID: "fixture-session"}, "", "", "", "", "android", 2, true, "android")
	status := readDevStatusSnapshot(statusPath)
	if status == nil || status.State != "building" || status.LastRebuild == nil || status.LastRebuild.Seq != 2 {
		t.Fatalf("rebuild status was not persisted: %+v", status)
	}
	for _, path := range []string{contextPath, devCtxPIDPath(root, "legacy").String(), statusPath.String(), devDetachLogPath(root).String()} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		testutil.AssertPOSIXPermissions(t, info, 0o600)
	}
	if err := os.WriteFile(filepath.Join(root, "release-helper"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		alive, err := isDevCtxProcessAlive(childPID, childNonce, devCtxPIDPath(root, "legacy"))
		if err != nil {
			t.Fatal(err)
		}
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture dev loop did not exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := stopOneDevContext(cmd, root, "legacy"); err != nil {
		t.Fatal(err)
	}
	stopped, err := loadDevContext(root, "legacy")
	if err != nil || stopped.State != devContextStateStopped || stopped.SessionID != "" || stopped.PID != 0 {
		t.Fatalf("stop did not persist cleared context: %+v, error=%v", stopped, err)
	}
}
