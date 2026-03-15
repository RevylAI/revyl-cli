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

	cfg := &config.ProjectConfig{Tests: map[string]string{"login-flow": "missing-id"}}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

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

	cfg := &config.ProjectConfig{Tests: map[string]string{"login-flow": "invalid-id"}}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

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

	cfg := &config.ProjectConfig{Tests: map[string]string{"login-flow": "forbidden-id"}}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

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

	cfg := &config.ProjectConfig{Tests: map[string]string{"login-flow": "stale-id"}}
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
		if r.URL.Path != "/api/v1/tests/create" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode create request: %v", err)
		}
		if got := req["org_id"]; got != "org-config" {
			t.Fatalf("org_id = %v, want org-config", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"remote-id","version":2}`))
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
		Tests:   map[string]string{},
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
		switch r.URL.Path {
		case "/api/v1/tests/get_test_by_id/remote-id":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"remote-id","name":"Checkout Flow","platform":"ios","tasks":[],"version":5}`))
		case "/api/v1/tests/tags/tests/remote-id":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	defer cleanup()

	cfg := &config.ProjectConfig{Tests: map[string]string{}}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

	results, err := resolver.ImportRemoteTest(context.Background(), "remote-id", "Checkout Flow", testsDir, false)
	if err != nil {
		t.Fatalf("ImportRemoteTest() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if got := cfg.Tests["checkout-flow"]; got != "remote-id" {
		t.Fatalf("cfg.Tests[checkout-flow] = %q, want remote-id", got)
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
	if len(cfg.Tests) != 1 {
		t.Fatalf("len(cfg.Tests) = %d, want 1", len(cfg.Tests))
	}
	if _, exists := cfg.Tests["checkout-flow-2"]; exists {
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

	cfg := &config.ProjectConfig{Tests: map[string]string{}}
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
