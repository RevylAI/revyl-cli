package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/testutil"
)

func TestRunCreateTestFromSessionCompilesAndCreatesTest(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	var compileStarted bool
	var createCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/execution/device-sessions/sess-1":
			_, _ = w.Write([]byte(`{
				"id":"sess-1",
				"org_id":"org-1",
				"platform":"ios",
				"status":"passed",
				"source_metadata":{"app_id":"app-1","app_name":"Bug Bazaar","build_version":"1.2.3"},
				"step_count":2,
				"action_count":2
			}`))
		case "/api/v1/recordings/compile":
			compileStarted = true
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode compile request: %v", err)
			}
			if req["source_type"] != "session" || req["source_id"] != "sess-1" {
				t.Fatalf("compile request = %#v", req)
			}
			_, _ = w.Write([]byte(`{"job_id":"job-1","status":"queued"}`))
		case "/api/v1/recordings/compile/job-1":
			_, _ = w.Write([]byte(`{
				"job_id":"job-1",
				"status":"completed",
				"progress":{"stage":"completed","total_chunks":1,"completed_chunks":1},
				"result":{
					"blocks":[{"type":"instructions","step_description":"Tap the checkout button"}],
					"suggested_title":"Checkout Flow #abcd",
					"warnings":[],
					"total_actions_compiled":2,
					"total_chunks_processed":1
				}
			}`))
		case "/api/v1/tests/yaml/from-blocks":
			createCalled = true
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			metadata, ok := req["metadata"].(map[string]any)
			if !ok {
				t.Fatalf("metadata = %#v", req["metadata"])
			}
			if metadata["name"] != "CLI Checkout" || metadata["platform"] != "ios" || metadata["app_id"] != "app-1" {
				t.Fatalf("metadata = %#v", metadata)
			}
			if metadata["pinned_version"] != "1.2.3" {
				t.Fatalf("pinned_version = %#v", metadata["pinned_version"])
			}
			blocks, ok := req["blocks"].([]any)
			if !ok || len(blocks) != 1 {
				t.Fatalf("blocks = %#v", req["blocks"])
			}
			_, _ = w.Write([]byte(`{"success":true,"test_id":"test-1","blocks_count":1,"generated_ids":["step-1"],"errors":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	resetCreateSessionFlags := func() {
		createTestPlatform = ""
		createTestAppID = ""
		createTestNoOpen = false
		createTestForce = false
		createTestDryRun = false
		createTestFromFile = ""
		createTestFromSession = ""
		createTestCompileTimeout = 120
		createTestModules = nil
		createTestTags = nil
		createTestInteractive = false
		createTestHotReload = false
	}
	resetCreateSessionFlags()
	t.Cleanup(resetCreateSessionFlags)

	createTestFromSession = "sess-1"
	createTestNoOpen = true
	createTestCompileTimeout = 5

	cmd := newLeafCommand("create", runCreateTestFromSession)
	if err := runCreateTestFromSession(cmd, []string{"CLI Checkout"}); err != nil {
		t.Fatalf("runCreateTestFromSession() error = %v", err)
	}

	if !compileStarted {
		t.Fatal("expected compile endpoint to be called")
	}
	if !createCalled {
		t.Fatal("expected create-from-blocks endpoint to be called")
	}

	localPath := filepath.Join(tmpDir, ".revyl", "tests", "CLI Checkout.yaml")
	content, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", localPath, err)
	}
	if got := string(content); !containsAll(got, "remote_id: test-1", "name: CLI Checkout", "platform: ios", "pinned_version: 1.2.3", "Tap the checkout button") {
		t.Fatalf("local YAML missing expected content:\n%s", got)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}
