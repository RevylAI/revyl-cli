package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func newResolverTestClient(t *testing.T, handler http.HandlerFunc) (*api.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	return api.NewClientWithBaseURL("test-key", srv.URL), srv.Close
}

func TestGetTestStatus_OrphanedMissing(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_test_by_id/missing-id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"remote test not found"}`))
	})
	defer cleanup()

	cfg := &config.ProjectConfig{}
	localTests := map[string]*config.LocalTest{
		"login-flow": {Meta: config.TestMeta{RemoteID: "missing-id"}},
	}
	resolver := NewResolver(client, cfg, localTests)

	status, err := resolver.getTestStatus(context.Background(), "login-flow")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusOrphaned {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusOrphaned.String())
	}
	if status.LinkIssue != RemoteLinkIssueMissing {
		t.Fatalf("link issue = %s, want %s", status.LinkIssue, RemoteLinkIssueMissing)
	}
}

func TestGetTestStatus_OrphanedInvalidID(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_test_by_id/invalid-id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"Invalid test ID format"}`))
	})
	defer cleanup()

	cfg := &config.ProjectConfig{}
	localTests := map[string]*config.LocalTest{
		"login-flow": {Meta: config.TestMeta{RemoteID: "invalid-id"}},
	}
	resolver := NewResolver(client, cfg, localTests)

	status, err := resolver.getTestStatus(context.Background(), "login-flow")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusOrphaned {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusOrphaned.String())
	}
	if status.LinkIssue != RemoteLinkIssueInvalidID {
		t.Fatalf("link issue = %s, want %s", status.LinkIssue, RemoteLinkIssueInvalidID)
	}
}

func TestGetTestStatus_OrphanedForbidden(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_test_by_id/forbidden-id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Not authorized to access this test"}`))
	})
	defer cleanup()

	cfg := &config.ProjectConfig{}
	localTests := map[string]*config.LocalTest{
		"login-flow": {Meta: config.TestMeta{RemoteID: "forbidden-id"}},
	}
	resolver := NewResolver(client, cfg, localTests)

	status, err := resolver.getTestStatus(context.Background(), "login-flow")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusOrphaned {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusOrphaned.String())
	}
	if status.LinkIssue != RemoteLinkIssueForbidden {
		t.Fatalf("link issue = %s, want %s", status.LinkIssue, RemoteLinkIssueForbidden)
	}
}

func TestGetTestStatus_FallbackToLocalRemoteID(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/get_test_by_id/stale-id":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
		case "/api/v1/tests/get_test_by_id/live-id":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"live-id","name":"Login","platform":"ios","tasks":[],"version":3}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "live-id",
			RemoteVersion: 3,
			LocalVersion:  3,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login", Platform: "ios"},
			Blocks:   []config.TestBlock{},
		},
	}

	cfg := &config.ProjectConfig{}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{"login-flow": local})

	status, err := resolver.getTestStatus(context.Background(), "login-flow")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusSynced {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusSynced.String())
	}
	if status.RemoteID != "live-id" {
		t.Fatalf("remote id = %s, want live-id", status.RemoteID)
	}
	if status.LinkIssue != RemoteLinkIssueNone {
		t.Fatalf("link issue = %s, want none", status.LinkIssue)
	}
}

