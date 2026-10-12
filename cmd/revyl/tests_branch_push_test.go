package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/testutil"
)

func TestPushReturnsSelectorFailureWhileKeepingSuccessfulTests(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	projectRoot := t.TempDir()
	gitInitCreateRepository(t, projectRoot)
	writeCanonicalCreateConfig(t, projectRoot, "project:\n  id: 00000000-0000-4000-8000-000000000001\n")
	t.Chdir(projectRoot)
	testsDir := filepath.Join(projectRoot, ".revyl", "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"accepted", "rejected"} {
		local := &config.LocalTest{Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: name, Platform: "ios"},
			Build: config.TestBuildConfig{
				Name: "App", BranchID: "11111111-1111-4111-8111-111111111111", BuildSelection: "pinned",
				PinnedBuildID: "22222222-2222-4222-8222-222222222222", PinnedVersion: "1.0",
			},
			Blocks: []config.TestBlock{{Type: "instructions", StepDescription: "Open app"}},
		}}
		if err := config.SaveLocalTest(filepath.Join(testsDir, name+".yaml"), local); err != nil {
			t.Fatal(err)
		}
	}
	oldForce, oldDryRun := testsForce, testsPushDryRun
	testsForce, testsPushDryRun = true, false
	t.Cleanup(func() { testsForce, testsPushDryRun = oldForce, oldDryRun })
	created := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user","org_id":"org"}`))
		case "/api/v1/apps/":
			_, _ = w.Write([]byte(`{"items":[{"id":"app","name":"App","platform":"ios","versions_count":1,"latest_version":"1.0"}],"total":1,"has_next":false}`))
		case "/api/v1/tests/yaml/validate-yaml":
			_, _ = w.Write([]byte(`{"is_valid":true,"validation_type":"full_test","errors":0,"warnings":0,"messages":[]}`))
		case "/api/v1/tests/scripts":
			_, _ = w.Write([]byte(`{"scripts":[],"count":0}`))
		case "/api/v1/modules/list":
			_, _ = w.Write([]byte(`{"result":[]}`))
		case "/api/v1/tests/create":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			name := body["name"].(string)
			created[name] = true
			if body["pinned_version"] != "1.0" || body["build_selection"] != "pinned" {
				t.Errorf("mixed selector missing: %v", body)
			}
			if name == "rejected" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"detail":"Pinned version and exact build disagree"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"accepted-id","version":1}`))
			}
		case "/api/v1/variables/custom/delete_all":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	err := runTestsPush(newLeafCommand("push", runTestsPush), nil)
	if err == nil || !strings.Contains(err.Error(), "rejected: Pinned version and exact build disagree") {
		t.Fatalf("push did not return per-test selector failure: %v", err)
	}
	if !created["accepted"] || !created["rejected"] {
		t.Fatalf("push did not finish independent tests: %v", created)
	}
	accepted, err := config.LoadLocalTest(filepath.Join(testsDir, "accepted.yaml"))
	if err != nil || accepted.Meta.RemoteID != "accepted-id" {
		t.Fatalf("successful test lost: local=%+v err=%v", accepted, err)
	}
	rejected, err := config.LoadLocalTest(filepath.Join(testsDir, "rejected.yaml"))
	if err != nil || rejected.Meta.RemoteID != "" {
		t.Fatalf("failed test gained success state: local=%+v err=%v", rejected, err)
	}
}
