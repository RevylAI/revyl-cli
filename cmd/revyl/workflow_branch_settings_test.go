package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestWorkflowAppBranchCommandsProduceJSON(t *testing.T) {
	for _, operation := range []string{"show", "set", "clear", "disable", "enable"} {
		t.Run(operation, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/workflows/get_workflow_info":
					_, _ = w.Write([]byte(`{"id":"workflow","override_build_config":true,"build_config":{"ios_build":{"app_id":"app","build_selection":"inherit","branch_id":null,"pinned_build_id":null}}}`))
				case "/api/v1/workflows/update_build_config/workflow":
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.Error(w, "unexpected", http.StatusNotFound)
				}
			}))
			defer server.Close()
			withWfSettingsMockClient(t, server, "workflow")
			oldIOS, oldAndroid := workflowAppIOS, workflowAppAndroid
			workflowAppIOS, workflowAppAndroid = "", ""
			t.Cleanup(func() { workflowAppIOS, workflowAppAndroid = oldIOS, oldAndroid })
			handler := map[string]func(*cobra.Command, []string) error{"show": runWorkflowAppShow, "set": runWorkflowAppSet, "clear": runWorkflowAppClear, "disable": runWorkflowAppDisable, "enable": runWorkflowAppEnable}[operation]
			cmd := newLeafCommand(operation, handler)
			cmd.Flags().Bool("ios-automatic", false, "")
			if operation == "set" {
				if err := cmd.Flags().Set("ios-automatic", "true"); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmd.Root().PersistentFlags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			cmd.SetOut(&output)
			if err := handler(cmd, []string{"workflow"}); err != nil {
				t.Fatal(err)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(output.Bytes(), &body); err != nil {
				t.Fatalf("JSON output=%q err=%v", output.String(), err)
			}
			turnedOff := operation == "clear" || operation == "disable"
			if body["workflow_id"] != "workflow" || body["override_build_config"] != !turnedOff {
				t.Fatal(body)
			}
			if !turnedOff && body["build_config"] == nil {
				t.Fatal("missing selectors")
			}
		})
	}
}

func TestWorkflowClearRemovesAndDisableRetainsStoredSelectors(t *testing.T) {
	for _, operation := range []string{"clear", "disable"} {
		t.Run(operation, func(t *testing.T) {
			var request map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "PUT" || r.URL.Path != "/api/v1/workflows/update_build_config/workflow" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			withWfSettingsMockClient(t, server, "workflow")
			handler := map[string]func(*cobra.Command, []string) error{"clear": runWorkflowAppClear, "disable": runWorkflowAppDisable}[operation]
			if err := handler(newLeafCommand(operation, handler), []string{"workflow"}); err != nil {
				t.Fatal(err)
			}
			if request["override_build_config"] != false {
				t.Fatal(request)
			}
			buildConfig, sent := request["build_config"]
			if operation == "clear" && (!sent || buildConfig != nil) {
				t.Fatalf("clear kept saved selectors: %v", request)
			}
			if operation == "disable" && sent {
				t.Fatalf("disable erased saved selectors: %v", request)
			}
		})
	}
}

func TestWorkflowSavedAppsIgnoreEmptySlots(t *testing.T) {
	if workflowHasSavedApps(nil) || workflowHasSavedApps(map[string]interface{}{"ios_build": nil, "android_build": nil}) {
		t.Fatal("empty slots reported as saved apps")
	}
	if !workflowHasSavedApps(map[string]interface{}{"android_build": map[string]interface{}{"app_id": "app"}}) {
		t.Fatal("saved app not reported")
	}
}

