// Package main provides tests for new CLI features:
// - parseLocation helper
// - maskValue helper
// - test env commands (list, set, delete, clear)
// - workflow location commands (set, clear, show)
// - workflow app commands (set, clear, show)
// - command registration for new subcommands
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

// --- parseLocation unit tests ---

func TestParseLocationValid(t *testing.T) {
	tests := []struct {
		input   string
		wantLat float64
		wantLng float64
	}{
		{"37.7749,-122.4194", 37.7749, -122.4194},
		{"0,0", 0, 0},
		{"-90,-180", -90, -180},
		{"90,180", 90, 180},
		{" 37.7749 , -122.4194 ", 37.7749, -122.4194}, // whitespace
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			lat, lng, err := parseLocation(tt.input)
			if err != nil {
				t.Fatalf("parseLocation(%q) returned error: %v", tt.input, err)
			}
			if lat != tt.wantLat {
				t.Errorf("lat = %f, want %f", lat, tt.wantLat)
			}
			if lng != tt.wantLng {
				t.Errorf("lng = %f, want %f", lng, tt.wantLng)
			}
		})
	}
}

func TestParseLocationInvalid(t *testing.T) {
	tests := []struct {
		input   string
		wantErr string
	}{
		{"", "invalid --location format"},
		{"37.7749", "invalid --location format"},
		{"abc,-122.4194", "invalid latitude"},
		{"37.7749,abc", "invalid longitude"},
		{"91,0", "latitude must be between -90 and 90"},
		{"-91,0", "latitude must be between -90 and 90"},
		{"0,181", "longitude must be between -180 and 180"},
		{"0,-181", "longitude must be between -180 and 180"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, _, err := parseLocation(tt.input)
			if err == nil {
				t.Fatalf("parseLocation(%q) expected error, got nil", tt.input)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseLocation(%q) error = %q, want to contain %q", tt.input, err.Error(), tt.wantErr)
			}
		})
	}
}

// --- maskValue unit tests ---

func TestMaskValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "****"},
		{"ab", "****"},
		{"abcd", "****"},
		{"abcde", "****bcde"},
		{"https://staging.example.com", "****.com"},
		{"my-secret-value-123", "****-123"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := maskValue(tt.input)
			if got != tt.want {
				t.Errorf("maskValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Command registration tests ---

func TestTestEnvSubcommands(t *testing.T) {
	// Find "test" command
	var testCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "test" {
			testCmd = cmd
			break
		}
	}
	if testCmd == nil {
		t.Fatal("expected 'test' command to exist")
	}

	// Find "env" subcommand
	var envCmd *cobra.Command
	for _, cmd := range testCmd.Commands() {
		if cmd.Name() == "env" {
			envCmd = cmd
			break
		}
	}
	if envCmd == nil {
		t.Fatal("expected 'test env' command to exist")
	}

	// Verify all env subcommands are registered
	expected := []string{"list", "set", "delete", "clear"}
	for _, name := range expected {
		found := false
		for _, cmd := range envCmd.Commands() {
			if cmd.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'test env %s' command, not found", name)
		}
	}
}

func TestWorkflowLocationSubcommands(t *testing.T) {
	var wfCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "workflow" {
			wfCmd = cmd
			break
		}
	}
	if wfCmd == nil {
		t.Fatal("expected 'workflow' command to exist")
	}

	var locCmd *cobra.Command
	for _, cmd := range wfCmd.Commands() {
		if cmd.Name() == "location" {
			locCmd = cmd
			break
		}
	}
	if locCmd == nil {
		t.Fatal("expected 'workflow location' command to exist")
	}

	expected := []string{"set", "clear", "show"}
	for _, name := range expected {
		found := false
		for _, cmd := range locCmd.Commands() {
			if cmd.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'workflow location %s' command, not found", name)
		}
	}
}

func TestWorkflowAppSubcommands(t *testing.T) {
	var wfCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "workflow" {
			wfCmd = cmd
			break
		}
	}
	if wfCmd == nil {
		t.Fatal("expected 'workflow' command to exist")
	}

	var appCmd *cobra.Command
	for _, cmd := range wfCmd.Commands() {
		if cmd.Name() == "app" {
			appCmd = cmd
			break
		}
	}
	if appCmd == nil {
		t.Fatal("expected 'workflow app' command to exist")
	}

	expected := []string{"set", "clear", "show"}
	for _, name := range expected {
		found := false
		for _, cmd := range appCmd.Commands() {
			if cmd.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'workflow app %s' command, not found", name)
		}
	}
}

