package mcp

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func TestWebStartRejectsInvalidOptionsBeforeTransport(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*StartSessionOptions)
	}{
		{"missing app", func(o *StartSessionOptions) { o.AppID = "" }},
		{"invalid app", func(o *StartSessionOptions) { o.AppID = "not-an-id" }},
		{"invalid build", func(o *StartSessionOptions) { o.BuildVersionID = "not-an-id" }},
		{"URL and build", func(o *StartSessionOptions) { o.WebURL = "https://example.test/" }},
		{"insecure URL", func(o *StartSessionOptions) { o.BuildVersionID = ""; o.WebURL = "http://example.test/" }},
		{"URL userinfo", func(o *StartSessionOptions) { o.BuildVersionID = ""; o.WebURL = "https://private:secret@example.test/" }},
		{"URL query", func(o *StartSessionOptions) { o.BuildVersionID = ""; o.WebURL = "https://example.test/?private=value" }},
		{"URL fragment", func(o *StartSessionOptions) { o.BuildVersionID = ""; o.WebURL = "https://example.test/#private" }},
		{"URL control", func(o *StartSessionOptions) { o.BuildVersionID = ""; o.WebURL = "https://example.test/\nprivate" }},
		{"URL backslash", func(o *StartSessionOptions) { o.BuildVersionID = ""; o.WebURL = "https://example.test/\\private" }},
		{"URL invalid port", func(o *StartSessionOptions) { o.BuildVersionID = ""; o.WebURL = "https://example.test:65536/" }},
		{"URL whitespace", func(o *StartSessionOptions) { o.BuildVersionID = ""; o.WebURL = " https://example.test/" }},
		{"commit without URL", func(o *StartSessionOptions) { o.WebCommitSHA = "abcdef1" }},
		{"invalid commit", func(o *StartSessionOptions) {
			o.BuildVersionID = ""
			o.WebURL = "https://example.test/"
			o.WebCommitSHA = "private"
		}},
		{"phone install", func(o *StartSessionOptions) { o.AppURL = "https://example.test/app.apk" }},
		{"phone launch", func(o *StartSessionOptions) { o.LaunchVars = []string{"PRIVATE"} }},
		{"viewport NaN", func(o *StartSessionOptions) {
			o.Web = &api.DeviceWebOptions{Viewport: &api.DeviceWebViewport{Width: new(1280), Height: new(800), DeviceScaleFactor: new(math.NaN())}}
		}},
		{"viewport bounds", func(o *StartSessionOptions) {
			o.Web = &api.DeviceWebOptions{Viewport: &api.DeviceWebViewport{Width: new(1), Height: new(800), DeviceScaleFactor: new(1.0)}}
		}},
		{"negative timeout", func(o *StartSessionOptions) { o.IdleTimeout = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := StartSessionOptions{Platform: "web", AppID: webFixtureAppID, BuildVersionID: webFixtureBuildID}
			test.mutate(&options)
			manager := NewDeviceSessionManager(nil, t.TempDir())
			if _, _, err := manager.StartSession(context.Background(), options); err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") {
				t.Fatalf("invalid input was not rejected safely: %v", err)
			}
		})
	}
}

