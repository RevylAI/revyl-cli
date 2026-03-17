package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sync/testutil"
)

func TestIntegrationSyncPushCreatesRemoteTest(t *testing.T) {
	mock := testutil.NewMockServer()
	defer mock.Close()

	testsDir := t.TempDir()

	local := &config.LocalTest{
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login Flow", Platform: "ios"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: "Tap the login button"},
			},
		},
	}

	cfg := &config.ProjectConfig{
		Project: config.Project{OrgID: "org-test"},
	}

	resolver := NewResolver(mock.Client, cfg, map[string]*config.LocalTest{
		"login-flow": local,
	})

	results, err := resolver.SyncToRemote(context.Background(), "login-flow", testsDir, false)
	if err != nil {
		t.Fatalf("SyncToRemote() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("results[0].Error = %v", results[0].Error)
	}

	if !mock.HasCall("POST", "/api/v1/tests/create") {
		t.Fatal("expected POST /api/v1/tests/create call")
	}

	if local.Meta.RemoteID == "" {
		t.Fatal("local.Meta.RemoteID is empty after push; expected mock-assigned ID")
	}

	saved, err := config.LoadLocalTest(filepath.Join(testsDir, "login-flow.yaml"))
	if err != nil {
		t.Fatalf("LoadLocalTest() error = %v", err)
	}
	if saved.Meta.RemoteID != local.Meta.RemoteID {
		t.Fatalf("saved remote_id = %q, want %q", saved.Meta.RemoteID, local.Meta.RemoteID)
	}
	if saved.Meta.RemoteVersion != 1 {
		t.Fatalf("saved remote_version = %d, want 1", saved.Meta.RemoteVersion)
	}
}

func TestIntegrationSyncPullUpdatesLocalTest(t *testing.T) {
	mock := testutil.NewMockServer()
	defer mock.Close()

	mock.SeedTest(testutil.MockTest{
		ID:       "remote-pull-1",
		Name:     "Login Flow v2",
		Version:  5,
		Platform: "ios",
		Tasks: []interface{}{
			map[string]interface{}{
				"type":             "instructions",
				"step_description": "Tap the new login button",
			},
		},
	})

	testsDir := t.TempDir()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "remote-pull-1",
			RemoteVersion: 3,
			LocalVersion:  3,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login Flow", Platform: "ios"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: "Tap login"},
			},
		},
	}
	local.Meta.Checksum = config.ComputeTestChecksum(&local.Test)

	resolver := NewResolver(mock.Client, &config.ProjectConfig{}, map[string]*config.LocalTest{
		"login-flow": local,
	})

	results, err := resolver.PullFromRemote(context.Background(), "login-flow", testsDir, false)
	if err != nil {
		t.Fatalf("PullFromRemote() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("results[0].Error = %v", results[0].Error)
	}
	if results[0].NewVersion != 5 {
		t.Fatalf("results[0].NewVersion = %d, want 5", results[0].NewVersion)
	}

	saved, err := config.LoadLocalTest(filepath.Join(testsDir, "login-flow.yaml"))
	if err != nil {
		t.Fatalf("LoadLocalTest() error = %v", err)
	}
	if saved.Meta.RemoteVersion != 5 {
		t.Fatalf("saved remote_version = %d, want 5", saved.Meta.RemoteVersion)
	}
	if saved.Test.Metadata.Name != "Login Flow v2" {
		t.Fatalf("saved name = %q, want %q", saved.Test.Metadata.Name, "Login Flow v2")
	}
}

func TestIntegrationSyncStatusDetectsConflict(t *testing.T) {
	mock := testutil.NewMockServer()
	defer mock.Close()

	mock.SeedTest(testutil.MockTest{
		ID:       "remote-conflict-1",
		Name:     "Login Flow",
		Version:  5,
		Platform: "ios",
		Tasks:    []interface{}{},
	})

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "remote-conflict-1",
			RemoteVersion: 3,
			LocalVersion:  3,
			Checksum:      "stale-checksum-forces-local-changes-detection",
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login Flow (edited)", Platform: "ios"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: "locally modified step"},
			},
		},
	}

	resolver := NewResolver(mock.Client, &config.ProjectConfig{}, map[string]*config.LocalTest{
		"login-flow": local,
	})

	statuses, err := resolver.GetAllStatuses(context.Background())
	if err != nil {
		t.Fatalf("GetAllStatuses() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].Status != StatusConflict {
		t.Fatalf("status = %s, want %s", statuses[0].Status.String(), StatusConflict.String())
	}
	if statuses[0].LocalVersion != 3 {
		t.Fatalf("local_version = %d, want 3", statuses[0].LocalVersion)
	}
	if statuses[0].RemoteVersion != 5 {
		t.Fatalf("remote_version = %d, want 5", statuses[0].RemoteVersion)
	}
}

func TestIntegrationSyncStatusDetectsOrphaned(t *testing.T) {
	mock := testutil.NewMockServer()
	defer mock.Close()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "deleted-remote-id",
			RemoteVersion: 2,
			LocalVersion:  2,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Removed Test", Platform: "android"},
			Blocks:   []config.TestBlock{},
		},
	}

	resolver := NewResolver(mock.Client, &config.ProjectConfig{}, map[string]*config.LocalTest{
		"removed-test": local,
	})

	statuses, err := resolver.GetAllStatuses(context.Background())
	if err != nil {
		t.Fatalf("GetAllStatuses() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].Status != StatusOrphaned {
		t.Fatalf("status = %s, want %s", statuses[0].Status.String(), StatusOrphaned.String())
	}
	if statuses[0].LinkIssue != RemoteLinkIssueMissing {
		t.Fatalf("link_issue = %s, want %s", statuses[0].LinkIssue, RemoteLinkIssueMissing)
	}

	if !mock.HasCall("GET", "/api/v1/tests/get_test_by_id/deleted-remote-id") {
		t.Fatal("expected GET /api/v1/tests/get_test_by_id/deleted-remote-id call")
	}
}

