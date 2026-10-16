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

const webUploadTestAppID = "11111111-1111-4111-8111-111111111111"
const webUploadTestBuildID = "22222222-2222-4222-8222-222222222222"

func newWebUploadTestCommand(t *testing.T, appFlag, urlFlag string, args ...string) *cobra.Command {
	t.Helper()
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("BUILDKITE", "")
	previous := []string{uploadAppFlag, uploadURLFlag, uploadFileFlag}
	previousJSON := buildUploadJSON
	t.Cleanup(func() {
		uploadAppFlag, uploadURLFlag, uploadFileFlag = previous[0], previous[1], previous[2]
		buildUploadJSON = previousJSON
	})
	uploadAppFlag, uploadURLFlag, uploadFileFlag = appFlag, urlFlag, ""
	buildUploadJSON = true
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("dev", false, "")
	for _, name := range []string{"header", "version", "name", "branch"} {
		cmd.Flags().String(name, "", "")
	}
	for _, name := range []string{buildPinFlagName, buildSetCurrentFlagName, buildNoSetCurrentFlagName} {
		cmd.Flags().Bool(name, false, "")
	}
	cmd.Flags().Bool("preview", false, "")
	registerBuildUploadSCMFlags(cmd)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	if err := applyBuildUploadSCMFlags(cmd); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestWebURLUploadSendsPhoneUploadSCMIdentityForConfiguredApp(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, "project: {id: 33333333-3333-4333-8333-333333333333}\nbuild:\n  profiles:\n    preview:\n      web:\n        app_id: "+webUploadTestAppID+"\n")
	withWorkingDirectory(t, repository)
	sha := strings.Repeat("a", 40)
	var requests int
	var body map[string]any
	var headers http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/"+webUploadTestAppID+"/builds/external" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		requests++
		headers = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": webUploadTestBuildID, "app_id": webUploadTestAppID, "artifact_kind": "web_external", "version": "web-7", "was_reused": true})
	}))
	t.Cleanup(backend.Close)
	t.Setenv("REVYL_BACKEND_URL", backend.URL)

	cmd := newWebUploadTestCommand(t, "", "https://pr-42.example.test/app", "--repo", "acme/web", "--commit", sha, "--pr", "42")
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := runWebURLUpload(cmd, "fixture-key"); err != nil {
		t.Fatal(err)
	}

	wantBody := map[string]any{
		"url":        "https://pr-42.example.test/app",
		"commit_sha": sha,
		"metadata": map[string]any{
			"ci_system":         "external",
			"github_repository": "acme/web",
			"scm_repo":          "acme/web",
			"scm_provider":      "github",
			"scm_namespace":     "acme",
			"scm_project":       "web",
			"scm_review_number": float64(42),
			"pr_number":         float64(42),
			"scm_head_sha":      sha,
			"scm_platform":      "web",
		},
	}
	if requests != 1 || !jsonEqual(t, body, wantBody) {
		t.Fatalf("requests=%d body=%#v", requests, body)
	}
	if headers.Get("X-CI-Repository") != "acme/web" || headers.Get("X-CI-Commit-SHA") != sha || headers.Get("X-CI-PR-Number") != "42" {
		t.Fatalf("CI headers = %#v", headers)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, output.String())
	}
	wantResult := map[string]any{
		"platform":   "web",
		"app_id":     webUploadTestAppID,
		"version":    "web-7",
		"version_id": webUploadTestBuildID,
		"was_reused": true,
		"source_url": "https://pr-42.example.test/app",
		"commit_sha": sha,
		"review":     map[string]any{"repository": "acme/web", "pr_number": float64(42), "head_sha": sha},
		"defaults_applied": []any{
			map[string]any{"flag": "app", "value": webUploadTestAppID, "reason": "project_web_profile"},
		},
	}
	if !jsonEqual(t, result, wantResult) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestWebURLUploadWithoutSCMSendsOnlyTheURL(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	var body map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": webUploadTestBuildID, "app_id": webUploadTestAppID, "artifact_kind": "web_external", "version": "web-1"})
	}))
	t.Cleanup(backend.Close)
	t.Setenv("REVYL_BACKEND_URL", backend.URL)

	cmd := newWebUploadTestCommand(t, webUploadTestAppID, "https://example.test")
	cmd.SetOut(&bytes.Buffer{})
	if err := runWebURLUpload(cmd, "fixture-key"); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, body, map[string]any{"url": "https://example.test"}) {
		t.Fatalf("body = %#v", body)
	}
}

