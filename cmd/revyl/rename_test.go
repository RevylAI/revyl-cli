package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

const renameTestID = "44444444-4444-4444-8444-444444444444"

func TestRunRenameTestUsesNearestCanonicalProjectTestsDir(t *testing.T) {
	repository := t.TempDir()
	gitInitForRenameTest(t, repository)
	projectRoot := filepath.Join(repository, "apps", "mobile")
	workingDirectory := filepath.Join(projectRoot, "src", "screens")
	testsDir := filepath.Join(projectRoot, ".revyl", "tests")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRenameTestFile(t, filepath.Join(projectRoot, ".revyl", "config.yaml"), "project:\n  id: 11111111-1111-4111-8111-111111111111\n")
	writeRenameTestFile(t, filepath.Join(testsDir, "old-name.yaml"), "_meta:\n  remote_id: "+renameTestID+"\ntest:\n  metadata:\n    name: Remote Name\n")
	withWorkingDir(t, workingDirectory)

	updateCount := 0
	server := newRenameTestServer(t, renameTestID, &updateCount)
	defer server.Close()
	configureRenameCommandTest(t, server.URL)

	cmd := newLeafCommand("rename", runRenameTest)
	if err := runRenameTest(cmd, []string{"old-name", "new-name"}); err != nil {
		t.Fatalf("runRenameTest() error = %v", err)
	}
	if updateCount != 1 {
		t.Fatalf("remote update count = %d, want 1", updateCount)
	}
	if _, err := os.Stat(filepath.Join(testsDir, "old-name.yaml")); !os.IsNotExist(err) {
		t.Fatalf("old canonical-project alias still exists: %v", err)
	}
	renamed, err := config.LoadLocalTest(filepath.Join(testsDir, "new-name.yaml"))
	if err != nil {
		t.Fatalf("load renamed canonical-project alias: %v", err)
	}
	if renamed.Test.Metadata.Name != "new-name" {
		t.Fatalf("renamed metadata name = %q, want new-name", renamed.Test.Metadata.Name)
	}
	if _, err := os.Stat(filepath.Join(workingDirectory, ".revyl")); !os.IsNotExist(err) {
		t.Fatalf("rename wrote relative to the nested working directory: %v", err)
	}
}

func TestRunRenameTestRejectsDiscoveredInvalidConfigBeforeRemoteRequest(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		source string
	}{
		{name: "malformed canonical", source: "project:\n  id: not-a-uuid\n"},
		{name: "legacy", source: "project_id: 11111111-1111-4111-8111-111111111111\nname: legacy\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := t.TempDir()
			gitInitForRenameTest(t, repository)
			writeRenameTestFile(t, filepath.Join(repository, ".revyl", "config.yaml"), testCase.source)
			withWorkingDir(t, repository)

			requestCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				t.Fatalf("remote request occurred before canonical config preflight: %s %s", r.Method, r.URL.Path)
			}))
			defer server.Close()
			configureRenameCommandTest(t, server.URL)

			cmd := newLeafCommand("rename", runRenameTest)
			err := runRenameTest(cmd, []string{renameTestID, "new-name"})
			if err == nil || !strings.Contains(err.Error(), "cannot inspect local project before renaming test") {
				t.Fatalf("runRenameTest() error = %v, want canonical config preflight failure", err)
			}
			if requestCount != 0 {
				t.Fatalf("remote request count = %d, want 0", requestCount)
			}
		})
	}
}

func TestRunRenameTestByUUIDRemainsConfigless(t *testing.T) {
	workingDirectory := t.TempDir()
	withWorkingDir(t, workingDirectory)
	updateCount := 0
	server := newRenameTestServer(t, renameTestID, &updateCount)
	defer server.Close()
	configureRenameCommandTest(t, server.URL)

	cmd := newLeafCommand("rename", runRenameTest)
	if err := runRenameTest(cmd, []string{renameTestID, "new-name"}); err != nil {
		t.Fatalf("runRenameTest() error = %v", err)
	}
	if updateCount != 1 {
		t.Fatalf("remote update count = %d, want 1", updateCount)
	}
	if _, err := os.Stat(filepath.Join(workingDirectory, ".revyl")); !os.IsNotExist(err) {
		t.Fatalf("configless rename created local project state: %v", err)
	}
}

func configureRenameCommandTest(t *testing.T, backendURL string) {
	t.Helper()
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", backendURL)
	previousNonInteractive, previousYes := renameNonInteractive, renameYes
	renameNonInteractive, renameYes = true, false
	t.Cleanup(func() {
		renameNonInteractive, renameYes = previousNonInteractive, previousYes
	})
}

