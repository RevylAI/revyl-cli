package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
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

func TestInitUploadsKeepInvocationBranchAfterCheckoutChanges(t *testing.T) {
	for _, explicit := range []string{"", "release"} {
		t.Run("explicit="+explicit, func(t *testing.T) {
			projectDir := t.TempDir()
			gitInitForInitTest(t, projectDir)
			setInitTestGitBranch(t, projectDir, "feature/original")
			selection := initUploadBranchSelection{name: explicit}
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
				if stringValue(got.BranchName) != selection.name || got.ContextBranchName != nil {
					t.Fatalf("upload changed frozen selection: %+v, want %+v", got, selection)
				}
			}
		})
	}
}
