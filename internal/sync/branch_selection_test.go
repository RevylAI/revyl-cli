package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sync/testutil"
)

func TestImportRemoteTest_SavedBuildSelectionRoundTrip(t *testing.T) {
	const branchID = "11111111-1111-4111-8111-111111111111"
	const buildID = "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		name          string
		remoteFields  map[string]any
		wantBuild     config.TestBuildConfig
		wantSelection map[string]any
	}{
		{
			name:          "legacy_pin_with_omitted_mode",
			remoteFields:  map[string]any{"pinned_version": "v_old"},
			wantBuild:     config.TestBuildConfig{PinnedVersion: "v_old"},
			wantSelection: map[string]any{"pinned_version": "v_old"},
		},
		{
			name:          "legacy_pin_with_inherit_and_null_ids",
			remoteFields:  map[string]any{"build_selection": "inherit", "branch_id": nil, "pinned_build_id": nil, "pinned_version": "v_old"},
			wantBuild:     config.TestBuildConfig{PinnedVersion: "v_old"},
			wantSelection: map[string]any{"pinned_version": "v_old"},
		},
		{
			name:          "canonical_pin_with_legacy_mirror",
			remoteFields:  map[string]any{"build_selection": "pinned", "branch_id": branchID, "pinned_build_id": buildID, "pinned_version": "v_old"},
			wantBuild:     config.TestBuildConfig{BuildSelection: "pinned", BranchID: branchID, PinnedBuildID: buildID},
			wantSelection: map[string]any{"build_selection": "pinned", "branch_id": branchID, "pinned_build_id": buildID},
		},
		{
			name:          "latest_with_stale_legacy_mirror",
			remoteFields:  map[string]any{"build_selection": "latest", "branch_id": nil, "pinned_build_id": nil, "pinned_version": "v_old"},
			wantBuild:     config.TestBuildConfig{BuildSelection: "latest"},
			wantSelection: map[string]any{"build_selection": "latest", "branch_id": nil, "pinned_build_id": nil},
		},
		{
			name:          "branch_latest_with_stale_legacy_mirror",
			remoteFields:  map[string]any{"build_selection": "latest", "branch_id": branchID, "pinned_build_id": nil, "pinned_version": "v_old"},
			wantBuild:     config.TestBuildConfig{BuildSelection: "latest", BranchID: branchID},
			wantSelection: map[string]any{"build_selection": "latest", "branch_id": branchID, "pinned_build_id": nil},
		},
		{
			name:          "branch_inherit_with_stale_legacy_mirror",
			remoteFields:  map[string]any{"build_selection": "inherit", "branch_id": branchID, "pinned_build_id": nil, "pinned_version": "v_old"},
			wantBuild:     config.TestBuildConfig{BuildSelection: "inherit", BranchID: branchID},
			wantSelection: map[string]any{"build_selection": "inherit", "branch_id": branchID, "pinned_build_id": nil},
		},
		{
			name:          "automatic_without_legacy_pin",
			remoteFields:  map[string]any{"build_selection": "inherit", "branch_id": nil, "pinned_build_id": nil, "pinned_version": nil},
			wantBuild:     config.TestBuildConfig{BuildSelection: "inherit"},
			wantSelection: map[string]any{"build_selection": "inherit", "branch_id": nil, "pinned_build_id": nil},
		},
		{
			name: "omitted_selection",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remoteTest := map[string]any{
				"id": "remote-id", "name": "Test", "platform": "ios", "app_id": "app-id",
				"tasks": []any{}, "version": 3,
			}
			for key, value := range tc.remoteFields {
				remoteTest[key] = value
			}
			updates := make(chan map[string]any, 1)
			client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/tests/get_test_by_id/remote-id":
					if err := json.NewEncoder(w).Encode(remoteTest); err != nil {
						t.Errorf("encode remote test: %v", err)
					}
				case "GET /api/v1/apps/app-id":
					_, _ = w.Write([]byte(`{"id":"app-id","name":"App","platform":"ios","versions_count":1,"latest_version":"v_new"}`))
				case "GET /api/v1/apps/":
					_, _ = w.Write([]byte(`{"items":[{"id":"app-id","name":"App","platform":"ios","versions_count":1,"latest_version":"v_new"}],"total":1,"page":1,"page_size":100,"total_pages":1,"has_next":false,"has_previous":false}`))
				case "GET /api/v1/tests/tags/tests/remote-id":
					_, _ = w.Write([]byte(`[]`))
				case "GET /api/v1/tests/scripts", "GET /api/v1/modules/list",
					"GET /api/v1/variables/custom/read_variables", "GET /api/v1/variables/org_launch_env/test-attachments":
					_, _ = w.Write([]byte(`{"scripts":[],"result":[]}`))
				case "PUT /api/v1/tests/update/remote-id":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode update: %v", err)
						http.Error(w, "invalid update", http.StatusBadRequest)
						return
					}
					updates <- body
					_, _ = w.Write([]byte(`{"id":"remote-id","version":4}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			defer cleanup()

			testsDir := t.TempDir()
			importer := NewResolver(client, &config.ProjectConfig{}, map[string]*config.LocalTest{})
			results, err := importer.ImportRemoteTest(context.Background(), "remote-id", "test", testsDir, false)
			if err != nil || len(results) != 1 || results[0].Error != nil || results[0].Conflict {
				t.Fatalf("ImportRemoteTest() results=%+v err=%v", results, err)
			}
			local, err := config.LoadLocalTest(filepath.Join(testsDir, "test.yaml"))
			if err != nil {
				t.Fatalf("LoadLocalTest() error = %v", err)
			}
			wantBuild := tc.wantBuild
			wantBuild.Name = "App"
			if local.Test.Build != wantBuild {
				t.Errorf("persisted build = %+v, want %+v", local.Test.Build, wantBuild)
			}
			local.Test.Metadata.Description = "Edited description"
			if !local.HasLocalChanges() {
				t.Fatal("metadata edit was not detected as a local change")
			}
			pusher := NewResolver(client, &config.ProjectConfig{}, map[string]*config.LocalTest{"test": local})
			results, err = pusher.SyncToRemote(context.Background(), "test", testsDir, false)
			if err != nil || len(results) != 1 || results[0].Error != nil || results[0].TagSyncError != nil || results[0].ConfigSyncError != nil {
				t.Fatalf("SyncToRemote() results=%+v err=%v", results, err)
			}
			var update map[string]any
			select {
			case update = <-updates:
			default:
				t.Fatal("missing update request")
			}
			if update["description"] != "Edited description" || update["app_id"] != "app-id" || update["expected_version"] != float64(3) {
				t.Errorf("update did not preserve the app/version and edit metadata: %v", update)
			}
			for _, key := range []string{"pinned_version", "branch_id", "build_selection", "pinned_build_id"} {
				got, gotPresent := update[key]
				want, wantPresent := tc.wantSelection[key]
				if gotPresent != wantPresent || got != want {
					t.Errorf("update %s = %v (present %t), want %v (present %t)", key, got, gotPresent, want, wantPresent)
				}
			}
		})
	}
}

func TestSyncWritesSavedBranchSelectorsAndExplicitReset(t *testing.T) {
	const branchID = "11111111-1111-4111-8111-111111111111"
	const buildID = "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		name                  string
		build                 config.TestBuildConfig
		wantMode              string
		wantBranch, wantBuild interface{}
	}{
		{"pin", config.TestBuildConfig{Name: "App", BranchID: branchID, BuildSelection: "pinned", PinnedBuildID: buildID}, "pinned", branchID, buildID},
		{"latest", config.TestBuildConfig{Name: "App", BranchID: branchID, BuildSelection: "latest"}, "latest", branchID, nil},
		{"reset", config.TestBuildConfig{Name: "App", BuildSelection: "inherit"}, "inherit", nil, nil},
		{"omitted", config.TestBuildConfig{Name: "App"}, "", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockServer()
			defer mock.Close()
			mock.SeedApp(testutil.MockApp{ID: "app", Name: "App", Platform: "ios"})
			local := &config.LocalTest{Test: config.TestDefinition{Metadata: config.TestMetadata{Name: "Test", Platform: "ios"}, Build: tc.build, Blocks: []config.TestBlock{{Type: "instructions", StepDescription: "Tap"}}}}
			resolver := NewResolver(mock.Client, &config.ProjectConfig{}, map[string]*config.LocalTest{"test": local})
			for _, create := range []bool{true, false} {
				results, err := resolver.SyncToRemote(context.Background(), "test", t.TempDir(), false)
				if err != nil || len(results) != 1 || results[0].Error != nil {
					t.Fatalf("results=%+v err=%v", results, err)
				}
				method, path := "PUT", "/api/v1/tests/update/"+local.Meta.RemoteID
				if create {
					method, path = "POST", "/api/v1/tests/create"
				}
				var body map[string]interface{}
				for _, call := range mock.Calls() {
					if call.Method == method && call.Path == path {
						if err := json.Unmarshal(call.Body, &body); err != nil {
							t.Fatal(err)
						}
					}
				}
				if body == nil {
					t.Fatal("missing request")
				}
				if tc.wantMode == "" {
					if _, exists := body["build_selection"]; exists {
						t.Fatalf("omitted selector sent: %v", body)
					}
				} else if body["build_selection"] != tc.wantMode || body["branch_id"] != tc.wantBranch || body["pinned_build_id"] != tc.wantBuild {
					t.Fatalf("selector=%v", body)
				}
			}
		})
	}
}

func TestSyncResolvesGlobalBuildNameBeforeSendingABranch(t *testing.T) {
	const branchID = "11111111-1111-4111-8111-111111111111"
	mock := testutil.NewMockServer()
	defer mock.Close()
	mock.SeedApp(testutil.MockApp{ID: "app", Name: "Shop iOS", Platform: "ios"})
	mock.SeedGlobalVariable("name", "Shop iOS")
	local := &config.LocalTest{Test: config.TestDefinition{
		Metadata: config.TestMetadata{Name: "Test", Platform: "ios"},
		Build:    config.TestBuildConfig{Name: "{{global.name}}", BranchID: branchID, BuildSelection: "latest"},
		Blocks:   []config.TestBlock{{Type: "instructions", StepDescription: "Tap"}},
	}}
	resolver := NewResolver(mock.Client, &config.ProjectConfig{}, map[string]*config.LocalTest{"test": local})

	results, err := resolver.SyncToRemote(context.Background(), "test", t.TempDir(), false)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	var body map[string]interface{}
	for _, call := range mock.Calls() {
		if call.Method == "POST" && call.Path == "/api/v1/tests/create" {
			if err := json.Unmarshal(call.Body, &body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if body["app_id"] != "app" || body["branch_id"] != branchID {
		t.Fatalf("create request = %v, want the resolved app with the saved branch", body)
	}
}

func TestSyncReportsAnUnresolvableGlobalBuildName(t *testing.T) {
	mock := testutil.NewMockServer()
	defer mock.Close()
	local := &config.LocalTest{Test: config.TestDefinition{
		Metadata: config.TestMetadata{Name: "Test", Platform: "ios"},
		Build:    config.TestBuildConfig{Name: "{{global.missing}}"},
		Blocks:   []config.TestBlock{{Type: "instructions", StepDescription: "Tap"}},
	}}
	resolver := NewResolver(mock.Client, &config.ProjectConfig{}, map[string]*config.LocalTest{"test": local})

	results, err := resolver.SyncToRemote(context.Background(), "test", t.TempDir(), false)
	if err != nil || len(results) != 1 || results[0].Error == nil || !strings.Contains(results[0].Error.Error(), "{{global.missing}}") {
		t.Fatalf("results=%+v err=%v, want a missing global variable error", results, err)
	}
	if mock.HasCall("POST", "/api/v1/tests/create") {
		t.Fatal("created a test without resolving its app")
	}
}
