package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestSavedTestBuildWritesJSONAfterSuccessfulMutation(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "clear"}[clear], func(t *testing.T) {
			updated := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/tests/get_test_by_id/test":
					_, _ = w.Write([]byte(`{"id":"test","app_id":"app","build_selection":"inherit"}`))
				case "/api/v1/tests/update/test":
					updated = true
					_, _ = w.Write([]byte(`{"id":"test","version":2}`))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.Error(w, "unexpected", http.StatusNotFound)
				}
			}))
			defer server.Close()
			previous := resolveTestClient
			resolveTestClient = func(*cobra.Command, string) (string, *api.Client, error) {
				return "test", api.NewClientWithBaseURL("key", server.URL), nil
			}
			t.Cleanup(func() { resolveTestClient = previous })
			cmd := newLeafCommand("build", runTestBuildSet)
			cmd.Flags().String("branch", "", "")
			cmd.Flags().String("build-selection", "latest", "")
			cmd.Flags().String("build-id", "", "")
			if err := cmd.Root().PersistentFlags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			cmd.SetOut(&output)
			mode := "latest"
			var err error
			if clear {
				mode = "inherit"
				err = runTestBuildClear(cmd, []string{"test"})
			} else {
				err = runTestBuildSet(cmd, []string{"test"})
			}
			if err != nil || !updated {
				t.Fatalf("updated=%t err=%v", updated, err)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(output.Bytes(), &body); err != nil {
				t.Fatalf("invalid JSON result %q: %v", output.String(), err)
			}
			if clear {
				mode = "latest_all_branches"
			}
			if body["test_id"] != "test" || body["build_selection"] != mode || body["branch_id"] != nil || body["pinned_build_id"] != nil {
				t.Fatalf("result=%v", body)
			}
		})
	}
}

func TestSavedTestBuildShowSummarizesSelection(t *testing.T) {
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, test := range []struct {
		name, savedTest string
		jsonOutput      bool
		want            []string
	}{
		{"legacy pin", `{"id":"test","app_id":"app","build_selection":"inherit","pinned_version":"1.2.3"}`, false, []string{"Not recorded (saved before branches)", "Exact build 1.2.3"}},
		{"legacy pin with branch reads disabled", `{"id":"test","app_id":"app","pinned_version":"1.2.3"}`, false, []string{"Not recorded (saved before branches)", "Exact build 1.2.3"}},
		{"legacy JSON", `{"id":"test","app_id":"app","pinned_version":"1.2.3"}`, true, []string{`"pinned_version":"1.2.3"`, `"branch_id":null`, `"build_selection":"inherit"`, `"pinned_build_id":null`}},
		{"canonical exact supersedes legacy", `{"id":"test","app_id":"app","branch_id":"` + branchID + `","build_selection":"pinned","pinned_build_id":"` + buildID + `","pinned_version":"old"}`, true, []string{`"build_selection":"pinned"`, `"pinned_build_id":"` + buildID + `"`}},
		{"canonical branch supersedes legacy", `{"id":"test","app_id":"app","branch_id":"` + branchID + `","build_selection":"inherit","pinned_version":"old"}`, true, []string{`"build_selection":"inherit"`, `"branch_id":"` + branchID + `"`}},
		{"canonical choice supersedes legacy", `{"id":"test","app_id":"app","build_selection":"latest","pinned_version":"1.2.3"}`, true, []string{`"build_selection":"latest"`}},
		{"default branch", `{"id":"test","app_id":"app","build_selection":"inherit"}`, false, []string{"Default branch", "Automatic"}},
		{"all branches", `{"id":"test","app_id":"app","build_selection":"latest_all_branches"}`, false, []string{"All branches", "Latest build"}},
		{"latest on a branch", `{"id":"test","app_id":"app","branch_id":"` + branchID + `","build_selection":"latest"}`, false, []string{"staging (" + branchID + ")", "Latest build"}},
		{"pinned", `{"id":"test","app_id":"app","branch_id":"` + branchID + `","build_selection":"pinned","pinned_build_id":"` + buildID + `"}`, false, []string{"staging (" + branchID + ")", "Exact build 1.4.2 (" + buildID + ")"}},
		{"json", `{"id":"test","app_id":"app","branch_id":"` + branchID + `","build_selection":"pinned","pinned_build_id":"` + buildID + `"}`, true, []string{`"test_id":"test"`, `"pinned_build_id":"` + buildID + `"`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/tests/get_test_by_id/test":
					_, _ = w.Write([]byte(test.savedTest))
				case r.URL.Path == "/api/v1/apps/app/branches/"+branchID && !test.jsonOutput:
					_, _ = w.Write([]byte(`{"id":"` + branchID + `","app_id":"app","name":"staging"}`))
				case r.URL.Path == "/api/v1/apps/builds/"+buildID && !test.jsonOutput:
					_, _ = w.Write([]byte(`{"id":"` + buildID + `","version":"1.4.2"}`))
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.Error(w, "unexpected", http.StatusNotFound)
				}
			}))
			defer server.Close()
			previous := resolveTestClient
			resolveTestClient = func(*cobra.Command, string) (string, *api.Client, error) {
				return "test", api.NewClientWithBaseURL("key", server.URL), nil
			}
			t.Cleanup(func() { resolveTestClient = previous })
			cmd := newLeafCommand("show", runTestBuildShow)
			if test.jsonOutput {
				if err := cmd.Root().PersistentFlags().Set("json", "true"); err != nil {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			_, stderr := captureStdoutAndStderrSeparate(t, func() {
				if err := runTestBuildShow(cmd, []string{"test"}); err != nil {
					t.Fatal(err)
				}
			})
			output := stderr
			if test.jsonOutput {
				if !json.Valid(stdout.Bytes()) {
					t.Fatalf("invalid JSON: %s", stdout.String())
				}
				output = strings.ReplaceAll(stdout.String(), " ", "")
				if stderr != "" {
					t.Fatalf("JSON mode wrote to stderr: %q", stderr)
				}
			} else if stdout.Len() != 0 {
				t.Fatalf("summary wrote to stdout: %q", stdout.String())
			}
			if strings.HasPrefix(test.name, "canonical") && strings.Contains(output, "pinned_version") {
				t.Fatalf("canonical output retained superseded version: %s", output)
			}
			for _, want := range test.want {
				if !strings.Contains(output, want) {
					t.Fatalf("output missing %q:\n%s", want, output)
				}
			}
			for _, unwanted := range []string{"Default build", "Branch default", "Pinned to"} {
				if strings.Contains(output, unwanted) {
					t.Fatalf("output still says %q:\n%s", unwanted, output)
				}
			}
		})
	}
}