func TestIntegrationSyncImportCreatesLocalFile(t *testing.T) {
	mock := testutil.NewMockServer()
	defer mock.Close()

	mock.SeedTest(testutil.MockTest{
		ID:       "remote-import-1",
		Name:     "Checkout Flow",
		Version:  3,
		Platform: "android",
		Tasks: []interface{}{
			map[string]interface{}{
				"type":             "instructions",
				"step_description": "Add item to cart",
			},
			map[string]interface{}{
				"type":             "instructions",
				"step_description": "Tap checkout",
			},
		},
	})

	testsDir := t.TempDir()

	resolver := NewResolver(mock.Client, &config.ProjectConfig{}, map[string]*config.LocalTest{})

	results, err := resolver.ImportRemoteTest(
		context.Background(), "remote-import-1", "", testsDir, false,
	)
	if err != nil {
		t.Fatalf("ImportRemoteTest() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("results[0].Error = %v", results[0].Error)
	}
	if results[0].NewVersion != 3 {
		t.Fatalf("results[0].NewVersion = %d, want 3", results[0].NewVersion)
	}

	saved, err := config.LoadLocalTest(filepath.Join(testsDir, "checkout-flow.yaml"))
	if err != nil {
		t.Fatalf("LoadLocalTest() error = %v", err)
	}
	if saved.Meta.RemoteID != "remote-import-1" {
		t.Fatalf("saved remote_id = %q, want %q", saved.Meta.RemoteID, "remote-import-1")
	}
	if saved.Meta.RemoteVersion != 3 {
		t.Fatalf("saved remote_version = %d, want 3", saved.Meta.RemoteVersion)
	}
	if saved.Test.Metadata.Name != "Checkout Flow" {
		t.Fatalf("saved name = %q, want %q", saved.Test.Metadata.Name, "Checkout Flow")
	}
	if saved.Test.Metadata.Platform != "android" {
		t.Fatalf("saved platform = %q, want %q", saved.Test.Metadata.Platform, "android")
	}
	if len(saved.Test.Blocks) != 2 {
		t.Fatalf("len(saved.Test.Blocks) = %d, want 2", len(saved.Test.Blocks))
	}
}

func TestIntegrationDryRunPushDoesNotCallAPI(t *testing.T) {
	mock := testutil.NewMockServer()
	defer mock.Close()

	local := &config.LocalTest{
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Dry Run Push", Platform: "android"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: "Open settings"},
			},
		},
	}

	cfg := &config.ProjectConfig{
		Project: config.Project{OrgID: "org-test"},
	}

	resolver := NewResolver(mock.Client, cfg, map[string]*config.LocalTest{
		"dry-run-push": local,
	})

	statuses, err := resolver.GetAllStatuses(context.Background())
	if err != nil {
		t.Fatalf("GetAllStatuses() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].Status != StatusLocalOnly {
		t.Fatalf("status = %s, want %s", statuses[0].Status.String(), StatusLocalOnly.String())
	}

	if mock.HasCall("POST", "/api/v1/tests/create") {
		t.Fatal("GetAllStatuses must not call POST /api/v1/tests/create")
	}
	if mock.HasCall("PUT", "/api/v1/tests/update") {
		t.Fatal("GetAllStatuses must not call PUT /api/v1/tests/update")
	}
}

func TestIntegrationDryRunPullDoesNotWriteFiles(t *testing.T) {
	mock := testutil.NewMockServer()
	defer mock.Close()

	mock.SeedTest(testutil.MockTest{
		ID:       "remote-dryrun-1",
		Name:     "Settings Flow v3",
		Version:  7,
		Platform: "ios",
		Tasks: []interface{}{
			map[string]interface{}{
				"type":             "instructions",
				"step_description": "Tap the new settings icon",
			},
		},
	})

	testsDir := t.TempDir()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "remote-dryrun-1",
			RemoteVersion: 3,
			LocalVersion:  3,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Settings Flow", Platform: "ios"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: "Open settings"},
			},
		},
	}
	local.Meta.Checksum = config.ComputeTestChecksum(&local.Test)

	localPath := filepath.Join(testsDir, "settings-flow.yaml")
	if err := config.SaveLocalTest(localPath, local); err != nil {
		t.Fatalf("SaveLocalTest() setup error = %v", err)
	}

	contentBefore, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("ReadFile() setup error = %v", err)
	}

	resolver := NewResolver(mock.Client, &config.ProjectConfig{}, map[string]*config.LocalTest{
		"settings-flow": local,
	})

	statuses, err := resolver.GetAllStatuses(context.Background())
	if err != nil {
		t.Fatalf("GetAllStatuses() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].Status != StatusOutdated {
		t.Fatalf("status = %s, want %s", statuses[0].Status.String(), StatusOutdated.String())
	}

	contentAfter, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("ReadFile() after error = %v", err)
	}
	if string(contentBefore) != string(contentAfter) {
		t.Fatal("GetAllStatuses must not modify the local test file")
	}

	if mock.HasCall("POST", "/api/v1/tests/create") {
		t.Fatal("GetAllStatuses must not call POST /api/v1/tests/create")
	}
	if mock.HasCall("PUT", "/api/v1/tests/update") {
		t.Fatal("GetAllStatuses must not call PUT /api/v1/tests/update")
	}
}
