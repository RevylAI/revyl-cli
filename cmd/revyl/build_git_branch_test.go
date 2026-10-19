package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/spf13/cobra"
)

func TestBuildGitBranchUsesEffectiveProjectAndFreezesAcrossRebuild(t *testing.T) {
	project := executionSourceCheckout(t)
	withWorkingDir(t, t.TempDir())
	cmd := &cobra.Command{Use: "build"}
	cmd.SetContext(context.Background())
	cmd.Flags().String("branch", "", "")
	invocation, err := resolveLocalGitBranch(cmd, projectBuildInvocation{ProjectRoot: project})
	if err != nil || invocation.GitBranchName != "Feature/git-branch" || !invocation.GitBranchResolved {
		t.Fatalf("effective project context = %+v, %v", invocation, err)
	}
	setInitTestGitBranch(t, project, "changed/during-build")
	frozen, err := resolveLocalGitBranch(cmd, invocation)
	if err != nil || frozen.GitBranchName != invocation.GitBranchName {
		t.Fatalf("rebuild context changed: %+v, %v", frozen, err)
	}
	if err := cmd.Flags().Set("branch", "release"); err != nil {
		t.Fatal(err)
	}
	explicit, err := resolveLocalGitBranch(cmd, projectBuildInvocation{ProjectRoot: project})
	if err != nil || explicit.BranchName != "release" || explicit.GitBranchName != "" {
		t.Fatalf("explicit branch = %+v, %v", explicit, err)
	}
}

