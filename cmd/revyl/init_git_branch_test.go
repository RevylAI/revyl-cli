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
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func setInitTestGitBranch(t *testing.T, directory, branch string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", directory, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("set test branch: %v: %s", err, output)
	}
}

func TestInitUploadBranchUsesSelectedProject(t *testing.T) {
	projectDir := t.TempDir()
	gitInitForInitTest(t, projectDir)
	setInitTestGitBranch(t, projectDir, "Feature/Écran")
	selected, err := detectInitUploadBranch(context.Background(), projectDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.name != "" || selected.contextName != "Feature/Écran" {
		t.Fatalf("selected branch = %+v", selected)
	}

	draft := &initConfigDraft{Project: initProjectDraft{ID: buildTestProjectID}, uploadBranch: selected}
	authored, err := draft.canonicalAuthoredConfig()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := config.MarshalCanonicalConfig(*authored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Feature/Écran") || strings.Contains(string(encoded), "branch") {
		t.Fatalf("runtime branch leaked into authored config: %s", encoded)
	}
}

func TestInitUploadBranchExplicitSkipsInference(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	selected, err := detectInitUploadBranch(ctx, filepath.Join(t.TempDir(), "missing"), "Release/Écran")
	if err != nil || selected.name != "Release/Écran" || selected.contextName != "" {
		t.Fatalf("explicit selection = %+v, %v", selected, err)
	}
	if _, err := detectInitUploadBranch(ctx, t.TempDir(), ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inference = %v", err)
	}
}

func TestInitUploadBranchWithoutGitBranchKeepsAppDefault(t *testing.T) {
	for _, directory := range []string{"", t.TempDir()} {
		selected, err := detectInitUploadBranch(context.Background(), directory, "")
		if err != nil || selected != (initUploadBranchSelection{}) {
			t.Fatalf("selection without project context = %+v, %v", selected, err)
		}
	}
}

func TestInitUploadsKeepInvocationBranchAfterCheckoutChanges(t *testing.T) {
	for _, explicit := range []string{"", "release"} {
		t.Run("explicit="+explicit, func(t *testing.T) {
			projectDir := t.TempDir()
			gitInitForInitTest(t, projectDir)
			setInitTestGitBranch(t, projectDir, "feature/original")
			selection, err := detectInitUploadBranch(context.Background(), projectDir, explicit)
			if err != nil {
				t.Fatal(err)
			}
			setInitTestGitBranch(t, projectDir, "feature/changed-during-build")
			artifactPath := filepath.Join(projectDir, "app.apk")
			if err := os.WriteFile(artifactPath, []byte("synthetic-apk"), 0o600); err != nil {
				t.Fatal(err)
			}
			requests := make(chan api.BuildUploadSessionRequest, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/builds/upload-session") || r.Method != http.MethodPost {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				} else {
					var decoded api.BuildUploadSessionRequest
					if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
						t.Error(err)
					}
					requests <- decoded
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"detail":"stop after recording upload destination"}`))
			}))
			defer server.Close()
			client := api.NewClientWithBaseURL("test", server.URL)
			recipe := initBuildRecipeDraft{Platform: "android", AppID: "app", OutputPath: "app.apk"}
			cfg := &initConfigDraft{
				uploadBranch: selection,
				Build:        initBuildDraft{Recipes: map[string]initBuildRecipeDraft{"android": recipe}},
			}
			for range 2 {
				results := runWizardBuildBatch(context.Background(), client, cfg, projectDir, []string{"android"}, true, nil)
				if len(results) != 1 || results[0].Err == nil {
					t.Fatalf("expected deliberate upload stop: %+v", results)
				}
			}
			sequentialBuildAndUpload(context.Background(), client, cfg, projectDir, "android", recipe, "", true, &firstBuildOutcome{})
			if len(requests) != 3 {
				t.Fatalf("upload requests = %d, want batch, retry and sequential", len(requests))
			}
			for range 3 {
				got := <-requests
				if stringValue(got.BranchName) != selection.name || stringValue(got.GitBranchName) != selection.contextName {
					t.Fatalf("upload changed frozen selection: %+v, want %+v", got, selection)
				}
			}
		})
	}
}

func TestInitCreatedAppUploadKeepsInitialBranch(t *testing.T) {
	for _, explicit := range []string{"", "release"} {
		t.Run("explicit="+explicit, func(t *testing.T) {
			projectDir := t.TempDir()
			artifactPath := filepath.Join(projectDir, "app.apk")
			if err := os.WriteFile(artifactPath, []byte("synthetic-apk"), 0600); err != nil {
				t.Fatal(err)
			}
			requests := make(chan api.BuildUploadSessionRequest, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/builds/upload-session") {
					t.Errorf("unexpected request: %s", r.URL)
				}
				var request api.BuildUploadSessionRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				requests <- request
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"detail":"stop after recording destination"}`))
			}))
			defer server.Close()
			client := api.NewClientWithBaseURL("test", server.URL)
			recipe := initBuildRecipeDraft{Platform: "android", AppID: "app", OutputPath: "app.apk"}
			cfg := &initConfigDraft{uploadBranch: initUploadBranchSelection{name: explicit, contextName: "feature"}, Build: initBuildDraft{Recipes: map[string]initBuildRecipeDraft{"android": recipe}}}
			cfg.rememberCreatedApp(&createOrLinkAppResult{ID: "app", DefaultBranchID: "22222222-2222-4222-8222-222222222222"})
			for range 2 {
				runWizardBuildBatch(context.Background(), client, cfg, projectDir, []string{"android"}, true, nil)
			}
			sequentialBuildAndUpload(context.Background(), client, cfg, projectDir, "android", recipe, "", true, &firstBuildOutcome{})
			if len(requests) != 3 {
				t.Fatalf("upload requests = %d", len(requests))
			}
			for range 3 {
				got := <-requests
				if explicit == "" {
					if got.BranchId == nil || got.BranchId.String() != cfg.initialBranchIDs["app"] || got.GitBranchName != nil || got.BranchName != nil {
						t.Fatalf("new app did not use initial branch: %+v", got)
					}
				} else if stringValue(got.BranchName) != explicit || got.BranchId != nil {
					t.Fatalf("explicit override lost: %+v", got)
				}
			}
			cfg.rememberCreatedApp(&createOrLinkAppResult{ID: "linked", LinkedExisting: true})
			if got := cfg.uploadBranchForApp("linked"); got != cfg.uploadBranch {
				t.Fatalf("linked app lost inferred context: %+v", got)
			}
		})
	}
}
