// Package main provides tests for new CLI features:
// - parseLocation helper
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

// --- Command registration tests ---

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

// newNewFeaturesServer creates a mock server for workflow settings and app endpoints.
func newNewFeaturesServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

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