func TestWorkflowSavedBranchChoiceRetainsOtherPlatform(t *testing.T) {
	const branchID = "11111111-1111-4111-8111-111111111111"
	var request map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/workflows/get_workflow_info":
			_, _ = w.Write([]byte(`{"id":"workflow","build_config":{"ios_build":{"app_id":"ios-app","branch_id":"old-branch","build_selection":"pinned","pinned_build_id":"old-build"},"android_build":{"app_id":"android-app","pinned_version":"1.0"}}}`))
		case "/api/v1/apps/ios-app/branches":
			if r.URL.Query().Get("name") != "main" {
				t.Error(r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"branches":[{"id":"` + branchID + `","app_id":"ios-app","name":"main"}]}`))
		case "/api/v1/apps/ios-app":
			_, _ = w.Write([]byte(`{"id":"ios-app","name":"Shop iOS","platform":"ios","default_branch_id":"` + branchID + `"}`))
		case "/api/v1/apps/ios-app/branches/" + branchID:
			_, _ = w.Write([]byte(`{"id":"` + branchID + `","app_id":"ios-app","name":"main"}`))
		case "/api/v1/workflows/update_build_config/workflow":
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	withWfSettingsMockClient(t, server, "workflow")
	oldIOS, oldAndroid := workflowAppIOS, workflowAppAndroid
	workflowAppIOS, workflowAppAndroid = "", ""
	t.Cleanup(func() { workflowAppIOS, workflowAppAndroid = oldIOS, oldAndroid })
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().String("ios-branch", "", "")
	cmd.Flags().String("ios-build-selection", "", "")
	cmd.Flags().String("ios-build-id", "", "")
	cmd.Flags().Bool("ios-automatic", false, "")
	if err := cmd.Flags().Set("ios-branch", "main"); err != nil {
		t.Fatal(err)
	}
	output := captureStdoutAndStderr(t, func() {
		if err := runWorkflowAppSet(cmd, []string{"workflow"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, line := range []string{"iOS App:     Shop iOS (ios-app)", "Branch: main (" + branchID + ") · Automatic"} {
		if !strings.Contains(output, line) {
			t.Fatalf("set did not echo %q:\n%s", line, output)
		}
	}
	if strings.Contains(output, "Android App:") {
		t.Fatalf("set echoed a platform it did not change:\n%s", output)
	}
	config := request["build_config"].(map[string]interface{})
	ios := config["ios_build"].(map[string]interface{})
	android := config["android_build"].(map[string]interface{})
	if ios["branch_id"] != branchID || ios["build_selection"] != "inherit" || ios["pinned_build_id"] != nil {
		t.Fatal(ios)
	}
	if android["pinned_version"] != "1.0" {
		t.Fatal(android)
	}
}

func TestWorkflowSameAppPolicyChangeRetainsSavedBranch(t *testing.T) {
	const branchID = "11111111-1111-4111-8111-111111111111"
	var request map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/android-app":
			_, _ = w.Write([]byte(`{"id":"android-app","name":"App","platform":"android","versions_count":1,"latest_version":"1.0"}`))
		case "/api/v1/apps/android-app/branches/" + branchID:
			_, _ = w.Write([]byte(`{"id":"` + branchID + `","app_id":"android-app","name":"staging"}`))
		case "/api/v1/workflows/get_workflow_info":
			_, _ = w.Write([]byte(`{"id":"workflow","build_config":{"android_build":{"app_id":"android-app","branch_id":"` + branchID + `","build_selection":"pinned","pinned_build_id":"22222222-2222-4222-8222-222222222222","pinned_version":"1.0"}}}`))
		case "/api/v1/workflows/update_build_config/workflow":
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	withWfSettingsMockClient(t, server, "workflow")
	oldIOS, oldAndroid := workflowAppIOS, workflowAppAndroid
	workflowAppIOS, workflowAppAndroid = "", "android-app"
	t.Cleanup(func() { workflowAppIOS, workflowAppAndroid = oldIOS, oldAndroid })
	cmd := newLeafCommand("set", runWorkflowAppSet)
	cmd.Flags().String("android-build-selection", "", "")
	if err := cmd.Flags().Set("android-build-selection", "latest"); err != nil {
		t.Fatal(err)
	}
	output := captureStdoutAndStderr(t, func() {
		if err := runWorkflowAppSet(cmd, []string{"workflow"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "Android App: App (android-app)") || !strings.Contains(output, "Branch: staging ("+branchID+") · Latest build") {
		t.Fatalf("set did not echo the saved choice:\n%s", output)
	}
	selection := request["build_config"].(map[string]interface{})["android_build"].(map[string]interface{})
	if selection["branch_id"] != branchID || selection["build_selection"] != "latest" || selection["pinned_build_id"] != nil {
		t.Fatalf("same app dropped saved branch: %v", selection)
	}
	if _, exists := selection["pinned_version"]; exists {
		t.Fatalf("policy update kept legacy pin: %v", selection)
	}
}

func TestWorkflowAppShowNamesEachPlatform(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/workflows/get_workflow_info":
			_, _ = w.Write([]byte(`{"id":"workflow","override_build_config":true,"build_config":{"ios_build":{"app_id":"ios-app"}}}`))
		case "/api/v1/apps/ios-app":
			http.Error(w, `{"detail":"App not found"}`, http.StatusNotFound)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	withWfSettingsMockClient(t, server, "workflow")
	output := captureStdoutAndStderr(t, func() {
		if err := runWorkflowAppShow(newLeafCommand("show", runWorkflowAppShow), []string{"workflow"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, line := range []string{"iOS App:     ios-app", "Branch: Default branch", "Android App: (not set)"} {
		if !strings.Contains(output, line) {
			t.Fatalf("missing %q in:\n%s", line, output)
		}
	}
}

func TestWorkflowAppShowNamesTheSavedBranchAndBuild(t *testing.T) {
	const (
		appID    = "11111111-1111-4111-8111-111111111111"
		branchID = "22222222-2222-4222-8222-222222222222"
		buildID  = "33333333-3333-4333-8333-333333333333"
	)
	for _, test := range []struct {
		name, selection string
		lookupsFail     bool
		want, unwanted  []string
	}{
		{"default branch", `{"app_id":"` + appID + `","build_selection":"inherit","branch_id":null,"pinned_build_id":null}`, false,
			[]string{"iOS App:     Shop iOS (" + appID + ")", "Branch: Default branch"}, []string{"·", "Legacy"}},
		{"automatic", `{"app_id":"` + appID + `","build_selection":"inherit","branch_id":"` + branchID + `"}`, false,
			[]string{"Branch: main (" + branchID + ") · Automatic"}, nil},
		{"latest build", `{"app_id":"` + appID + `","build_selection":"latest","branch_id":"` + branchID + `"}`, false,
			[]string{"Branch: main (" + branchID + ") · Latest build"}, nil},
		{"exact build keeps its mirrored version out of the legacy line", `{"app_id":"` + appID + `","build_selection":"pinned","branch_id":"` + branchID + `","pinned_build_id":"` + buildID + `","pinned_version":"v1.2"}`, false,
			[]string{"Branch: main (" + branchID + ") · Exact build v1.2 (" + buildID + ")"}, []string{"Legacy version pin"}},
		{"legacy version pin", `{"app_id":"` + appID + `","pinned_version":"v1"}`, false,
			[]string{"Branch: Default branch", "Legacy version pin: v1"}, nil},
		{"failed lookups fall back to IDs", `{"app_id":"` + appID + `","build_selection":"pinned","branch_id":"` + branchID + `","pinned_build_id":"` + buildID + `"}`, true,
			[]string{"iOS App:     " + appID + "\n", "Branch: " + branchID + " · Exact build " + buildID + "\n"}, []string{"()"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/workflows/get_workflow_info" {
					_, _ = w.Write([]byte(`{"id":"workflow","override_build_config":true,"build_config":{"ios_build":` + test.selection + `}}`))
					return
				}
				if test.lookupsFail {
					http.Error(w, `{"detail":"unavailable"}`, http.StatusServiceUnavailable)
					return
				}
				switch r.URL.Path {
				case "/api/v1/apps/" + appID:
					_, _ = w.Write([]byte(`{"id":"` + appID + `","name":"Shop iOS","platform":"ios","default_branch_id":"` + branchID + `"}`))
				case "/api/v1/apps/" + appID + "/branches/" + branchID:
					_, _ = w.Write([]byte(`{"id":"` + branchID + `","app_id":"` + appID + `","name":"main"}`))
				case "/api/v1/apps/builds/" + buildID:
					_, _ = w.Write([]byte(`{"id":"` + buildID + `","version":"v1.2"}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.Error(w, "unexpected", http.StatusNotFound)
				}
			}))
			defer server.Close()
			withWfSettingsMockClient(t, server, "workflow")
			output := captureStdoutAndStderr(t, func() {
				if err := runWorkflowAppShow(newLeafCommand("show", runWorkflowAppShow), []string{"workflow"}); err != nil {
					t.Fatal(err)
				}
			})
			for _, want := range test.want {
				if !strings.Contains(output, want) {
					t.Fatalf("missing %q in:\n%s", want, output)
				}
			}
			for _, unwanted := range append(test.unwanted, "Default build", "build selection:") {
				if strings.Contains(output, unwanted) {
					t.Fatalf("unexpected %q in:\n%s", unwanted, output)
				}
			}
		})
	}
}