func TestSavedTestBuildShowMarksAnUnavailablePin(t *testing.T) {
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, test := range []struct {
		name        string
		buildStatus int
		wantErr     bool
	}{
		{"deleted build", http.StatusNotFound, false},
		{"unreadable build", http.StatusForbidden, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/tests/get_test_by_id/test":
					_, _ = w.Write([]byte(`{"id":"test","app_id":"app","branch_id":"` + branchID + `","build_selection":"pinned","pinned_build_id":"` + buildID + `"}`))
				case "/api/v1/apps/app/branches/" + branchID:
					_, _ = w.Write([]byte(`{"id":"` + branchID + `","app_id":"app","name":"staging"}`))
				case "/api/v1/apps/builds/" + buildID:
					w.WriteHeader(test.buildStatus)
					_, _ = w.Write([]byte(`{"detail":"Build not found"}`))
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.Error(w, "unexpected", http.StatusNotFound)
				}
			}))
			defer server.Close()
			previous := resolveTestClient
			resolveTestClient = func(*cobra.Command, string) (string, *api.Client, error) {
				return "test", api.NewClientWithBaseURL("key", server.URL), nil
			}
			t.Cleanup(func() { resolveTestClient = previous })
			cmd := newLeafCommand("show", runTestBuildShow)
			var err error
			_, stderr := captureStdoutAndStderrSeparate(t, func() {
				err = runTestBuildShow(cmd, []string{"test"})
			})
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "read saved exact build") {
					t.Fatalf("err = %v, want a saved exact build read failure", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"staging (" + branchID + ")", "Exact build " + buildID + " (unavailable)"} {
				if !strings.Contains(stderr, want) {
					t.Fatalf("output missing %q:\n%s", want, stderr)
				}
			}
		})
	}
}

func TestSavedTestBuildAllBranchesDropsTheSavedBranch(t *testing.T) {
	const branchID = "22222222-2222-4222-8222-222222222222"
	var update map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/get_test_by_id/test":
			_, _ = w.Write([]byte(`{"id":"test","app_id":"app","branch_id":"` + branchID + `","build_selection":"latest"}`))
		case "/api/v1/tests/update/test":
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"id":"test","version":2}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	previous := resolveTestClient
	resolveTestClient = func(*cobra.Command, string) (string, *api.Client, error) {
		return "test", api.NewClientWithBaseURL("key", server.URL), nil
	}
	t.Cleanup(func() { resolveTestClient = previous })
	newSetCommand := func(t *testing.T, args ...string) *cobra.Command {
		t.Helper()
		cmd := newLeafCommand("set", runTestBuildSet)
		cmd.Flags().String("branch", "", "")
		cmd.Flags().String("build-selection", "", "")
		cmd.Flags().String("build-id", "", "")
		cmd.Flags().Bool("all-branches", false, "")
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	if err := runTestBuildSet(newSetCommand(t, "--all-branches"), []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if update["build_selection"] != "latest_all_branches" || update["branch_id"] != nil || update["pinned_build_id"] != nil {
		t.Fatalf("saved all-branches selection = %v", update)
	}
	for _, args := range [][]string{{"--all-branches", "--branch", "main"}, {"--all-branches", "--build-selection", "inherit"}} {
		update = nil
		err := runTestBuildSet(newSetCommand(t, args...), []string{"test"})
		if err == nil || !strings.Contains(err.Error(), "--all-branches cannot be combined with") || update != nil {
			t.Fatalf("%v: err=%v saved=%v", args, err, update)
		}
	}
}

func TestDevLoopRefusesAllBranches(t *testing.T) {
	if err := rejectAllBranchesSelection("latest", "unused"); err != nil {
		t.Fatal(err)
	}
	err := rejectAllBranchesSelection("latest_all_branches", "a dev loop follows one branch")
	if err == nil || !strings.Contains(err.Error(), "latest_all_branches isn't available here: a dev loop follows one branch") {
		t.Fatalf("error = %v", err)
	}
}