// --- Mock server for new features ---

// newNewFeaturesServer creates a mock server for env var, workflow settings, and app endpoints.
func newNewFeaturesServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// GET /api/v1/variables/app_launch_env/read?test_id=X
	mux.HandleFunc("/api/v1/variables/app_launch_env/read", func(w http.ResponseWriter, r *http.Request) {
		testID := r.URL.Query().Get("test_id")
		w.Header().Set("Content-Type", "application/json")

		if testID == "test-no-envvars" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "success",
				"result":  []interface{}{},
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "success",
			"result": []map[string]interface{}{
				{
					"id":         "env-001",
					"test_id":    testID,
					"key":        "API_URL",
					"value":      "https://staging.example.com",
					"created_at": "2025-01-15T10:00:00Z",
					"updated_at": "2025-01-15T12:00:00Z",
				},
				{
					"id":         "env-002",
					"test_id":    testID,
					"key":        "DEBUG",
					"value":      "true",
					"created_at": "2025-01-15T10:00:00Z",
					"updated_at": "",
				},
			},
		})
	})

	// POST /api/v1/variables/app_launch_env/add
	mux.HandleFunc("/api/v1/variables/app_launch_env/add", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "success",
			"result": map[string]interface{}{
				"id":      "env-new",
				"test_id": body["test_id"],
				"key":     body["key"],
				"value":   body["value"],
			},
		})
	})

	// PUT /api/v1/variables/app_launch_env/update
	mux.HandleFunc("/api/v1/variables/app_launch_env/update", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "success",
			"result": map[string]interface{}{
				"id":    body["env_var_id"],
				"key":   body["key"],
				"value": body["value"],
			},
		})
	})

	// DELETE /api/v1/variables/app_launch_env/delete?env_var_id=X
	mux.HandleFunc("/api/v1/variables/app_launch_env/delete", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "deleted",
		})
	})

	// DELETE /api/v1/variables/app_launch_env/delete_all?test_id=X
	mux.HandleFunc("/api/v1/variables/app_launch_env/delete_all", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "deleted all",
		})
	})

	// GET /api/v1/workflows/get_workflow_info?workflow_id=X
	mux.HandleFunc("/api/v1/workflows/get_workflow_info", func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.URL.Query().Get("workflow_id")
		w.Header().Set("Content-Type", "application/json")

		if workflowID == "wf-with-location" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   workflowID,
				"name": "smoke-tests",
				"location_config": map[string]interface{}{
					"latitude":  37.7749,
					"longitude": -122.4194,
				},
				"override_location":     true,
				"build_config":          nil,
				"override_build_config": false,
			})
			return
		}

		if workflowID == "wf-with-app" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                workflowID,
				"name":              "smoke-tests",
				"location_config":   nil,
				"override_location": false,
				"build_config": map[string]interface{}{
					"ios_build": map[string]interface{}{
						"app_id": "ios-app-001",
					},
					"android_build": map[string]interface{}{
						"app_id": "android-app-001",
					},
				},
				"override_build_config": true,
			})
			return
		}

		// Default: no overrides
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":                    workflowID,
			"name":                  "smoke-tests",
			"location_config":       nil,
			"override_location":     false,
			"build_config":          nil,
			"override_build_config": false,
		})
	})

	// PUT /api/v1/workflows/update_location_config/{id}
	mux.HandleFunc("/api/v1/workflows/update_location_config/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "updated",
		})
	})

	// PUT /api/v1/workflows/update_build_config/{id}
	mux.HandleFunc("/api/v1/workflows/update_build_config/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "updated",
		})
	})

	// GET /api/v1/builds/vars/{id} (GetApp)
	mux.HandleFunc("/api/v1/builds/vars/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		appID := parts[len(parts)-1]
		w.Header().Set("Content-Type", "application/json")

		if appID == "invalid-app" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "not found",
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       appID,
			"name":     "Test App",
			"platform": "android",
		})
	})

	return httptest.NewServer(mux)
}