func TestSyncToRemote_CreateUsesResolvedOrgID(t *testing.T) {
	testsDir := t.TempDir()

	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/create":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if got := req["org_id"]; got != "org-config" {
				t.Fatalf("org_id = %v, want org-config", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"remote-id","version":2}`))
		case "/api/v1/variables/custom/delete_all",
			"/api/v1/variables/app_launch_env/delete_all":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":"deleted all"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login", Platform: "ios"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: "Tap login"},
			},
		},
	}

	cfg := &config.ProjectConfig{
		Project: config.Project{OrgID: "org-config"},
	}

	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{
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
		t.Fatalf("results[0].Error = %v, want nil", results[0].Error)
	}
	if local.Meta.RemoteID != "remote-id" {
		t.Fatalf("local.Meta.RemoteID = %q, want remote-id", local.Meta.RemoteID)
	}
}

func TestImportRemoteTest_ReusesExistingAliasForSameRemoteID(t *testing.T) {
	testsDir := t.TempDir()

	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/tests/get_test_by_id/remote-id":
			_, _ = w.Write([]byte(`{"id":"remote-id","name":"Checkout Flow","platform":"ios","tasks":[],"version":5}`))
		case r.URL.Path == "/api/v1/tests/tags/tests/remote-id":
			_, _ = w.Write([]byte(`[]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/variables/custom/read_variables"),
			strings.HasPrefix(r.URL.Path, "/api/v1/variables/app_launch_env/read"):
			_, _ = w.Write([]byte(`{"result":[]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/scripts"):
			_, _ = w.Write([]byte(`{"scripts":[],"count":0}`))
		case r.URL.Path == "/api/v1/modules/list":
			_, _ = w.Write([]byte(`{"message":"ok","result":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	defer cleanup()

	cfg := &config.ProjectConfig{}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

	results, err := resolver.ImportRemoteTest(context.Background(), "remote-id", "Checkout Flow", testsDir, false)
	if err != nil {
		t.Fatalf("ImportRemoteTest() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	gotID, _ := config.GetLocalTestRemoteID(testsDir, "checkout-flow")
	if gotID != "remote-id" {
		t.Fatalf("checkout-flow remote_id = %q, want remote-id", gotID)
	}

	results, err = resolver.ImportRemoteTest(context.Background(), "remote-id", "Checkout Flow", testsDir, false)
	if err != nil {
		t.Fatalf("second ImportRemoteTest() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Name != "checkout-flow" {
		t.Fatalf("results[0].Name = %q, want checkout-flow", results[0].Name)
	}
	aliases := config.ListLocalTestAliases(testsDir)
	if len(aliases) != 1 {
		t.Fatalf("len(aliases) = %d, want 1", len(aliases))
	}
	if _, err := config.LoadLocalTest(filepath.Join(testsDir, "checkout-flow-2.yaml")); err == nil {
		t.Fatalf("unexpected collision alias checkout-flow-2 created")
	}
}

func TestPullRemoteTest_DoesNotOverwriteLocalFileWhenTaskParsingFails(t *testing.T) {
	testsDir := t.TempDir()
	localPath := filepath.Join(testsDir, "login-flow.yaml")

	existing := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "remote-id",
			RemoteVersion: 4,
			LocalVersion:  4,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login", Platform: "ios"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: "Keep existing block"},
			},
		},
	}
	if err := config.SaveLocalTest(localPath, existing); err != nil {
		t.Fatalf("SaveLocalTest() error = %v", err)
	}

	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_test_by_id/remote-id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"remote-id","name":"Login","platform":"ios","tasks":{"type":"instructions"},"version":5}`))
	})
	defer cleanup()

	cfg := &config.ProjectConfig{}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

	result := resolver.pullRemoteTest(
		context.Background(),
		"login-flow",
		"remote-id",
		testsDir,
		true,
	)
	if result.Error == nil {
		t.Fatalf("result.Error = nil, want parse error")
	}
	if !strings.Contains(result.Error.Error(), "parse remote test blocks") {
		t.Fatalf("result.Error = %v, want parse remote test blocks", result.Error)
	}

	localAfter, err := config.LoadLocalTest(localPath)
	if err != nil {
		t.Fatalf("LoadLocalTest() error = %v", err)
	}
	if len(localAfter.Test.Blocks) != 1 {
		t.Fatalf("len(localAfter.Test.Blocks) = %d, want 1", len(localAfter.Test.Blocks))
	}
	if got := localAfter.Test.Blocks[0].StepDescription; got != "Keep existing block" {
		t.Fatalf("local block description = %q, want existing content preserved", got)
	}
}

func TestFallbackTestAlias_SanitizesPathSeparators(t *testing.T) {
	got := fallbackTestAlias("  ab/cd\\ef?gh  ")
	if got != "test-abcdefgh" {
		t.Fatalf("fallbackTestAlias() = %q, want %q", got, "test-abcdefgh")
	}
}

func TestFallbackTestAlias_UsesImportWhenSanitizedEmpty(t *testing.T) {
	got := fallbackTestAlias(" /\\? ")
	if got != "test-import" {
		t.Fatalf("fallbackTestAlias() = %q, want %q", got, "test-import")
	}
}

func TestGetTestStatus_Modified(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"remote-1","name":"Login","platform":"ios","tasks":[],"version":2}`))
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "remote-1",
			RemoteVersion: 2,
			LocalVersion:  2,
			Checksum:      "stale-checksum",
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login - edited", Platform: "ios"},
			Blocks:   []config.TestBlock{{Type: "instructions", StepDescription: "New step"}},
		},
	}

	resolver := NewResolver(client, &config.ProjectConfig{}, map[string]*config.LocalTest{"login": local})

	status, err := resolver.getTestStatus(context.Background(), "login")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusModified {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusModified.String())
	}
}

func TestGetTestStatus_Outdated(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"remote-1","name":"Login","platform":"ios","tasks":[],"version":5}`))
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "remote-1",
			RemoteVersion: 3,
			LocalVersion:  3,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login", Platform: "ios"},
			Blocks:   []config.TestBlock{},
		},
	}
	local.Meta.Checksum = config.ComputeTestChecksum(&local.Test)

	resolver := NewResolver(client, &config.ProjectConfig{}, map[string]*config.LocalTest{"login": local})

	status, err := resolver.getTestStatus(context.Background(), "login")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusOutdated {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusOutdated.String())
	}
	if status.RemoteVersion != 5 {
		t.Fatalf("remote version = %d, want 5", status.RemoteVersion)
	}
}

func TestGetTestStatus_Conflict(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"remote-1","name":"Login","platform":"ios","tasks":[],"version":5}`))
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "remote-1",
			RemoteVersion: 3,
			LocalVersion:  3,
			Checksum:      "stale-checksum",
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login - edited locally", Platform: "ios"},
			Blocks:   []config.TestBlock{{Type: "instructions", StepDescription: "Local change"}},
		},
	}

	resolver := NewResolver(client, &config.ProjectConfig{}, map[string]*config.LocalTest{"login": local})

	status, err := resolver.getTestStatus(context.Background(), "login")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusConflict {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusConflict.String())
	}
}