func TestWebStartResolvesOnlyURLBuildsAndSendsCanonicalRequest(t *testing.T) {
	for _, mode := range []string{"latest", "explicit", "url", "reused", "sign-in", "latest_bundle", "explicit_bundle"} {
		t.Run(mode, func(t *testing.T) {
			kind := "web_external"
			if strings.HasSuffix(mode, "_bundle") {
				kind = "web_bundle"
			}
			latest := strings.HasPrefix(mode, "latest")
			var sessionID string
			var resolved, created, started int
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/apps/builds/"+webFixtureBuildID {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": webFixtureBuildID, "app_id": webFixtureAppID, "artifact_kind": kind})
					return
				}
				if serveWebFixtureBuild(w, r) {
					return
				}
				switch r.URL.Path {
				case "/api/v1/apps/resolve":
					resolved++
					if r.Method != http.MethodPost || r.URL.Query().Get("include_download_url") != "false" {
						t.Errorf("incorrect Web resolver request: %s %s", r.Method, r.URL.String())
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body["build_var_id"] != webFixtureAppID || body["build_selection"] != "latest" {
						t.Errorf("incorrect Web resolver selection: %#v", body)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"build_var_id": webFixtureAppID, "version_id": webFixtureBuildID, "platform": "web", "artifact_kind": kind, "resolution_status": "resolved"})
				case "/api/v1/apps/" + webFixtureAppID + "/builds/external":
					created++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if r.Method != http.MethodPost || len(body) != 2 || body["url"] != "https://EXAMPLE.test:8443/private-path%2F/" || body["commit_sha"] != "abcdef1" {
						t.Errorf("incorrect URL build request: %#v", body)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": webFixtureBuildID, "app_id": webFixtureAppID, "artifact_kind": "web_external", "was_reused": mode == "reused"})
				case "/api/v1/telemetry/cli-traces":
					w.WriteHeader(http.StatusNoContent)
				case "/api/v1/execution/start_device":
					started++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					sessionID, _ = body["session_id"].(string)
					if body["app_id"] != webFixtureAppID || body["build_id"] != webFixtureBuildID || sessionID == "" || body["platform"] != "web" {
						t.Errorf("missing app/build/session binding: %#v", body)
					}
					for _, key := range []string{"url", "app_url", "project_id", "parent_session_id", "is_simulation"} {
						if _, ok := body[key]; ok {
							t.Errorf("start included forbidden %s", key)
						}
					}
					web, ok := body["web"].(map[string]any)
					if mode == "sign-in" {
						want := map[string]any{"path": "/__e2e__/login", "secret_name": "REVYL_SIGN_IN_SECRET"}
						if !ok || len(web) != 2 || !reflect.DeepEqual(web["sign_in"], want) {
							t.Errorf("web options missing project sign-in: %#v", web)
						}
					} else if !ok || len(web) != 1 || web["viewport"] == nil {
						t.Errorf("web options not viewport-only: %#v", web)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "workflow_run_id": startIDWorkflowRunID})
				case "/api/v1/execution/device-sessions/" + sessionID:
					_ = json.NewEncoder(w).Encode(runtimeProjectedSessionDetail(sessionID, "running"))
				case "/api/v1/execution/device-proxy/" + startIDWorkflowRunID + "/screenshot":
					w.Header().Set("X-Revyl-Screen-Token", "fixture-token")
					_, _ = w.Write(runtimeReadyFixturePNG(t))
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backend.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
			server := &Server{sessionMgr: manager}
			if mode == "sign-in" {
				server.project = &config.ProjectContext{Authored: &config.AuthoredConfig{Session: &config.AuthoredSession{AuthBypass: &config.AuthoredAuthBypass{SignIn: &config.AuthoredSignIn{Path: new("/__e2e__/login"), Secret: new("REVYL_SIGN_IN_SECRET")}}}}}
			}
			input := StartDeviceSessionInput{Platform: "web", AppID: webFixtureAppID, NoOpen: true, Viewport: &api.DeviceWebViewport{Width: new(1280), Height: new(800), DeviceScaleFactor: new(1.0)}}
			if strings.HasPrefix(mode, "explicit") || mode == "sign-in" {
				input.BuildVersionID = webFixtureBuildID
			}
			if mode == "url" || mode == "reused" {
				input.URL = "https://EXAMPLE.test:8443/private-path%2F/"
				input.Commit = "abcdef1"
			}
			_, output, err := server.handleStartDeviceSession(context.Background(), nil, input)
			if err != nil || !output.Success || output.SessionID != sessionID || output.BuildID != webFixtureBuildID || started != 1 {
				t.Fatalf("start failed: %#v %v", output, err)
			}
			manager.StopIdleTimer(output.SessionIndex)
			if latest && resolved != 1 || !latest && resolved != 0 || (mode == "url" || mode == "reused") && created != 1 {
				t.Fatal("incorrect build selection path")
			}
			encoded, _ := json.Marshal(output)
			if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "parent_session") {
				t.Fatal("start leaked URL or extra identity")
			}
			for _, step := range output.NextSteps {
				if step.Tool == "install_app" {
					t.Fatal("Web suggests phone installation")
				}
			}
		})
	}
}

func TestWebBuildSelectionFailsClosed(t *testing.T) {
	for _, test := range []struct{ name, kind, appID string }{
		{"foreign bundle", "web_bundle", "33333333-3333-4333-8333-333333333333"}, {"foreign app", "web_external", "33333333-3333-4333-8333-333333333333"}, {"unknown kind", "unknown", webFixtureAppID}, {"phone", "android_apk", webFixtureAppID},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/apps/builds/"+webFixtureBuildID {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": webFixtureBuildID, "app_id": test.appID, "artifact_kind": test.kind})
					return
				}
				if serveWebFixtureBuild(w, r) {
					return
				}
				t.Errorf("rejected build reached %s", r.URL.Path)
			}))
			t.Cleanup(backend.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
			_, _, err := manager.StartSession(context.Background(), StartSessionOptions{Platform: "web", AppID: webFixtureAppID, BuildVersionID: webFixtureBuildID})
			if err == nil {
				t.Fatal("invalid build accepted")
			}
		})
	}
}

func TestWebSignInFromSessionAppliesDefaultsAndCarriesOnlyTheSecretName(t *testing.T) {
	if WebSignInFromSession(nil) != nil || WebSignInFromSession(&config.AuthoredSession{AuthBypass: &config.AuthoredAuthBypass{LaunchVars: []string{"AUTH"}}}) != nil {
		t.Fatal("sign_in mapped without a sign_in block")
	}
	path, secret := "/__e2e__/login", "REVYL_SIGN_IN_SECRET"
	got := WebSignInFromSession(&config.AuthoredSession{AuthBypass: &config.AuthoredAuthBypass{SignIn: &config.AuthoredSignIn{Path: &path, Secret: &secret}}})
	want := &api.DeviceWebSignIn{Path: path, SecretName: secret}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WebSignInFromSession() = %#v, want %#v", got, want)
	}
}

