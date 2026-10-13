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

func TestBuildPinFlagIsPublicAndOldFlagsAreHidden(t *testing.T) {
	for _, command := range []*cobra.Command{buildCmd, buildUploadCmd} {
		pin := command.Flags().Lookup("pin")
		if pin == nil || pin.Hidden || pin.Usage != "Pin the branch to the new build" {
			t.Fatalf("%s --pin must be visible: %+v", command.Name(), pin)
		}
		for _, name := range []string{"set-current", "no-set-current"} {
			flag := command.Flags().Lookup(name)
			if flag == nil || !flag.Hidden {
				t.Fatalf("%s --%s must stay accepted and hidden", command.Name(), name)
			}
		}
		help := command.Use + command.Long + command.Example
		if strings.Contains(help, "set-current") {
			t.Fatalf("%s help still recommends a deprecated flag: %s", command.Name(), help)
		}
	}
	if flag := buildRemoteCmd.Flags().Lookup("pin"); flag == nil || !flag.Hidden {
		t.Fatal("deprecated build remote must accept a hidden --pin")
	}
}

func TestResolveBuildPinFlags(t *testing.T) {
	const (
		setCurrentWarning        = "--set-current is deprecated and will be removed; use --pin to pin the branch to the new build"
		setCurrentFalseWarning   = "--set-current=false is deprecated and does nothing; uploads don't pin their branch unless you pass --pin"
		noSetCurrentWarning      = "--no-set-current is deprecated and does nothing; uploads don't pin their branch unless you pass --pin"
		noSetCurrentFalseWarning = "--no-set-current=false is deprecated and does nothing; uploads don't pin their branch unless you pass --pin"
	)
	for _, test := range []struct {
		name     string
		args     []string
		wantPin  bool
		warnings []string
		wantErr  string
	}{
		{name: "no flags leaves the pin alone"},
		{name: "pin", args: []string{"--pin"}, wantPin: true},
		{name: "explicit pin false", args: []string{"--pin=false"}},
		{name: "deprecated set-current pins", args: []string{"--set-current"}, wantPin: true, warnings: []string{setCurrentWarning}},
		{name: "deprecated set-current false does nothing", args: []string{"--set-current=false"}, warnings: []string{setCurrentFalseWarning}},
		{name: "deprecated no-set-current does nothing", args: []string{"--no-set-current"}, warnings: []string{noSetCurrentWarning}},
		{name: "deprecated no-set-current false does nothing", args: []string{"--no-set-current=false"}, warnings: []string{noSetCurrentFalseWarning}},
		{name: "pin with redundant set-current", args: []string{"--pin", "--set-current"}, wantPin: true, warnings: []string{setCurrentWarning}},
		{name: "no-set-current false with pin", args: []string{"--pin", "--no-set-current=false"}, wantPin: true, warnings: []string{noSetCurrentFalseWarning}},
		{name: "pin contradicts no-set-current", args: []string{"--pin", "--no-set-current"}, wantErr: "--pin and --no-set-current conflict"},
		{name: "pin contradicts set-current false", args: []string{"--pin", "--set-current=false"}, wantErr: "--pin and --set-current=false conflict"},
		{name: "pin false contradicts set-current", args: []string{"--pin=false", "--set-current"}, wantErr: "--pin=false and --set-current conflict"},
		{name: "set-current contradicts no-set-current", args: []string{"--set-current", "--no-set-current"}, wantErr: "--set-current and --no-set-current conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := newLeafCommand("upload", runBuildUpload)
			registerBuildPinFlags(cmd)
			if err := cmd.ParseFlags(test.args); err != nil {
				t.Fatal(err)
			}
			var pin bool
			var err error
			stdout, stderr := captureStdoutAndStderrSeparate(t, func() { pin, err = resolveBuildPin(cmd) })
			if stdout != "" {
				t.Fatalf("pin flags wrote to stdout: %q", stdout)
			}
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) || !strings.Contains(err.Error(), "use only --pin to pin the branch to the new build") {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || pin != test.wantPin {
				t.Fatalf("pin = %t, %v; want %t", pin, err, test.wantPin)
			}
			for _, warning := range test.warnings {
				if !strings.Contains(stderr, warning) {
					t.Errorf("stderr missing %q:\n%s", warning, stderr)
				}
			}
			if len(test.warnings) == 0 && stderr != "" {
				t.Fatalf("unexpected stderr: %q", stderr)
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
