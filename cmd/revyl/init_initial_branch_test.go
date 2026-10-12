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
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
)

func TestInitAppCreationRemembersOnlyNewAppInitialBranches(t *testing.T) {
	for _, route := range []string{"interactive", "expo_named"} {
		for _, state := range []string{"new", "existing", "conflict"} {
			t.Run(route+"/"+state, func(t *testing.T) {
				const initialBranchID = "22222222-2222-4222-8222-222222222222"
				created := false
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/api/v1/apps/" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					switch r.Method {
					case http.MethodPost:
						created = true
						if state != "new" {
							w.WriteHeader(http.StatusConflict)
							_, _ = w.Write([]byte(`{"detail":"app already exists"}`))
							return
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "app", "name": "Example App", "default_branch_id": initialBranchID, "default_branch_name": "base"})
					case http.MethodGet:
						items := []map[string]any{}
						if state == "existing" || (state == "conflict" && created) {
							items = append(items, map[string]any{"id": "app", "name": "Example App", "platform": "android", "default_branch_id": initialBranchID})
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "total": len(items), "page": 1, "page_size": 100, "total_pages": 1, "has_next": false})
					default:
						t.Errorf("unexpected method: %s", r.Method)
						w.WriteHeader(http.StatusMethodNotAllowed)
					}
				}))
				defer server.Close()
				client := api.NewClientWithBaseURL("test", server.URL)
				cfg := &initConfigDraft{Project: initProjectDraft{Name: "Example"}}
				var appID string
				if route == "interactive" {
					withStdin(t, "Example App\n", func() {
						appID = createAppInteractive(context.Background(), client, cfg, "android")
					})
				} else {
					var err error
					appID, err = ensureNamedApp(context.Background(), client, cfg, "Example App", "android")
					if err != nil {
						t.Fatal(err)
					}
				}
				if appID != "app" {
					t.Fatalf("app ID = %q", appID)
				}
				wantBranchID := ""
				if state == "new" {
					wantBranchID = initialBranchID
				}
				if got := cfg.uploadBranchForApp(appID); got != (initUploadBranchSelection{id: wantBranchID}) {
					t.Fatalf("upload branch = %+v, want ID %q", got, wantBranchID)
				}
				if state != "new" && len(cfg.initialBranchIDs) != 0 {
					t.Fatalf("linked existing app recorded as new: %+v", cfg.initialBranchIDs)
				}
			})
		}
	}
}

func TestInitInitialBranchSelectionIsPerAppAndTransient(t *testing.T) {
	cfg := &initConfigDraft{Project: initProjectDraft{ID: buildTestProjectID}}
	cfg.rememberCreatedApp(&createOrLinkAppResult{ID: "ios", DefaultBranchID: "ios-initial"})
	cfg.rememberCreatedApp(&createOrLinkAppResult{ID: "android", DefaultBranchID: "android-initial"})
	cfg.rememberCreatedApp(&createOrLinkAppResult{ID: "linked", DefaultBranchID: "linked-default", LinkedExisting: true})
	for _, explicit := range []string{"", "release"} {
		cfg.uploadBranch = initUploadBranchSelection{name: explicit}
		for appID, initialID := range map[string]string{"ios": "ios-initial", "android": "android-initial", "linked": "", "other": ""} {
			want := initUploadBranchSelection{id: initialID}
			if explicit != "" {
				want = initUploadBranchSelection{name: explicit}
			}
			if got := cfg.uploadBranchForApp(appID); got != want {
				t.Fatalf("app %s explicit %q: got %+v, want %+v", appID, explicit, got, want)
			}
		}
	}
	authored, err := cfg.canonicalAuthoredConfig()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := config.MarshalCanonicalConfig(*authored)
	if err != nil {
		t.Fatal(err)
	}
	for _, transient := range []string{"branch", "release", "ios-initial", "android-initial"} {
		if strings.Contains(string(encoded), transient) {
			t.Fatalf("transient branch selection leaked into config: %s", encoded)
		}
	}
}

func TestInitFirstBuildRoutesPreserveInitialBranchAndPin(t *testing.T) {
	for _, route := range []string{"batch_retry", "sequential", "react_native", "expo"} {
		for _, linked := range []bool{false, true} {
			for _, explicit := range []string{"", "release"} {
				t.Run(fmt.Sprintf("%s/linked=%t/explicit=%s", route, linked, explicit), func(t *testing.T) {
					const initialBranchID = "22222222-2222-4222-8222-222222222222"
					const changedDefaultID = "33333333-3333-4333-8333-333333333333"
					const explicitBranchID = "44444444-4444-4444-8444-444444444444"
					const uploadID = "55555555-5555-4555-8555-555555555555"
					projectDir := t.TempDir()
					if err := os.WriteFile(filepath.Join(projectDir, "app.apk"), []byte("synthetic-apk"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(filepath.Join(projectDir, "node_modules"), 0o755); err != nil {
						t.Fatal(err)
					}
					recipe := initBuildRecipeDraft{Platform: "android", AppID: "app", BuildCommands: []string{"echo built"}, OutputPath: "app.apk"}
					cfg := &initConfigDraft{uploadBranch: initUploadBranchSelection{name: explicit}, Build: initBuildDraft{Recipes: map[string]initBuildRecipeDraft{"android": recipe}}}
					cfg.rememberCreatedApp(&createOrLinkAppResult{ID: "app", DefaultBranchID: initialBranchID, LinkedExisting: linked})
					if route == "expo" {
						cfg.Build.DetectedSystem = build.SystemExpo
						if err := os.WriteFile(filepath.Join(projectDir, "app.json"), []byte(`{"expo":{"scheme":"example"}}`), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					requests := make(chan api.BuildUploadSessionRequest, 2)
					finalizations := make(chan api.BuildCreateFromUploadRequest, 2)
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
							branchID := changedDefaultID
							if request.BranchId != nil {
								branchID = request.BranchId.String()
							} else if request.BranchName != nil {
								branchID = explicitBranchID
							}
							_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": uploadID, "upload_url": server.URL + "/artifact", "content_type": "application/octet-stream", "branch_id": branchID})
						case "/artifact":
							w.WriteHeader(http.StatusOK)
						case "/api/v1/apps/app/builds":
							var request api.BuildCreateFromUploadRequest
							if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
								t.Error(err)
							}
							finalizations <- request
							_ = json.NewEncoder(w).Encode(map[string]any{"id": "66666666-6666-4666-8666-666666666666", "version": request.Version})
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
						if request.ContextBranchName != nil || stringValue(request.BranchName) != explicit {
							t.Fatalf("unexpected branch name or context: %+v", request)
						}
						if !linked && explicit == "" {
							if request.BranchId == nil || request.BranchId.String() != initialBranchID {
								t.Fatalf("new app followed changed default instead of initial branch: %+v", request)
							}
						} else if request.BranchId != nil {
							t.Fatalf("initial branch overrode linked default or explicit branch: %+v", request)
						}
					}
					finalization := <-finalizations
					if finalization.SetAsCurrent == nil || !*finalization.SetAsCurrent || finalization.UploadId.String() != uploadID {
						t.Fatalf("init lost pin or frozen upload provenance: %+v", finalization)
					}
					if finalization.BranchId != nil || finalization.BranchName != nil || finalization.ContextBranchName != nil {
						t.Fatalf("finalization reselected its branch: %+v", finalization)
					}
				})
			}
		}
	}
}
