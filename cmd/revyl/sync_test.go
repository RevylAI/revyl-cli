package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/spf13/cobra"
)

func newSyncDomainTestClient(t *testing.T, handler http.HandlerFunc) (*api.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	return api.NewClientWithBaseURL("test-key", srv.URL), srv.Close
}

func writeTestFile(t *testing.T, testsDir, name, remoteID string) {
	t.Helper()
	lt := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      remoteID,
			RemoteVersion: 2,
			LocalVersion:  2,
			LastSyncedAt:  "2026-01-01T00:00:00Z",
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: name, Platform: "ios"},
			Blocks:   []config.TestBlock{{Type: "instructions", StepDescription: "Open app"}},
		},
	}
	path := filepath.Join(testsDir, name+".yaml")
	if err := config.SaveLocalTest(path, lt); err != nil {
		t.Fatalf("SaveLocalTest() error = %v", err)
	}
}

func mutateTestFileWithoutChecksumRefresh(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	updated := strings.Replace(string(data), "Open app", "Open app (edited locally)", 1)
	if updated == string(data) {
		t.Fatalf("failed to mutate fixture at %s", path)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func TestSyncTestsDomain_LocalOnlyDoesNotPush(t *testing.T) {
	client, cleanup := newSyncDomainTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/tests/get_simple_tests") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tests":[],"count":0}`))
			return
		}
		t.Fatalf("unexpected request: %s", r.URL.Path)
	})
	defer cleanup()

	testsDir := t.TempDir()
	writeTestFile(t, testsDir, "local-only-test", "")

	cfg := &config.ProjectConfig{Tests: map[string]string{}, Workflows: map[string]string{}}
	items, changed, err := syncTestsDomain(context.Background(), client, cfg, testsDir, syncOptions{Prompt: false, Prune: false, DryRun: false})
	if err != nil {
		t.Fatalf("syncTestsDomain() error = %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false")
	}

	found := false
	for _, it := range items {
		if it.Name == "local-only-test" {
			found = true
			if it.Action != "keep-local" {
				t.Fatalf("action = %s, want keep-local", it.Action)
			}
			if it.Error != "" {
				t.Fatalf("unexpected error item: %s", it.Error)
			}
		}
	}
	if !found {
		t.Fatal("expected local-only-test item in sync output")
	}
}

func TestSyncTestsDomain_PruneDetachesOrphanedLink(t *testing.T) {
	client, cleanup := newSyncDomainTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/get_simple_tests"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tests":[],"count":0}`))
		case r.URL.Path == "/api/v1/tests/get_test_by_id/deleted-id":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"remote test not found"}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	})
	defer cleanup()

	testsDir := t.TempDir()
	writeTestFile(t, testsDir, "login-flow", "deleted-id")

	cfg := &config.ProjectConfig{
		Tests:     map[string]string{"login-flow": "deleted-id"},
		Workflows: map[string]string{},
	}

	items, changed, err := syncTestsDomain(context.Background(), client, cfg, testsDir, syncOptions{Prompt: false, Prune: true, DryRun: false})
	if err != nil {
		t.Fatalf("syncTestsDomain() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if _, ok := cfg.Tests["login-flow"]; ok {
		t.Fatal("expected login-flow mapping to be detached")
	}

	loaded, lErr := config.LoadLocalTest(filepath.Join(testsDir, "login-flow.yaml"))
	if lErr != nil {
		t.Fatalf("LoadLocalTest() error = %v", lErr)
	}
	if loaded.Meta.RemoteID != "" {
		t.Fatalf("remote_id = %q, want empty", loaded.Meta.RemoteID)
	}
	if loaded.Meta.RemoteVersion != 0 {
		t.Fatalf("remote_version = %d, want 0", loaded.Meta.RemoteVersion)
	}

	found := false
	for _, it := range items {
		if it.Name == "login-flow" {
			found = true
			if it.Action != "detach" {
				t.Fatalf("action = %s, want detach", it.Action)
			}
			if it.Error != "" {
				t.Fatalf("unexpected error item: %s", it.Error)
			}
		}
	}
	if !found {
		t.Fatal("expected login-flow item in sync output")
	}
}