func newRenameTestServer(t *testing.T, testID string, updateCount *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tests/get_test_by_id/"+testID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"name":"Remote Name","platform":"ios","version":7}`, testID)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tests/get_simple_tests":
			_, _ = fmt.Fprintf(w, `{"tests":[{"id":%q,"name":"Remote Name","platform":"ios"}],"count":1}`, testID)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/tests/update/"+testID:
			*updateCount++
			_, _ = fmt.Fprintf(w, `{"id":%q,"version":8}`, testID)
		default:
			t.Fatalf("unexpected rename API request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
}

func gitInitForRenameTest(t *testing.T, root string) {
	t.Helper()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func writeRenameTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChooseAliasForTestRename(t *testing.T) {
	tests := []struct {
		name        string
		aliases     map[string]string
		oldNameOrID string
		remoteName  string
		testID      string
		wantAlias   string
		wantAmbig   bool
	}{
		{
			name: "old arg alias wins",
			aliases: map[string]string{
				"CLI-0-onboard-a": "id-1",
			},
			oldNameOrID: "CLI-0-onboard-a",
			remoteName:  "CLI-0-onboard-a",
			testID:      "id-1",
			wantAlias:   "CLI-0-onboard-a",
		},
		{
			name: "single alias inferred",
			aliases: map[string]string{
				"tracked-name": "id-2",
			},
			oldNameOrID: "id-2",
			remoteName:  "Remote Name",
			testID:      "id-2",
			wantAlias:   "tracked-name",
		},
		{
			name: "ambiguous aliases",
			aliases: map[string]string{
				"one": "id-3",
				"two": "id-3",
			},
			oldNameOrID: "id-3",
			remoteName:  "Remote Name",
			testID:      "id-3",
			wantAlias:   "",
			wantAmbig:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAlias, gotAmbig := chooseAliasForTestRename(tt.aliases, tt.oldNameOrID, tt.remoteName, tt.testID)
			if gotAlias != tt.wantAlias {
				t.Fatalf("alias=%q want %q", gotAlias, tt.wantAlias)
			}
			if gotAmbig != tt.wantAmbig {
				t.Fatalf("ambiguous=%v want %v", gotAmbig, tt.wantAmbig)
			}
		})
	}
}

func TestChooseLocalFileForTestRename(t *testing.T) {
	tests := []struct {
		name          string
		local         map[string]*config.LocalTest
		aliasToRename string
		oldNameOrID   string
		remoteName    string
		testID        string
		wantAlias     string
		wantAmbig     bool
	}{
		{
			name: "uses alias file even with empty remote id",
			local: map[string]*config.LocalTest{
				"tracked": {Meta: config.TestMeta{RemoteID: ""}},
			},
			aliasToRename: "tracked",
			oldNameOrID:   "id-1",
			remoteName:    "Remote",
			testID:        "id-1",
			wantAlias:     "tracked",
		},
		{
			name: "falls back to remote id match",
			local: map[string]*config.LocalTest{
				"other": {Meta: config.TestMeta{RemoteID: "id-x"}},
				"mine":  {Meta: config.TestMeta{RemoteID: "id-2"}},
			},
			oldNameOrID: "id-2",
			remoteName:  "Remote",
			testID:      "id-2",
			wantAlias:   "mine",
		},
		{
			name: "ambiguous remote id matches",
			local: map[string]*config.LocalTest{
				"a": {Meta: config.TestMeta{RemoteID: "id-3"}},
				"b": {Meta: config.TestMeta{RemoteID: "id-3"}},
			},
			oldNameOrID: "id-3",
			remoteName:  "Remote",
			testID:      "id-3",
			wantAlias:   "a",
			wantAmbig:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAlias, gotAmbig := chooseLocalFileForTestRename(tt.local, tt.aliasToRename, tt.oldNameOrID, tt.remoteName, tt.testID)
			if gotAlias != tt.wantAlias {
				t.Fatalf("alias=%q want %q", gotAlias, tt.wantAlias)
			}
			if gotAmbig != tt.wantAmbig {
				t.Fatalf("ambiguous=%v want %v", gotAmbig, tt.wantAmbig)
			}
		})
	}
}

func TestTestRenameSubcommandRegistered(t *testing.T) {
	var testCmdFound bool
	var renameFound bool

	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() != "test" {
			continue
		}
		testCmdFound = true
		for _, child := range cmd.Commands() {
			if child.Name() == "rename" {
				renameFound = true
				break
			}
		}
	}

	if !testCmdFound {
		t.Fatal("expected 'test' command to exist")
	}
	if !renameFound {
		t.Fatal("expected 'test rename' command to exist")
	}
}
