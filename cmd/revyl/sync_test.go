package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

	cfg := &config.ProjectConfig{}
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

	cfg := &config.ProjectConfig{}

	items, changed, err := syncTestsDomain(context.Background(), client, cfg, testsDir, syncOptions{Prompt: false, Prune: true, DryRun: false})
	if err != nil {
		t.Fatalf("syncTestsDomain() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}

	detachedID, _ := config.GetLocalTestRemoteID(testsDir, "login-flow")
	if detachedID != "" {
		t.Fatalf("expected login-flow remote_id to be cleared, got %q", detachedID)
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

	cfg := &config.ProjectConfig{}

	items, changed, err := syncTestsDomain(context.Background(), client, cfg, testsDir, syncOptions{Prompt: false, Prune: true, DryRun: false})
	if err != nil {
		t.Fatalf("syncTestsDomain() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}

	detachedID, _ := config.GetLocalTestRemoteID(testsDir, "login-flow")
	if detachedID != "" {
		t.Fatalf("expected login-flow remote_id to be cleared, got %q", detachedID)
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

	cfg := &config.ProjectConfig{}

	items, changed, err := syncTestsDomain(context.Background(), client, cfg, testsDir, syncOptions{Prompt: false, Prune: true, DryRun: false})
	if err != nil {
		t.Fatalf("syncTestsDomain() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
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
	if flagsA.apps || flagsA.skipImport {
		t.Fatalf("cmdA unexpected flags set: %+v", flagsA)
	}

	cmdB := &cobra.Command{Use: "sync"}
	registerSyncFlags(cmdB)
	if err := cmdB.Flags().Parse([]string{"--skip-import", "--dry-run"}); err != nil {
		t.Fatalf("parse cmdB flags: %v", err)
	}
	flagsB, err := readSyncFlags(cmdB)
	if err != nil {
		t.Fatalf("readSyncFlags(cmdB): %v", err)
	}
	if !flagsB.skipImport || !flagsB.dryRun {
		t.Fatalf("cmdB flags incorrect: %+v", flagsB)
	}
	if flagsB.tests || flagsB.prune || flagsB.apps {
		t.Fatalf("cmdB unexpected flags set: %+v", flagsB)
	}
}

func TestSyncCommandsDefaultToTestsOnlyAndRejectApps(t *testing.T) {
	for _, name := range []string{"root alias", "test subcommand"} {
		t.Run(name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "sync"}
			registerSyncFlags(cmd)
			flags, err := readSyncFlags(cmd)
			if err != nil {
				t.Fatalf("readSyncFlags() error = %v", err)
			}
			if flags.apps {
				t.Fatal("default sync unexpectedly selected app reconciliation")
			}
			if err := cmd.Flags().Set("apps", "true"); err != nil {
				t.Fatalf("set --apps: %v", err)
			}
			err = runSync(cmd, nil)
			if err == nil || !strings.Contains(err.Error(), "--apps is no longer supported") {
				t.Fatalf("runSync(--apps) error = %v", err)
			}
		})
	}
}

func TestSyncBootstrapIsLocalNoAuthAndJSONClean(t *testing.T) {
	workDir := t.TempDir()
	gitInitBuildRepository(t, workDir)
	writeProjectBuildConfig(t, workDir, "project:\n  id: 11111111-1111-4111-8111-111111111111\n")
	testsDir := filepath.Join(workDir, ".revyl", "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeTestFile(t, testsDir, "login-flow", "remote-123")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REVYL_API_KEY", "")

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	root := &cobra.Command{Use: "revyl"}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("dev", false, "")
	cmd := &cobra.Command{Use: "sync"}
	registerSyncFlags(cmd)
	root.AddCommand(cmd)
	if err := root.PersistentFlags().Set("json", "true"); err != nil {
		t.Fatalf("set --json: %v", err)
	}
	if err := cmd.Flags().Set("bootstrap", "true"); err != nil {
		t.Fatalf("set --bootstrap: %v", err)
	}

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	originalStdout := os.Stdout
	os.Stdout = writeEnd
	runErr := runSync(cmd, nil)
	_ = writeEnd.Close()
	os.Stdout = originalStdout
	var stdout bytes.Buffer
	_, _ = stdout.ReadFrom(readEnd)
	_ = readEnd.Close()
	if runErr != nil {
		t.Fatalf("runSync(--bootstrap --json) error = %v", runErr)
	}
	var output syncOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("stdout is not clean JSON: %v\n%s", err, stdout.String())
	}
	if output.Mode != "bootstrap" || len(output.Tests) != 1 || output.Tests[0].ID != "remote-123" {
		t.Fatalf("bootstrap output = %+v", output)
	}
}

func TestResolveSyncProjectFilesSelectsNearestNestedCanonicalConfig(t *testing.T) {
	repository := t.TempDir()
	command := exec.Command("git", "init", "-q", repository)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	writeProject := func(root, projectID string) {
		t.Helper()
		configDir := filepath.Join(root, ".revyl")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		contents := []byte("project:\n  id: " + projectID + "\n")
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), contents, 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	writeProject(repository, "00000000-0000-4000-8000-000000000001")
	projectRoot := filepath.Join(repository, "apps", "mobile")
	writeProject(projectRoot, "00000000-0000-4000-8000-000000000002")
	nested := filepath.Join(projectRoot, "src", "screens")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	project, err := resolveSyncProjectFiles(nested)
	if err != nil {
		t.Fatalf("resolveSyncProjectFiles() error = %v", err)
	}
	if project.Context.Authored.Project.ID != "00000000-0000-4000-8000-000000000002" {
		t.Fatalf("resolved project = %+v", project)
	}
	wantTestsDir, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	wantTestsDir = filepath.Join(wantTestsDir, ".revyl", "tests")
	if project.TestsDir != wantTestsDir {
		t.Fatalf("tests dir = %q", project.TestsDir)
	}
}

func TestSyncTestsDomainNeverWritesTopLevelCanonicalConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/get_simple_tests"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tests":[],"count":0}`))
		case r.URL.Path == "/api/v1/tests/get_test_by_id/deleted-id":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	command := exec.Command("git", "init", "-q", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	configPath := filepath.Join(root, ".revyl", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	original := []byte("project:\n  id: 00000000-0000-4000-8000-000000000001\n")
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	testsDir := filepath.Join(root, ".revyl", "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeTestFile(t, testsDir, "login-flow", "deleted-id")
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	cmd := newLeafCommand("sync", nil)
	registerSyncFlags(cmd)
	if err := cmd.Flags().Set("non-interactive", "true"); err != nil {
		t.Fatalf("set --non-interactive: %v", err)
	}
	if err := cmd.Flags().Set("prune", "true"); err != nil {
		t.Fatalf("set --prune: %v", err)
	}
	if err := runSync(cmd, nil); err != nil {
		t.Fatalf("runSync() error = %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("top-level canonical config changed:\n%s", after)
	}
}