func TestSyncTestsDomain_PruneKeepsModifiedLocalFileForStaleMapping(t *testing.T) {
	client, cleanup := newSyncDomainTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/get_simple_tests"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tests":[],"count":0}`))
		case r.URL.Path == "/api/v1/tests/get_test_by_id/stale-id":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"stale-id","name":"login-flow","platform":"ios","tasks":[],"version":2}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	})
	defer cleanup()

	testsDir := t.TempDir()
	writeTestFile(t, testsDir, "login-flow", "stale-id")
	testPath := filepath.Join(testsDir, "login-flow.yaml")
	mutateTestFileWithoutChecksumRefresh(t, testPath)

	cfg := &config.ProjectConfig{
		Tests:     map[string]string{"login-flow": "stale-id"},
		Workflows: map[string]string{},
	}

	items, changed, err := syncTestsDomain(context.Background(), client, cfg, testsDir, syncOptions{Prompt: false, Prune: true, DryRun: false})
	if err != nil {
		t.Fatalf("syncTestsDomain() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if _, ok := cfg.Tests["login-flow"]; ok {
		t.Fatal("expected login-flow mapping to be detached")
	}
	if _, statErr := os.Stat(testPath); statErr != nil {
		t.Fatalf("expected modified local test file to remain, stat error: %v", statErr)
	}

	loaded, lErr := config.LoadLocalTest(testPath)
	if lErr != nil {
		t.Fatalf("LoadLocalTest() error = %v", lErr)
	}
	if loaded.Meta.RemoteID != "" {
		t.Fatalf("remote_id = %q, want empty", loaded.Meta.RemoteID)
	}
	if loaded.Meta.RemoteVersion != 0 {
		t.Fatalf("remote_version = %d, want 0", loaded.Meta.RemoteVersion)
	}

	found := false
	for _, it := range items {
		if it.Name == "login-flow" {
			found = true
			if it.Action != "detach" {
				t.Fatalf("action = %s, want detach", it.Action)
			}
			if it.Error != "" {
				t.Fatalf("unexpected error item: %s", it.Error)
			}
		}
	}
	if !found {
		t.Fatal("expected login-flow item in sync output")
	}
}

func TestSyncTestsDomain_PruneAllDeletesUnmodifiedLocalFileForStaleMapping(t *testing.T) {
	client, cleanup := newSyncDomainTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/get_simple_tests"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tests":[],"count":0}`))
		case r.URL.Path == "/api/v1/tests/get_test_by_id/stale-id":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"stale-id","name":"login-flow","platform":"ios","tasks":[],"version":2}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	})
	defer cleanup()

	testsDir := t.TempDir()
	writeTestFile(t, testsDir, "login-flow", "stale-id")
	testPath := filepath.Join(testsDir, "login-flow.yaml")

	cfg := &config.ProjectConfig{
		Tests:     map[string]string{"login-flow": "stale-id"},
		Workflows: map[string]string{},
	}

	items, changed, err := syncTestsDomain(context.Background(), client, cfg, testsDir, syncOptions{Prompt: false, Prune: true, DryRun: false})
	if err != nil {
		t.Fatalf("syncTestsDomain() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if _, ok := cfg.Tests["login-flow"]; ok {
		t.Fatal("expected login-flow mapping to be pruned")
	}
	if _, statErr := os.Stat(testPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected unmodified local test file to be removed, stat error: %v", statErr)
	}

	found := false
	for _, it := range items {
		if it.Name == "login-flow" {
			found = true
			if it.Action != "prune-all" {
				t.Fatalf("action = %s, want prune-all", it.Action)
			}
			if it.Error != "" {
				t.Fatalf("unexpected error item: %s", it.Error)
			}
		}
	}
	if !found {
		t.Fatal("expected login-flow item in sync output")
	}
}

func TestReadSyncFlags_IsolatedPerCommand(t *testing.T) {
	cmdA := &cobra.Command{Use: "sync"}
	registerSyncFlags(cmdA)
	if err := cmdA.Flags().Parse([]string{"--tests", "--prune"}); err != nil {
		t.Fatalf("parse cmdA flags: %v", err)
	}
	flagsA, err := readSyncFlags(cmdA)
	if err != nil {
		t.Fatalf("readSyncFlags(cmdA): %v", err)
	}
	if !flagsA.tests || !flagsA.prune {
		t.Fatalf("cmdA flags incorrect: %+v", flagsA)
	}
	if flagsA.workflows || flagsA.apps {
		t.Fatalf("cmdA unexpected flags set: %+v", flagsA)
	}

	cmdB := &cobra.Command{Use: "sync"}
	registerSyncFlags(cmdB)
	if err := cmdB.Flags().Parse([]string{"--workflows", "--dry-run"}); err != nil {
		t.Fatalf("parse cmdB flags: %v", err)
	}
	flagsB, err := readSyncFlags(cmdB)
	if err != nil {
		t.Fatalf("readSyncFlags(cmdB): %v", err)
	}
	if !flagsB.workflows || !flagsB.dryRun {
		t.Fatalf("cmdB flags incorrect: %+v", flagsB)
	}
	if flagsB.tests || flagsB.prune || flagsB.apps {
		t.Fatalf("cmdB unexpected flags set: %+v", flagsB)
	}
}