// withEnvMockClient overrides envSetupClient so that env command handlers use
// the mock HTTP server. Restores the original function via t.Cleanup.
func withEnvMockClient(t *testing.T, server *httptest.Server, testID string) {
	t.Helper()
	original := envSetupClient
	t.Cleanup(func() { envSetupClient = original })

	envSetupClient = func(cmd *cobra.Command, testNameOrID string) (string, *api.Client, error) {
		client := api.NewClientWithBaseURL("test-key", server.URL)
		// Map alias to ID
		resolvedID := testID
		if testNameOrID == "empty-test" {
			resolvedID = "test-no-envvars"
		}
		return resolvedID, client, nil
	}
}

// withWfSettingsMockClient overrides wfSettingsSetupClient so that workflow settings
// command handlers use the mock HTTP server.
func withWfSettingsMockClient(t *testing.T, server *httptest.Server, workflowID string) {
	t.Helper()
	original := wfSettingsSetupClient
	t.Cleanup(func() { wfSettingsSetupClient = original })

	wfSettingsSetupClient = func(cmd *cobra.Command, nameOrID string) (string, *api.Client, error) {
		client := api.NewClientWithBaseURL("test-key", server.URL)
		return workflowID, client, nil
	}
}

// --- Test Env List ---

func TestTestEnvListWithVars(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withEnvMockClient(t, server, "test-uuid-001")

	leaf := newLeafCommand("list", runTestEnvList)

	// Should not error when vars exist
	err := leaf.RunE(leaf, []string{"login-flow"})
	if err != nil {
		t.Fatalf("runTestEnvList: %v", err)
	}
}

func TestTestEnvListEmpty(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withEnvMockClient(t, server, "test-no-envvars")

	leaf := newLeafCommand("list", runTestEnvList)

	err := leaf.RunE(leaf, []string{"empty-test"})
	if err != nil {
		t.Fatalf("runTestEnvList empty: %v", err)
	}
}

// --- Test Env Set ---

func TestTestEnvSetNewKey(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	// Use test-no-envvars so key won't be found in existing list (triggers add path)
	withEnvMockClient(t, server, "test-no-envvars")

	leaf := newLeafCommand("set", runTestEnvSet)

	err := leaf.RunE(leaf, []string{"login-flow", "NEW_KEY=new_value"})
	if err != nil {
		t.Fatalf("runTestEnvSet new key: %v", err)
	}
}

func TestTestEnvSetExistingKey(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	// Use test-uuid-001 so API_URL will be found in existing list (triggers update path)
	withEnvMockClient(t, server, "test-uuid-001")

	leaf := newLeafCommand("set", runTestEnvSet)

	err := leaf.RunE(leaf, []string{"login-flow", "API_URL=https://new.example.com"})
	if err != nil {
		t.Fatalf("runTestEnvSet existing key: %v", err)
	}
}

