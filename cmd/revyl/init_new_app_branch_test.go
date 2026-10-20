package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
)

const (
	initTestAppID           = "44444444-4444-4444-8444-444444444444"
	initTestStarterBranchID = "22222222-2222-4222-8222-222222222222"
	initTestUploadBranchID  = "33333333-3333-4333-8333-333333333333"
	initTestUploadID        = "55555555-5555-4555-8555-555555555555"
	initTestBuildID         = "66666666-6666-4666-8666-666666666666"
)

// serveInitTestApps answers app creation and lookup for one android app named
// appName; any other request fails the test.
func serveInitTestApps(t *testing.T, w http.ResponseWriter, r *http.Request, appName string, exists *bool) {
	t.Helper()
	switch r.Method {
	case http.MethodPost:
		if *exists {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"app already exists"}`))
			return
		}
		*exists = true
		_ = json.NewEncoder(w).Encode(map[string]any{"id": initTestAppID, "name": appName, "default_branch_id": initTestStarterBranchID, "default_branch_name": "base"})
	case http.MethodGet:
		items := []map[string]any{}
		if *exists {
			items = append(items, map[string]any{"id": initTestAppID, "name": appName, "platform": "android", "default_branch_id": initTestStarterBranchID, "default_branch_name": "base"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "total": len(items), "page": 1, "page_size": 100, "total_pages": 1, "has_next": false})
	default:
		t.Errorf("unexpected app request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestInitAppCreationSaysFirstUploadSetsDefaultBranch(t *testing.T) {
	for _, route := range []string{"interactive", "expo"} {
		for _, state := range []string{"new", "existing", "conflict"} {
			t.Run(route+"/"+state, func(t *testing.T) {
				appName := "Example App"
				if route == "expo" {
					appName = "Example App-android"
				}
				exists := state == "existing"
				var mu sync.Mutex
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path != "/api/v1/apps/" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
						return
					}
					if state == "conflict" && r.Method == http.MethodPost {
						exists = true
					}
					serveInitTestApps(t, w, r, appName, &exists)
				}))
				defer server.Close()
				client := api.NewClientWithBaseURL("test", server.URL)
				cfg := &initConfigDraft{Project: initProjectDraft{Name: "Example App"}}
				var appID string
				output := captureStdoutAndStderr(t, func() {
					if route == "interactive" {
						withStdin(t, appName+"\n", func() {
							appID = createAppInteractive(context.Background(), client, cfg, "android")
						})
						return
					}
					cfg.Build = initBuildDraft{DetectedSystem: build.SystemExpo, Recipes: map[string]initBuildRecipeDraft{"android": {Platform: "android"}}}
					withStdin(t, "y\n", func() {
						wizardCreateExpoAppStreams(context.Background(), client, cfg, filepath.Join(t.TempDir(), ".revyl", "config.yaml"))
					})
					appID = cfg.Build.Recipes["android"].AppID
				})
				if appID != initTestAppID {
					t.Fatalf("app ID = %q; output:\n%s", appID, output)
				}
				rule := "The first build you upload to " + appName + " sets its default branch"
				if strings.Contains(output, rule) != (state == "new") {
					t.Fatalf("default branch rule shown = %t, want %t; output:\n%s", strings.Contains(output, rule), state == "new", output)
				}
				if strings.Contains(output, "base") || strings.Contains(output, "initial branch") {
					t.Fatalf("app creation announced the starter branch:\n%s", output)
				}
			})
		}
	}
}

func TestInitNewAppFirstUploadNamesItsBranch(t *testing.T) {
	for _, appState := range []string{"new", "linked"} {
		for _, selection := range []struct {
			name, explicit, gitBranch, wantReported string
		}{
			{name: "git_branch", gitBranch: "main", wantReported: "main"},
			{name: "explicit_branch", explicit: "release", gitBranch: "main", wantReported: "release"},
			{name: "no_git_context", wantReported: "base"},
		} {
			t.Run(appState+"/"+selection.name, func(t *testing.T) {
				projectDir := t.TempDir()
				if selection.gitBranch != "" {
					gitInitForInitTest(t, projectDir)
					setInitTestGitBranch(t, projectDir, selection.gitBranch)
				}
				if err := os.WriteFile(filepath.Join(projectDir, "app.apk"), []byte("synthetic-apk"), 0o600); err != nil {
					t.Fatal(err)
				}
				exists := appState == "linked"
				uploads := make(chan api.BuildUploadSessionRequest, 1)
				reported := ""
				var mu sync.Mutex
				var server *httptest.Server
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/v1/apps/":
						serveInitTestApps(t, w, r, "Example App", &exists)
					case "/api/v1/apps/" + initTestAppID + "/builds/upload-session":
						var request api.BuildUploadSessionRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
						}
						uploads <- request
						reported = "base"
						if name := stringValue(request.BranchName); name != "" {
							reported = name
						} else if name := stringValue(request.GitBranchName); name != "" {
							reported = name
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": initTestUploadID, "upload_url": server.URL + "/artifact", "content_type": "application/octet-stream", "branch_id": initTestUploadBranchID, "branch_name": reported})
					case "/artifact":
						w.WriteHeader(http.StatusOK)
					case "/api/v1/apps/" + initTestAppID + "/builds":
						var request map[string]any
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"id": initTestBuildID, "version": request["version"], "branch_id": initTestUploadBranchID, "branch_name": reported})
					case "/api/v1/apps/resolve":
						_ = json.NewEncoder(w).Encode(map[string]any{"version_id": initTestBuildID, "branch_id": initTestUploadBranchID, "branch_name": reported, "resolution_status": "resolved"})
					case "/api/v1/apps/builds/" + initTestBuildID:
						_ = json.NewEncoder(w).Encode(api.BuildVersionDetail{ID: initTestBuildID, AppID: initTestAppID, Version: "v1"})
					case "/api/v1/apps/" + initTestAppID + "/branches/" + initTestUploadBranchID:
						_ = json.NewEncoder(w).Encode(api.BuildBranch{ID: initTestUploadBranchID, AppID: initTestAppID, Name: reported})
					default:
						t.Errorf("unexpected request, such as a branch creation: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				client := api.NewClientWithBaseURL("test", server.URL)
				cfg := &initConfigDraft{Project: initProjectDraft{Name: "Example App"}}
				recipe := initBuildRecipeDraft{Platform: "android", BuildCommands: []string{"echo built"}, OutputPath: "app.apk"}
				outcome := &firstBuildOutcome{}
				output := captureStdoutAndStderr(t, func() {
					withStdin(t, "Example App\n", func() {
						recipe.AppID = createAppInteractive(context.Background(), client, cfg, "android")
					})
					var err error
					cfg.uploadBranch, err = detectInitUploadBranch(context.Background(), projectDir, selection.explicit)
					if err != nil {
						t.Fatal(err)
					}
					sequentialBuildAndUpload(context.Background(), client, cfg, projectDir, "android", recipe, "", true, outcome)
				})
				if !outcome.HasSucceeded() || outcome.HasFailed() {
					t.Fatalf("first build outcome = %+v; output:\n%s", outcome, output)
				}
				request := <-uploads
				wantGitBranch := selection.gitBranch
				if selection.explicit != "" {
					wantGitBranch = ""
				}
				if request.BranchId != nil || stringValue(request.BranchName) != selection.explicit || stringValue(request.GitBranchName) != wantGitBranch {
					t.Fatalf("upload destination = branch_id %v, branch_name %q, git_branch_name %q; want branch_name %q, git_branch_name %q",
						request.BranchId, stringValue(request.BranchName), stringValue(request.GitBranchName), selection.explicit, wantGitBranch)
				}
				if want := selection.wantReported + " (Latest build: runs use this upload)"; !strings.Contains(output, want) {
					t.Fatalf("upload result did not show the branch the backend reported, %q:\n%s", want, output)
				}
				if strings.Contains(output, "initial branch") {
					t.Fatalf("init announced the starter branch:\n%s", output)
				}
			})
		}
	}
}

func TestInitFirstBuildRoutesSendSelectedBranchWithoutPinning(t *testing.T) {
	for _, route := range []string{"batch_retry", "sequential", "react_native", "expo"} {
		for _, selection := range []initUploadBranchSelection{{contextName: "main"}, {name: "release"}, {}} {
			t.Run(fmt.Sprintf("%s/name=%s/git=%s", route, selection.name, selection.contextName), func(t *testing.T) {
				projectDir := t.TempDir()
				if err := os.WriteFile(filepath.Join(projectDir, "app.apk"), []byte("synthetic-apk"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(projectDir, "node_modules"), 0o755); err != nil {
					t.Fatal(err)
				}
				recipe := initBuildRecipeDraft{Platform: "android", AppID: "app", BuildCommands: []string{"echo built"}, OutputPath: "app.apk"}
				cfg := &initConfigDraft{uploadBranch: selection, Build: initBuildDraft{Recipes: map[string]initBuildRecipeDraft{"android": recipe}}}
				if route == "expo" {
					cfg.Build.DetectedSystem = build.SystemExpo
					if err := os.WriteFile(filepath.Join(projectDir, "app.json"), []byte(`{"expo":{"scheme":"example"}}`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				requests := make(chan api.BuildUploadSessionRequest, 2)
				finalizations := make(chan map[string]any, 2)
				var server *httptest.Server
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/v1/apps/app/builds/upload-session":
						var request api.BuildUploadSessionRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
						}
						requests <- request
						if route == "batch_retry" && len(requests) == 1 {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"detail":"retry this initial upload"}`))
							return
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": initTestUploadID, "upload_url": server.URL + "/artifact", "content_type": "application/octet-stream", "branch_id": initTestUploadBranchID})
					case "/artifact":
						w.WriteHeader(http.StatusOK)
					case "/api/v1/apps/app/builds":
						var request map[string]any
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
						}
						finalizations <- request
						_ = json.NewEncoder(w).Encode(map[string]any{"id": initTestBuildID, "version": request["version"]})
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				client := api.NewClientWithBaseURL("test", server.URL)
				outcome := &firstBuildOutcome{}
				wantUploads := 1
				switch route {
				case "batch_retry":
					wantUploads = 2
					for attempt := range wantUploads {
						results := runWizardBuildBatch(context.Background(), client, cfg, projectDir, []string{"android"}, true, nil)
						if len(results) != 1 || (results[0].Err != nil) != (attempt == 0) {
							t.Fatalf("batch attempt %d: %+v", attempt, results)
						}
					}
				case "sequential":
					sequentialBuildAndUpload(context.Background(), client, cfg, projectDir, "android", recipe, "", true, outcome)
				case "react_native":
					withStdin(t, "1\n", func() {
						wizardFirstBuildReactNative(context.Background(), client, cfg, "", projectDir, []string{"android"}, outcome)
					})
				case "expo":
					withStdin(t, "3\n", func() {
						wizardFirstBuildExpo(context.Background(), client, cfg, "", projectDir, []string{"android"}, outcome)
					})
				}
				if route != "batch_retry" && (!outcome.HasSucceeded() || outcome.HasFailed()) {
					t.Fatalf("first build outcome = %+v", outcome)
				}
				if len(requests) != wantUploads || len(finalizations) != 1 {
					t.Fatalf("upload sessions=%d, want %d; finalizations=%d, want 1", len(requests), wantUploads, len(finalizations))
				}
				for range wantUploads {
					request := <-requests
					if request.BranchId != nil || stringValue(request.BranchName) != selection.name || stringValue(request.GitBranchName) != selection.contextName {
						t.Fatalf("upload destination = %+v, want %+v", request, selection)
					}
				}
				finalization := <-finalizations
				if finalization["upload_id"] != initTestUploadID {
					t.Fatalf("init lost frozen upload provenance: %+v", finalization)
				}
				for _, field := range []string{"pin", "set_as_current", "branch_id", "branch_name", "git_branch_name"} {
					if _, sent := finalization[field]; sent {
						t.Fatalf("init finalization sent %s; it must neither pin nor reselect its branch: %+v", field, finalization)
					}
				}
			})
		}
	}
}
