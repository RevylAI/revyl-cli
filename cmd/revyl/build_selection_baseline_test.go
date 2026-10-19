package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestExplicitUploadDestinationPreservesSourceGitAndCIProvenance(t *testing.T) {
	project := executionSourceCheckout(t)
	runGit(t, project, "-c", "user.email=fixture@example.com", "-c", "user.name=Fixture", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	headBytes, err := exec.Command("git", "-C", project, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_EVENT_PATH", "")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	t.Setenv("GITHUB_HEAD_REF", "Feature/git-branch")
	t.Setenv("GITHUB_SHA", head)
	t.Setenv("REVYL_INTERNAL_GIT_BRANCH", "unrelated/ambient")
	artifact := filepath.Join(project, "app.apk")
	if err := os.WriteFile(artifact, []byte("fixture-apk"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []struct {
		name, branchID, branchName string
	}{
		{name: "explicit_name", branchName: "release"},
		{name: "frozen_id", branchID: "22222222-2222-4222-8222-222222222222"},
	} {
		t.Run(selection.name, func(t *testing.T) {
			var destination, body map[string]any
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/11111111-1111-4111-8111-111111111111/builds/upload-session":
					if err := json.NewDecoder(r.Body).Decode(&destination); err != nil {
						t.Error(err)
					}
					fmt.Fprintf(w, `{"upload_id":"frozen-upload","upload_url":%q,"content_type":"application/octet-stream"}`, server.URL+"/artifact")
				case "/artifact":
					w.WriteHeader(http.StatusOK)
				case "/api/v1/apps/11111111-1111-4111-8111-111111111111/builds":
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"detail":"stop after capturing upload metadata"}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cmd := &cobra.Command{Use: "build"}
			cmd.SetContext(context.Background())
			cmd.Flags().String("branch", "release", "")
			_, err := performLocalArtifactUpload(cmd, projectBuildInvocation{
				ProjectRoot: project, Platform: "android",
				AppID:    "11111111-1111-4111-8111-111111111111",
				BranchID: selection.branchID, BranchName: selection.branchName,
			}, artifact, 0, api.NewClientWithBaseURL("test", server.URL), false, false)
			if err == nil || destination == nil || body == nil {
				t.Fatalf("expected captured upload boundary, got %v", err)
			}
			branchID, _ := destination["branch_id"].(string)
			branchName, _ := destination["branch_name"].(string)
			if branchID != selection.branchID || branchName != selection.branchName || destination["git_branch_name"] != nil {
				t.Fatalf("explicit destination changed: %#v", destination)
			}
			if body["upload_id"] != "frozen-upload" || body["branch_id"] != nil || body["branch_name"] != nil || body["git_branch_name"] != nil {
				t.Fatalf("finalization replaced frozen upload destination: %#v", body)
			}
			metadata, ok := body["metadata"].(map[string]any)
			if !ok {
				t.Fatal("source metadata missing")
			}
			gitMetadata, ok := metadata["git"].(map[string]any)
			if !ok || gitMetadata["branch"] != "Feature/git-branch" || gitMetadata["commit"] != head || metadata["scm_branch"] != "Feature/git-branch" || metadata["scm_head_sha"] != head {
				t.Fatalf("destination replaced source provenance: %#v", metadata)
			}
		})
	}
}