func TestWebURLUploadSendsTheSelectedBranch(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	var body map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": webUploadTestBuildID, "app_id": webUploadTestAppID, "artifact_kind": "web_external", "version": "web-1"})
	}))
	t.Cleanup(backend.Close)
	t.Setenv("REVYL_BACKEND_URL", backend.URL)

	cmd := newWebUploadTestCommand(t, webUploadTestAppID, "https://pr-42.example.test", "--branch", "pr-42")
	cmd.SetOut(&bytes.Buffer{})
	if err := runWebURLUpload(cmd, "fixture-key"); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, body, map[string]any{"url": "https://pr-42.example.test", "branch_name": "pr-42"}) {
		t.Fatalf("body = %#v", body)
	}
}

func TestWebURLUploadRejectsInvalidInputBeforeTransport(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_BACKEND_URL", "http://127.0.0.1:1")
	for _, test := range []struct {
		name string
		app  string
		url  string
		args []string
		want string
	}{
		{"missing url", webUploadTestAppID, "", nil, "pass --url https://"},
		{"http url", webUploadTestAppID, "http://example.test", nil, "HTTPS URL"},
		{"query string", webUploadTestAppID, "https://example.test/?token=x", nil, "query strings"},
		{"credentials", webUploadTestAppID, "https://user:pass@example.test", nil, "credentials"},
		{"header", webUploadTestAppID, "https://example.test", []string{"--header", "A: b"}, "--header is not available with --platform web"},
		{"version", webUploadTestAppID, "https://example.test", []string{"--version", "1"}, "--version is not available with --platform web"},
		{"pin", webUploadTestAppID, "https://example.test", []string{"--pin"}, "--pin is not available with --platform web"},
		{"deprecated set-current", webUploadTestAppID, "https://example.test", []string{"--set-current"}, "--set-current is not available with --platform web"},
		{"deprecated no-set-current", webUploadTestAppID, "https://example.test", []string{"--no-set-current"}, "--no-set-current is not available with --platform web"},
		{"no configured app", "", "https://example.test", nil, "build.profiles.<name>.web.app_id"},
		{"empty branch", webUploadTestAppID, "https://example.test", []string{"--branch", ""}, "--branch: branch name can't be empty"},
		{"padded branch", webUploadTestAppID, "https://example.test", []string{"--branch", " pr-42"}, "--branch: branch name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := newWebUploadTestCommand(t, test.app, test.url, test.args...)
			if err := runWebURLUpload(cmd, "fixture-key"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestWebURLUploadRequiresRepoWithCommitLikePhoneUploads(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	registerBuildUploadSCMFlags(cmd)
	if err := cmd.ParseFlags([]string{"--commit", strings.Repeat("a", 40)}); err != nil {
		t.Fatal(err)
	}
	if err := applyBuildUploadSCMFlags(cmd); err == nil || !strings.Contains(err.Error(), "--repo owner/name") {
		t.Fatalf("error = %v", err)
	}
}

func TestPhoneURLUploadStillRejectsHeaderWithoutURL(t *testing.T) {
	if err := validateUploadSourceFlags("", "", []string{"A: b"}); err == nil || err.Error() != "--header requires --url" {
		t.Fatalf("error = %v", err)
	}
}

func jsonEqual(t *testing.T, got, want any) bool {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(gotJSON, wantJSON)
}