func TestWebStartHonorsTheSelectedBranch(t *testing.T) {
	const defaultBranchID = "44444444-4444-4444-8444-444444444444"
	for _, test := range []struct {
		name       string
		opts       StartSessionOptions
		wantLookup string
		wantMode   string
		wantCreate map[string]any
	}{
		{"default branch", StartSessionOptions{GitBranchName: "feature/source"}, "|", "latest", nil},
		{"latest on default branch", StartSessionOptions{BuildSelection: "latest", GitBranchName: "feature/source"}, "|", "latest", nil},
		{"named branch", StartSessionOptions{BranchName: "pr-42"}, "|pr-42", "latest", nil},
		{"all branches", StartSessionOptions{BuildSelection: "latest_all_branches", GitBranchName: "feature/source"}, "|", "latest_all_branches", nil},
		{"URL on named branch", StartSessionOptions{BranchName: "pr-42", WebURL: "https://pr-42.example.test/"}, "", "", map[string]any{"url": "https://pr-42.example.test/", "branch_name": "pr-42"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var lookups []string
			var created map[string]any
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + webFixtureAppID:
					_ = json.NewEncoder(w).Encode(map[string]any{"id": webFixtureAppID, "platform": "Web", "default_branch_id": defaultBranchID})
				case "/api/v1/apps/resolve":
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if r.Method != http.MethodPost || r.URL.Query().Get("include_download_url") != "false" || body["build_selection"] != test.wantMode {
						t.Errorf("incorrect resolver request: %s %s %#v", r.Method, r.URL.String(), body)
					}
					wantGitBranch := test.opts.GitBranchName
					if test.wantMode == "latest_all_branches" {
						wantGitBranch = ""
					}
					if body["git_branch_name"] != wantGitBranch {
						t.Errorf("Git branch = %q, want %q", body["git_branch_name"], wantGitBranch)
					}
					lookups = append(lookups, body["branch_id"]+"|"+body["branch_name"])
					_ = json.NewEncoder(w).Encode(map[string]any{"build_var_id": webFixtureAppID, "version_id": webFixtureBuildID, "platform": "web", "artifact_kind": "web_external", "resolution_status": "resolved", "branch_id": defaultBranchID, "branch_name": "base", "branch_source": "default_branch", "git_branch_name": wantGitBranch})
				case "/api/v1/apps/" + webFixtureAppID + "/builds/external":
					if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
						t.Error(err)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": webFixtureBuildID, "app_id": webFixtureAppID, "artifact_kind": "web_external"})
				case "/api/v1/apps/builds/" + webFixtureBuildID:
					_ = json.NewEncoder(w).Encode(map[string]any{"id": webFixtureBuildID, "app_id": webFixtureAppID, "artifact_kind": "web_external"})
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
			}))
			t.Cleanup(backend.Close)
			opts := test.opts
			opts.Platform, opts.AppID = "web", webFixtureAppID
			if err := ValidateWebSessionStartOptions(opts); err != nil {
				t.Fatal(err)
			}
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
			artifact, err := manager.resolveWebStartArtifact(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantCreate != nil {
				if len(lookups) != 0 || !reflect.DeepEqual(created, test.wantCreate) {
					t.Fatalf("lookups=%#v created=%#v", lookups, created)
				}
				return
			}
			if artifact.BranchID != defaultBranchID || artifact.BranchName != "base" || artifact.BranchSource != api.BranchSourceDefaultBranch {
				t.Fatalf("lost branch provenance: %+v", artifact)
			}
			if !slices.Equal(lookups, []string{test.wantLookup}) || created != nil {
				t.Fatalf("lookups=%#v created=%#v", lookups, created)
			}
		})
	}
}

func TestWebStartRejectsBranchSelectionsItCannotApply(t *testing.T) {
	for _, test := range []struct {
		name string
		opts StartSessionOptions
		want string
	}{
		{"branch and build", StartSessionOptions{BranchName: "pr-42", BuildVersionID: webFixtureBuildID}, "only one of branch or build_version_id"},
		{"branch ID and name", StartSessionOptions{BranchID: "44444444-4444-4444-8444-444444444444", BranchName: "pr-42"}, "either branch ID or branch name"},
		{"padded branch", StartSessionOptions{BranchName: " pr-42"}, "whitespace"},
		{"inherit", StartSessionOptions{BuildSelection: "inherit"}, "build_selection inherit is not available on platform web"},
		{"pinned", StartSessionOptions{BuildSelection: "pinned", BuildVersionID: webFixtureBuildID}, "build_selection pinned is not available on platform web"},
		{"all branches with branch", StartSessionOptions{BuildSelection: "latest_all_branches", BranchName: "pr-42"}, "latest_all_branches takes no branch"},
		{"all branches with URL", StartSessionOptions{BuildSelection: "latest_all_branches", WebURL: "https://example.test/"}, "latest_all_branches takes no branch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := test.opts
			opts.Platform, opts.AppID = "web", webFixtureAppID
			if err := ValidateWebSessionStartOptions(opts); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