func TestTestEnvSetInvalidFormat(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withEnvMockClient(t, server, "test-uuid-001")

	leaf := newLeafCommand("set", runTestEnvSet)

	err := leaf.RunE(leaf, []string{"login-flow", "INVALID_NO_EQUALS"})
	if err == nil {
		t.Fatal("expected error for invalid KEY=VALUE format, got nil")
	}
	if !strings.Contains(err.Error(), "invalid KEY=VALUE format") {
		t.Errorf("expected error about format, got: %v", err)
	}
}

func TestTestEnvSetEmptyKey(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withEnvMockClient(t, server, "test-uuid-001")

	leaf := newLeafCommand("set", runTestEnvSet)

	err := leaf.RunE(leaf, []string{"login-flow", "=some_value"})
	if err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
}

// --- Test Env Delete ---

func TestTestEnvDeleteExistingKey(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withEnvMockClient(t, server, "test-uuid-001")

	leaf := newLeafCommand("delete", runTestEnvDelete)

	err := leaf.RunE(leaf, []string{"login-flow", "API_URL"})
	if err != nil {
		t.Fatalf("runTestEnvDelete: %v", err)
	}
}

func TestTestEnvDeleteNonexistentKey(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withEnvMockClient(t, server, "test-uuid-001")

	leaf := newLeafCommand("delete", runTestEnvDelete)

	err := leaf.RunE(leaf, []string{"login-flow", "NONEXISTENT_KEY"})
	if err == nil {
		t.Fatal("expected error for non-existent key, got nil")
	}
	if !strings.Contains(err.Error(), "env var not found") {
		t.Errorf("expected 'env var not found' error, got: %v", err)
	}
}

// --- Test Env Clear ---

func TestTestEnvClearWithForce(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withEnvMockClient(t, server, "test-uuid-001")

	// Set --force to skip interactive prompt
	oldForce := testEnvForce
	testEnvForce = true
	defer func() { testEnvForce = oldForce }()

	leaf := newLeafCommand("clear", runTestEnvClear)

	err := leaf.RunE(leaf, []string{"login-flow"})
	if err != nil {
		t.Fatalf("runTestEnvClear: %v", err)
	}
}

// --- Workflow Location Set ---

func TestWorkflowLocationSetValid(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	// Set flags
	oldLat := workflowLocationLat
	oldLng := workflowLocationLng
	workflowLocationLat = 37.7749
	workflowLocationLng = -122.4194
	defer func() {
		workflowLocationLat = oldLat
		workflowLocationLng = oldLng
	}()

	leaf := newLeafCommand("set", runWorkflowLocationSet)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowLocationSet: %v", err)
	}
}

func TestWorkflowLocationSetInvalidLat(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	oldLat := workflowLocationLat
	oldLng := workflowLocationLng
	workflowLocationLat = 91.0 // invalid
	workflowLocationLng = 0
	defer func() {
		workflowLocationLat = oldLat
		workflowLocationLng = oldLng
	}()

	leaf := newLeafCommand("set", runWorkflowLocationSet)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err == nil {
		t.Fatal("expected error for invalid latitude, got nil")
	}
	if !strings.Contains(err.Error(), "latitude must be between") {
		t.Errorf("expected latitude error, got: %v", err)
	}
}

func TestWorkflowLocationSetInvalidLng(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	oldLat := workflowLocationLat
	oldLng := workflowLocationLng
	workflowLocationLat = 0
	workflowLocationLng = 181.0 // invalid
	defer func() {
		workflowLocationLat = oldLat
		workflowLocationLng = oldLng
	}()

	leaf := newLeafCommand("set", runWorkflowLocationSet)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err == nil {
		t.Fatal("expected error for invalid longitude, got nil")
	}
	if !strings.Contains(err.Error(), "longitude must be between") {
		t.Errorf("expected longitude error, got: %v", err)
	}
}

// --- Workflow Location Clear ---

func TestWorkflowLocationClear(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	leaf := newLeafCommand("clear", runWorkflowLocationClear)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowLocationClear: %v", err)
	}
}

// --- Workflow Location Show ---