func TestLocalBuildCapturesBranchBeforeRecipeChangesCheckout(t *testing.T) {
	project := executionSourceCheckout(t)
	artifact := "app.apk"
	if err := os.WriteFile(filepath.Join(project, artifact), []byte("fixture-apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	var request api.BuildUploadSessionRequest
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/11111111-1111-4111-8111-111111111111/builds/upload-session" {
			t.Errorf("unexpected request %s", r.URL)
		} else {
			calls++
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"fixture stop after upload destination"}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	cmd := &cobra.Command{Use: "build"}
	cmd.SetContext(context.Background())
	cmd.Flags().String("branch", "", "")
	invocation := projectBuildInvocation{
		ProjectRoot: project, AppID: "11111111-1111-4111-8111-111111111111", Platform: "android",
		Recipe: config.EffectiveBuildRecipe{
			BuildCommands: config.CommandStepItems([]string{"git symbolic-ref HEAD refs/heads/changed-during-recipe"}),
			OutputPath:    &artifact,
		},
	}
	_, err := performLocalBuild(cmd, invocation, "test", true, false, false, &buildProgress{})
	if err == nil || calls != 1 || stringValue(request.GitBranchName) != "Feature/git-branch" || request.BranchName != nil {
		t.Fatalf("recipe retargeted upload: calls=%d request=%+v err=%v", calls, request, err)
	}
	current, err := commandGitBranch(cmd, project)
	if err != nil || current != "changed-during-recipe" {
		t.Fatalf("recipe did not change checkout: %q, %v", current, err)
	}
}

func TestBuildGitBranchSkipsFrozenDestinationAndRejectsMismatchedArtifactIdentity(t *testing.T) {
	project := executionSourceCheckout(t)
	cmd := &cobra.Command{Use: "build"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)
	for _, invocation := range []projectBuildInvocation{{BranchID: "frozen-id"}, {BranchName: "saved-branch"}} {
		resolved, err := resolveLocalGitBranch(cmd, invocation)
		if err != nil || resolved.BranchID != invocation.BranchID || resolved.BranchName != invocation.BranchName || resolved.GitBranchName != "" {
			t.Fatalf("frozen selection caused inference: %+v, %v", resolved, err)
		}
	}
	if _, err := resolveLocalGitBranch(cmd, projectBuildInvocation{ProjectRoot: project}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
	cmd.SetContext(context.Background())
	cmd.Flags().String("repo", "", "")
	cmd.Flags().String("commit", "", "")
	cmd.Flags().Int("pr", 0, "")
	_ = cmd.Flags().Set("repo", "example/different")
	_ = cmd.Flags().Set("commit", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	got, err := commandGitBranch(cmd, project)
	if err != nil || got != "" {
		t.Fatalf("external artifact inherited caller branch: %q, %v", got, err)
	}
}

func TestRemoteBuildRequestPreservesRawGitBranchSeparately(t *testing.T) {
	var source api.RemoteBuildRequest_Source
	if err := source.FromRemoteBuildArchiveSource(api.RemoteBuildArchiveSource{Key: "source-key"}); err != nil {
		t.Fatal(err)
	}
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	resolved := remoteBuildPlatformConfigFromProject(projectBuildInvocation{
		AppID: appID.String(), Platform: "android", Recipe: config.EffectiveBuildRecipe{},
	})
	for _, hint := range []string{"feature/Écran\u00a0", " invalid but irrelevant to explicit branch "} {
		request, err := newRemoteBuildTriggerRequest(source, appID, resolved, remoteBuildOptions{BranchName: "main", GitBranchName: hint})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded api.RemoteBuildRequest
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.GitBranchName == nil || *decoded.GitBranchName != hint || decoded.BranchName == nil || *decoded.BranchName != "main" {
			t.Fatalf("raw context changed: %+v", decoded)
		}
	}
}

func TestBuildListIgnoresGitBranch(t *testing.T) {
	withWorkingDir(t, executionSourceCheckout(t))
	listCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/apps/app/builds" {
			t.Errorf("browsing resolved Git branch: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		listCalls++
		if r.URL.Query().Has("branch_id") || r.URL.Query().Get("page_size") != "10" {
			t.Errorf("wrong browsing scope: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"items":[],"total":0}`))
	}))
	defer server.Close()
	cmd := &cobra.Command{Use: "list"}
	cmd.SetContext(context.Background())
	cmd.Flags().String("branch", "", "")
	cmd.Flags().Bool("all-branches", false, "")
	cmd.Flags().Int("page", 1, "")
	cmd.Flags().Int("limit", 20, "")
	cmd.Flags().Int("page-size", 20, "")
	if err := cmd.Flags().Set("page-size", "10"); err != nil {
		t.Fatal(err)
	}
	err := listBuildVersions(cmd, api.NewClientWithBaseURL("test", server.URL), "app")
	if err != nil || listCalls != 1 {
		t.Fatalf("list=%v, calls=%d", err, listCalls)
	}
}

func TestBuildGitBranchForkPullRequestHeadPreservesProvenanceAndOverride(t *testing.T) {
	project := executionSourceCheckout(t)
	runGit(t, project, "-c", "user.email=fixture@example.com", "-c", "user.name=Fixture", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	runGit(t, project, "remote", "add", "origin", "https://github.com/example/mobile.git")
	headOutput, err := exec.Command("git", "-C", project, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headOutput))
	event := filepath.Join(t.TempDir(), "event.json")
	writeFile(t, event, `{"pull_request":{"number":42,"head":{"sha":"`+head+`","ref":"Feature/git-branch","repo":{"full_name":"contributor/fork"}},"base":{"ref":"staging","repo":{"full_name":"example/mobile"}}}}`)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	t.Setenv("GITHUB_HEAD_REF", "Feature/git-branch")
	t.Setenv("GITHUB_EVENT_PATH", event)
	cmd := &cobra.Command{Use: "build"}
	cmd.SetContext(context.Background())
	cmd.Flags().String("branch", "", "")
	inferred, err := resolveLocalGitBranch(cmd, projectBuildInvocation{ProjectRoot: project})
	if err != nil || inferred.GitBranchName != "contributor:Feature/git-branch" {
		t.Fatalf("fork PR destination = %+v, %v", inferred, err)
	}
	metadata := build.CollectMetadata(project, "", "android", 0)
	git, ok := metadata["git"].(map[string]interface{})
	if !ok || git["branch"] != "Feature/git-branch" || metadata["scm_branch"] != "Feature/git-branch" || metadata["scm_head_sha"] != head {
		t.Fatalf("fork PR destination replaced source provenance: git=%v scm_branch=%v scm_head_sha=%v", git, metadata["scm_branch"], metadata["scm_head_sha"])
	}
	if err := cmd.Flags().Set("branch", "release"); err != nil {
		t.Fatal(err)
	}
	explicit, err := resolveLocalGitBranch(cmd, projectBuildInvocation{ProjectRoot: project})
	if err != nil || explicit.BranchName != "release" || explicit.GitBranchName != "" {
		t.Fatalf("explicit destination lost to the PR branch: %+v, %v", explicit, err)
	}
}