func TestGetTestStatus_LocalOnly(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("should not make API call for local-only test")
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Draft Test", Platform: "android"},
			Blocks:   []config.TestBlock{{Type: "instructions", StepDescription: "Tap login"}},
		},
	}

	resolver := NewResolver(client, &config.ProjectConfig{}, map[string]*config.LocalTest{"draft": local})

	status, err := resolver.getTestStatus(context.Background(), "draft")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusLocalOnly {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusLocalOnly.String())
	}
}

func TestSyncToRemote_UpdateExistingTest(t *testing.T) {
	testsDir := t.TempDir()
	updatedVersion := 0

	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/update"):
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if int(req["expected_version"].(float64)) != 3 {
				t.Fatalf("expected_version = %v, want 3", req["expected_version"])
			}
			updatedVersion = 4
			_, _ = w.Write([]byte(`{"id":"existing-id","version":4}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/variables/custom/delete_all"),
			strings.HasPrefix(r.URL.Path, "/api/v1/variables/app_launch_env/delete_all"):
			_, _ = w.Write([]byte(`{"message":"ok"}`))
		default:
			t.Fatalf("unexpected path: %s %s", r.Method, r.URL.Path)
		}
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "existing-id",
			RemoteVersion: 3,
			LocalVersion:  3,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login", Platform: "ios"},
			Blocks:   []config.TestBlock{{Type: "instructions", StepDescription: "Tap login"}},
		},
	}

	resolver := NewResolver(client, &config.ProjectConfig{}, map[string]*config.LocalTest{"login": local})

	results, err := resolver.SyncToRemote(context.Background(), "login", testsDir, false)
	if err != nil {
		t.Fatalf("SyncToRemote() error = %v", err)
	}
	if results[0].Error != nil {
		t.Fatalf("result error = %v", results[0].Error)
	}
	if updatedVersion != 4 {
		t.Fatalf("server was not called with update")
	}
	if local.Meta.RemoteVersion != 4 {
		t.Fatalf("local remote_version = %d, want 4", local.Meta.RemoteVersion)
	}
}

func TestSyncToRemote_VersionConflict(t *testing.T) {
	testsDir := t.TempDir()

	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/update"):
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"version conflict"}`))
		default:
			t.Fatalf("unexpected path: %s %s", r.Method, r.URL.Path)
		}
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "existing-id",
			RemoteVersion: 3,
			LocalVersion:  3,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login", Platform: "ios"},
			Blocks:   []config.TestBlock{{Type: "instructions", StepDescription: "Tap login"}},
		},
	}

	resolver := NewResolver(client, &config.ProjectConfig{}, map[string]*config.LocalTest{"login": local})

	results, err := resolver.SyncToRemote(context.Background(), "login", testsDir, false)
	if err != nil {
		t.Fatalf("SyncToRemote() error = %v", err)
	}
	if !results[0].Conflict {
		t.Fatal("expected Conflict = true")
	}
	if results[0].Error != nil {
		t.Fatalf("expected Error = nil for conflict, got %v", results[0].Error)
	}
}

func TestResolveBlockNames(t *testing.T) {
	blocks := []config.TestBlock{
		{Type: "code_execution", StepDescription: "script-uuid-1"},
		{Type: "module_import", ModuleID: "module-uuid-1"},
		{Type: "instructions", StepDescription: "Tap login"},
		{
			Type:      "if",
			Condition: "is visible?",
			Then: []config.TestBlock{
				{Type: "code_execution", StepDescription: "script-uuid-2"},
			},
		},
	}

	scriptNames := map[string]string{
		"script-uuid-1": "validate-auth",
		"script-uuid-2": "cleanup-session",
	}
	moduleNames := map[string]string{
		"module-uuid-1": "login-module",
	}

	resolveBlockNames(blocks, scriptNames, moduleNames)

	if blocks[0].Script != "validate-auth" {
		t.Fatalf("blocks[0].Script = %q, want validate-auth", blocks[0].Script)
	}
	if blocks[0].StepDescription != "" {
		t.Fatalf("blocks[0].StepDescription should be cleared, got %q", blocks[0].StepDescription)
	}
	if blocks[1].Module != "login-module" {
		t.Fatalf("blocks[1].Module = %q, want login-module", blocks[1].Module)
	}
	if blocks[1].ModuleID != "" {
		t.Fatalf("blocks[1].ModuleID should be cleared, got %q", blocks[1].ModuleID)
	}
	if blocks[2].StepDescription != "Tap login" {
		t.Fatalf("blocks[2].StepDescription = %q, want Tap login", blocks[2].StepDescription)
	}
	if blocks[3].Then[0].Script != "cleanup-session" {
		t.Fatalf("nested block Script = %q, want cleanup-session", blocks[3].Then[0].Script)
	}
}