func TestWorkflowLocationShowWithLocation(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-with-location")

	leaf := newLeafCommand("show", runWorkflowLocationShow)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowLocationShow: %v", err)
	}
}

func TestWorkflowLocationShowNoLocation(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	leaf := newLeafCommand("show", runWorkflowLocationShow)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowLocationShow no location: %v", err)
	}
}

// --- Workflow App Set ---

func TestWorkflowAppSetBothPlatforms(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	oldIOS := workflowAppIOS
	oldAndroid := workflowAppAndroid
	workflowAppIOS = "ios-app-001"
	workflowAppAndroid = "android-app-001"
	defer func() {
		workflowAppIOS = oldIOS
		workflowAppAndroid = oldAndroid
	}()

	leaf := newLeafCommand("set", runWorkflowAppSet)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowAppSet: %v", err)
	}
}

func TestWorkflowAppSetIOSOnly(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	oldIOS := workflowAppIOS
	oldAndroid := workflowAppAndroid
	workflowAppIOS = "ios-app-001"
	workflowAppAndroid = ""
	defer func() {
		workflowAppIOS = oldIOS
		workflowAppAndroid = oldAndroid
	}()

	leaf := newLeafCommand("set", runWorkflowAppSet)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowAppSet iOS only: %v", err)
	}
}

func TestWorkflowAppSetNoApps(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	oldIOS := workflowAppIOS
	oldAndroid := workflowAppAndroid
	workflowAppIOS = ""
	workflowAppAndroid = ""
	defer func() {
		workflowAppIOS = oldIOS
		workflowAppAndroid = oldAndroid
	}()

	leaf := newLeafCommand("set", runWorkflowAppSet)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err == nil {
		t.Fatal("expected error when no --ios or --android specified, got nil")
	}
	if !strings.Contains(err.Error(), "no app specified") {
		t.Errorf("expected 'no app specified' error, got: %v", err)
	}
}

func TestWorkflowAppSetInvalidAppID(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	oldIOS := workflowAppIOS
	oldAndroid := workflowAppAndroid
	workflowAppIOS = "invalid-app" // mock returns 404 for this
	workflowAppAndroid = ""
	defer func() {
		workflowAppIOS = oldIOS
		workflowAppAndroid = oldAndroid
	}()

	leaf := newLeafCommand("set", runWorkflowAppSet)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err == nil {
		t.Fatal("expected error for invalid app ID, got nil")
	}
	if !strings.Contains(err.Error(), "invalid iOS app ID") {
		t.Errorf("expected 'invalid iOS app ID' error, got: %v", err)
	}
}

// --- Workflow App Clear ---

func TestWorkflowAppClear(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	leaf := newLeafCommand("clear", runWorkflowAppClear)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowAppClear: %v", err)
	}
}

// --- Workflow App Show ---

func TestWorkflowAppShowWithConfig(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-with-app")

	leaf := newLeafCommand("show", runWorkflowAppShow)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowAppShow: %v", err)
	}
}

func TestWorkflowAppShowNoConfig(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	withWfSettingsMockClient(t, server, "wf-uuid-001")

	leaf := newLeafCommand("show", runWorkflowAppShow)

	err := leaf.RunE(leaf, []string{"smoke-tests"})
	if err != nil {
		t.Fatalf("runWorkflowAppShow no config: %v", err)
	}
}

// --- Workflow run app validation ---

func TestWorkflowRunAppValidation(t *testing.T) {
	// Verify that the --ios-app and --android-app flags exist on workflow run command
	var wfCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "workflow" {
			wfCmd = cmd
			break
		}
	}
	if wfCmd == nil {
		t.Fatal("expected 'workflow' command to exist")
	}

	var runCmd *cobra.Command
	for _, cmd := range wfCmd.Commands() {
		if cmd.Name() == "run" {
			runCmd = cmd
			break
		}
	}
	if runCmd == nil {
		t.Fatal("expected 'workflow run' command to exist")
	}

	// Check flags exist
	if runCmd.Flags().Lookup("ios-app") == nil {
		t.Error("expected --ios-app flag on workflow run")
	}
	if runCmd.Flags().Lookup("android-app") == nil {
		t.Error("expected --android-app flag on workflow run")
	}
	if runCmd.Flags().Lookup("location") == nil {
		t.Error("expected --location flag on workflow run")
	}
}

