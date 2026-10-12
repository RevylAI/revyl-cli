package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestBranchPolicyNoSetCurrentStaysPublic(t *testing.T) {
	for _, command := range []*cobra.Command{buildCmd, buildUploadCmd} {
		flag := command.Flags().Lookup("no-set-current")
		if flag == nil || flag.Hidden || flag.Deprecated != "" {
			t.Fatalf("%s --no-set-current must remain visible and nondeprecated", command.Name())
		}
	}
}

func TestBranchPolicyLegacySetCurrentStaysHiddenWithoutWarning(t *testing.T) {
	flag := buildUploadCmd.Flags().Lookup("set-current")
	if flag == nil || !flag.Hidden || flag.Deprecated != "" {
		t.Fatal("build upload --set-current must stay accepted, hidden and without a deprecation warning")
	}
}

func TestBuildUploadHonorsLegacySetCurrent(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	previousSetCurrent, previousNoSetCurrent := buildSetCurr, buildNoSetCurrent
	previousFile, previousURL := uploadFileFlag, uploadURLFlag
	t.Cleanup(func() {
		buildSetCurr, buildNoSetCurrent = previousSetCurrent, previousNoSetCurrent
		uploadFileFlag, uploadURLFlag = previousFile, previousURL
	})
	uploadFileFlag, uploadURLFlag = "app.apk", "https://example.test/app.apk"
	for _, test := range []struct {
		name    string
		args    []string
		wantPin bool
		wantErr string
	}{
		{name: "default pins", wantPin: true},
		{name: "legacy opt out", args: []string{"--set-current=false"}},
		{name: "legacy opt in", args: []string{"--set-current"}, wantPin: true},
		{name: "public opt out", args: []string{"--no-set-current"}},
		{name: "explicit public false", args: []string{"--no-set-current=false"}, wantPin: true},
		{name: "contradictory flags", args: []string{"--set-current", "--no-set-current"}, wantErr: "use either --set-current or --no-set-current"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := newLeafCommand("upload", runBuildUpload)
			cmd.Flags().BoolVar(&buildSetCurr, "set-current", true, "")
			cmd.Flags().BoolVar(&buildNoSetCurrent, "no-set-current", false, "")
			if err := cmd.ParseFlags(test.args); err != nil {
				t.Fatal(err)
			}
			err := runBuildUpload(cmd, nil)
			wantErr := test.wantErr
			if wantErr == "" {
				wantErr = "--file and --url are mutually exclusive"
				if buildSetCurr != test.wantPin {
					t.Fatalf("upload pins branch = %t, want %t", buildSetCurr, test.wantPin)
				}
			}
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("upload error = %v, want %q", err, wantErr)
			}
		})
	}
}

func TestBranchPolicyListAndUnpin(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const pinnedID = "33333333-3333-4333-8333-333333333333"
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appID {
			fmt.Fprintf(w, `{"id":%q,"name":"App","platform":"android","default_branch_id":%q}`, appID, branchID)
		} else if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/apps/"+appID+"/branches/"+branchID {
			patches++
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 1 || string(body["pinned_build_id"]) != "null" {
				t.Errorf("unpin must explicitly select Latest: %v", body)
			}
			fmt.Fprintf(w, `{"id":%q,"name":"default","pinned_build_id":null}`, branchID)
		} else if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appID+"/branches" {
			fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"default","pinned_build_id":null},{"id":"staging","name":"staging","pinned_build_id":%q}],"total":2}`, branchID, pinnedID)
		} else {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	for _, action := range []string{"list", "unpin"} {
		registered, _, err := appCmd.Find([]string{"branch", action})
		if err != nil {
			t.Fatal(err)
		}
		cmd := newLeafCommand(action, registered.RunE)
		cmd.Flags().String("app", appID, "")
		cmd.Flags().Int("page", 1, "")
		cmd.Flags().Int("page-size", 50, "")
		output := captureStdoutAndStderr(t, func() {
			if err := registered.RunE(cmd, []string{"default"}); err != nil {
				t.Fatal(err)
			}
		})
		if action == "list" && (!strings.Contains(output, "Latest") || !strings.Contains(output, pinnedID) || strings.Contains(output, "Current build")) {
			t.Fatalf("branch defaults must be Latest or pinned: %s", output)
		}
	}
	if patches != 1 {
		t.Fatalf("unpin PATCH requests = %d, want 1", patches)
	}
}

func TestBranchDefaultJSONReportsTheNewDefault(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	defaultSet := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appID:
			fmt.Fprintf(w, `{"id":%q,"name":"App","platform":"android"}`, appID)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appID+"/builds":
			fmt.Fprint(w, `{"items":[],"total":0,"page":1,"page_size":1,"has_next":false}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appID+"/branches":
			fmt.Fprintf(w, `{"branches":[{"id":%q,"app_id":%q,"name":"staging","pinned_build_id":null,"is_default":%t}],"total":1}`, branchID, appID, defaultSet)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/apps/"+appID:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["default_branch_id"] != branchID {
				t.Errorf("default request = %v", body)
			}
			defaultSet = true
			fmt.Fprintf(w, `{"id":%q,"name":"App","platform":"android","default_branch_id":%q}`, appID, branchID)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	registered, _, err := appCmd.Find([]string{"branch", "default"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := newLeafCommand("default", registered.RunE)
	cmd.Flags().String("app", appID, "")
	cmd.Flags().Bool("json", true, "")
	output := captureStdout(t, func() {
		if err := registered.RunE(cmd, []string{"staging"}); err != nil {
			t.Fatal(err)
		}
	})
	result := parseJSON(t, output)
	if !defaultSet || result["id"] != branchID || result["is_default"] != true {
		t.Fatalf("default --json = %v (default set: %t)", result, defaultSet)
	}
}