// --- Test run location flag ---

func TestTestRunLocationFlag(t *testing.T) {
	var testRunCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "test" {
			for _, sub := range cmd.Commands() {
				if sub.Name() == "run" {
					testRunCmd = sub
					break
				}
			}
			break
		}
	}
	if testRunCmd == nil {
		t.Fatal("expected 'test run' command to exist")
	}

	if testRunCmd.Flags().Lookup("location") == nil {
		t.Error("expected --location flag on test run")
	}
}

// --- Verify API endpoint paths in mock server ---

func TestEnvVarAPIEndpoints(t *testing.T) {
	// Verify the API client methods use the correct endpoint paths
	// by calling them against the mock server

	server := newNewFeaturesServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	// List env vars
	resp, err := client.ListEnvVars(ctx, "test-uuid-001")
	if err != nil {
		t.Fatalf("ListEnvVars: %v", err)
	}
	if len(resp.Result) != 2 {
		t.Errorf("ListEnvVars: expected 2 vars, got %d", len(resp.Result))
	}
	if resp.Result[0].Key != "API_URL" {
		t.Errorf("ListEnvVars: expected key 'API_URL', got %q", resp.Result[0].Key)
	}

	// Add env var
	addResp, err := client.AddEnvVar(ctx, "test-uuid-001", "NEW_KEY", "new_value")
	if err != nil {
		t.Fatalf("AddEnvVar: %v", err)
	}
	if addResp.Result.Key != "NEW_KEY" {
		t.Errorf("AddEnvVar: expected key 'NEW_KEY', got %q", addResp.Result.Key)
	}

	// Update env var
	updateResp, err := client.UpdateEnvVar(ctx, "env-001", "API_URL", "https://new.com")
	if err != nil {
		t.Fatalf("UpdateEnvVar: %v", err)
	}
	if updateResp.Result.Value != "https://new.com" {
		t.Errorf("UpdateEnvVar: expected value 'https://new.com', got %q", updateResp.Result.Value)
	}

	// Delete env var
	err = client.DeleteEnvVar(ctx, "env-001")
	if err != nil {
		t.Fatalf("DeleteEnvVar: %v", err)
	}

	// Delete all env vars
	err = client.DeleteAllEnvVars(ctx, "test-uuid-001")
	if err != nil {
		t.Fatalf("DeleteAllEnvVars: %v", err)
	}
}

func TestWorkflowSettingsAPIEndpoints(t *testing.T) {
	server := newNewFeaturesServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	// Get workflow (with location)
	wf, err := client.GetWorkflow(ctx, "wf-with-location")
	if err != nil {
		t.Fatalf("GetWorkflow: %v", err)
	}
	if !wf.OverrideLocation {
		t.Error("expected OverrideLocation to be true")
	}
	if wf.LocationConfig == nil {
		t.Fatal("expected LocationConfig to be non-nil")
	}

	// Update location config
	err = client.UpdateWorkflowLocationConfig(ctx, "wf-uuid-001",
		map[string]interface{}{"latitude": 40.0, "longitude": -74.0}, true)
	if err != nil {
		t.Fatalf("UpdateWorkflowLocationConfig: %v", err)
	}

	// Clear location config
	err = client.UpdateWorkflowLocationConfig(ctx, "wf-uuid-001", nil, false)
	if err != nil {
		t.Fatalf("UpdateWorkflowLocationConfig clear: %v", err)
	}

	// Update build config
	err = client.UpdateWorkflowBuildConfig(ctx, "wf-uuid-001",
		map[string]interface{}{
			"ios_build": map[string]interface{}{"app_id": "ios-001"},
		}, true)
	if err != nil {
		t.Fatalf("UpdateWorkflowBuildConfig: %v", err)
	}

	// Clear build config
	err = client.UpdateWorkflowBuildConfig(ctx, "wf-uuid-001", nil, false)
	if err != nil {
		t.Fatalf("UpdateWorkflowBuildConfig clear: %v", err)
	}

	// Get workflow (with app config)
	wfApp, err := client.GetWorkflow(ctx, "wf-with-app")
	if err != nil {
		t.Fatalf("GetWorkflow with app: %v", err)
	}
	if !wfApp.OverrideBuildConfig {
		t.Error("expected OverrideBuildConfig to be true")
	}
	if wfApp.BuildConfig == nil {
		t.Fatal("expected BuildConfig to be non-nil")
	}

	// Get app (valid)
	app, err := client.GetApp(ctx, "ios-app-001")
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if app.Name != "Test App" {
		t.Errorf("GetApp: expected name 'Test App', got %q", app.Name)
	}

	// Get app (invalid - 404)
	_, err = client.GetApp(ctx, "invalid-app")
	if err == nil {
		t.Error("expected error for invalid app ID, got nil")
	}
}

// --- Workflow location set flag registration ---

func TestWorkflowLocationSetFlags(t *testing.T) {
	if workflowLocationSetCmd.Flags().Lookup("lat") == nil {
		t.Error("expected --lat flag on workflow location set")
	}
	if workflowLocationSetCmd.Flags().Lookup("lng") == nil {
		t.Error("expected --lng flag on workflow location set")
	}
}

// --- Workflow app set flag registration ---

func TestWorkflowAppSetFlags(t *testing.T) {
	if workflowAppSetCmd.Flags().Lookup("ios") == nil {
		t.Error("expected --ios flag on workflow app set")
	}
	if workflowAppSetCmd.Flags().Lookup("android") == nil {
		t.Error("expected --android flag on workflow app set")
	}
}

// --- Test env clear --force flag ---

func TestTestEnvClearForceFlag(t *testing.T) {
	if testEnvClearCmd.Flags().Lookup("force") == nil {
		t.Error("expected --force flag on test env clear")
	}

	// Verify shorthand
	f := testEnvClearCmd.Flags().ShorthandLookup("f")
	if f == nil {
		t.Error("expected -f shorthand for --force on test env clear")
	}
}

// --- Verify mock endpoint request methods ---

func TestEnvVarDeleteEndpointMethod(t *testing.T) {
	// Verify that DELETE requests are sent with the correct method
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "delete") && !strings.Contains(r.URL.Path, "delete_all") {
			receivedMethod = r.Method
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"message":"ok"}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_ = client.DeleteEnvVar(context.Background(), "env-001")

	if receivedMethod != "DELETE" {
		t.Errorf("expected DELETE method, got %q", receivedMethod)
	}
}

func TestEnvVarAddEndpointMethod(t *testing.T) {
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "add") {
			receivedMethod = r.Method
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"message":"ok","result":{"id":"x","key":"k","value":"v"}}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, _ = client.AddEnvVar(context.Background(), "test-001", "KEY", "VALUE")

	if receivedMethod != "POST" {
		t.Errorf("expected POST method for AddEnvVar, got %q", receivedMethod)
	}
}

func TestWorkflowLocationConfigEndpointMethod(t *testing.T) {
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "update_location_config") {
			receivedMethod = r.Method
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"message":"ok"}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_ = client.UpdateWorkflowLocationConfig(context.Background(), "wf-001", nil, false)

	if receivedMethod != "PUT" {
		t.Errorf("expected PUT method for UpdateWorkflowLocationConfig, got %q", receivedMethod)
	}
}
