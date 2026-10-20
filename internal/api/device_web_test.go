package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

const webFixtureAppID = "11111111-1111-4111-8111-111111111111"
const webFixtureBuildID = "33333333-3333-4333-8333-333333333333"

func TestWebPlatformRequestsUseCanonicalLowercase(t *testing.T) {
	for _, input := range []string{"web", "Web", "WEB"} {
		t.Run(input, func(t *testing.T) {
			seen := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/":
					seen++
					if r.Method == http.MethodGet {
						if r.URL.Query().Get("platform") != "web" {
							t.Error("noncanonical app filter")
						}
						_, _ = w.Write([]byte(`{"items":[],"total":0}`))
						return
					}
					var request CreateAppRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Platform != "web" {
						t.Error("noncanonical app request")
					}
					_, _ = w.Write([]byte(`{"id":"fixture-app","name":"Fixture","platform":"web"}`))
				case "/api/v1/execution/start_device":
					seen++
					var request StartDeviceRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Platform != "web" {
						t.Error("noncanonical session request")
					}
					_, _ = w.Write([]byte(`{"status":"initializing"}`))
				case "/api/v1/telemetry/cli-traces":
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := NewClientWithBaseURL("fixture-key", server.URL)
			if _, err := client.CreateApp(context.Background(), &CreateAppRequest{Name: "Fixture", Platform: input}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.ListApps(context.Background(), input, 1, 1); err != nil {
				t.Fatal(err)
			}
			request := &StartDeviceRequest{Platform: input}
			if _, err := client.StartDevice(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if seen != 3 || request.Platform != input {
				t.Fatal("requests missing or caller's request mutated")
			}
		})
	}
	if normalizePlatform("ios") != "iOS" || normalizePlatform("android") != "Android" {
		t.Fatal("phone casing changed")
	}
}

func TestExternalURLBuildCreateOmitsCommitAndDoesNotRetry(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/apps/fixture-app/builds/external" {
			t.Errorf("incorrect endpoint: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 1 || body["url"] != "https://EXAMPLE.test/a%2Fb/" {
			t.Errorf("source identity changed: %#v", body)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"temporarily unavailable"}`))
	}))
	defer server.Close()
	_, err := NewClientWithBaseURL("fixture-key", server.URL).CreateExternalURLBuild(context.Background(), "fixture-app", &ExternalURLBuildRequest{URL: "https://EXAMPLE.test/a%2Fb/"})
	if err == nil || requests != 1 {
		t.Fatalf("requests=%d error=%v", requests, err)
	}
}

func TestLatestExternalURLBuildFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		wantError  bool
	}{
		{"bundle", "web_bundle", true},
		{"URL", "web_external", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/apps/resolve" || r.URL.Query().Get("include_download_url") != "false" {
					t.Errorf("incorrect resolver request: %s %s", r.Method, r.URL.String())
				}
				_, _ = w.Write([]byte(fmt.Sprintf(`{"build_var_id":%q,"version_id":%q,"platform":"web","artifact_kind":%q,"resolution_status":"resolved"}`, webFixtureAppID, webFixtureBuildID, test.kind)))
			}))
			defer server.Close()
			build, err := NewClientWithBaseURL("fixture-key", server.URL).GetLatestExternalURLBuild(context.Background(), webFixtureAppID)
			if (err != nil) != test.wantError || (test.wantError && build != nil) || (!test.wantError && build == nil) {
				t.Fatalf("build=%#v error=%v", build, err)
			}
		})
	}
}

func TestLatestWebBuildAcceptsURLAndBundle(t *testing.T) {
	for _, test := range []struct {
		name, kind, status string
		wantError          bool
	}{
		{name: "URL", kind: "web_external", status: "resolved"},
		{name: "bundle", kind: "web_bundle", status: "resolved"},
		{name: "empty", status: "branch_has_no_builds"},
		{name: "phone", kind: "android_apk", status: "resolved", wantError: true},
		{name: "unknown", kind: "unknown", status: "resolved", wantError: true},
		{name: "missing kind", status: "resolved", wantError: true},
		{name: "unknown status", status: "unknown", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/apps/resolve" || r.URL.Query().Get("include_download_url") != "false" {
					t.Errorf("incorrect resolver request: %s %s", r.Method, r.URL.String())
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["build_var_id"] != webFixtureAppID || body["build_selection"] != "latest" {
					t.Fatalf("incorrect Web selection: %#v", body)
				}
				_, _ = w.Write([]byte(fmt.Sprintf(`{"build_var_id":%q,"version_id":%q,"platform":"web","artifact_kind":%q,"resolution_status":%q}`, webFixtureAppID, webFixtureBuildID, test.kind, test.status)))
			}))
			defer server.Close()
			build, err := NewClientWithBaseURL("fixture-key", server.URL).GetLatestWebBuild(context.Background(), webFixtureAppID, BuildSelection{})
			if (err != nil) != test.wantError {
				t.Fatalf("build=%#v error=%v", build, err)
			}
			if !test.wantError && test.kind != "" {
				if build == nil || build.ArtifactKind != test.kind || build.ID != webFixtureBuildID {
					t.Fatalf("build=%#v, want %s", build, test.kind)
				}
			} else if build != nil {
				t.Fatalf("unexpected build: %#v", build)
			}
		})
	}
}

func TestLatestWebBuildSkipsNewerPendingUpload(t *testing.T) {
	for _, kind := range []string{"web_external", "web_bundle"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/apps/resolve" {
					t.Errorf("Web latest tried build history: %s %s", r.Method, r.URL.String())
				}
				_, _ = fmt.Fprintf(w, `{"build_var_id":%q,"version_id":%q,"platform":"web","artifact_kind":%q,"resolution_status":"resolved","branch_id":%q,"branch_name":"base","version":"eligible"}`, webFixtureAppID, webFixtureBuildID, kind, branchTestID)
			}))
			defer server.Close()
			build, err := NewClientWithBaseURL("fixture-key", server.URL).GetLatestWebBuild(context.Background(), webFixtureAppID, BuildSelection{BranchID: branchTestID})
			if err != nil || build == nil || build.ID != webFixtureBuildID || build.ArtifactKind != kind || build.BranchID != branchTestID || build.BranchName != "base" || build.Version != "eligible" || requests != 1 {
				t.Fatalf("build=%#v error=%v requests=%d", build, err, requests)
			}
		})
	}
}

func TestGetWebBuildPreservesExplicitBuildIdentity(t *testing.T) {
	for _, kind := range []string{"web_external", "web_bundle"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/apps/builds/fixture-build" || r.URL.RawQuery != "" {
					t.Errorf("incorrect explicit build lookup: %s %s", r.Method, r.URL.String())
				}
				_ = json.NewEncoder(w).Encode(WebBuild{ID: "fixture-build", AppID: "fixture-app", ArtifactKind: kind, Version: "v1"})
			}))
			defer server.Close()
			build, err := NewClientWithBaseURL("fixture-key", server.URL).GetWebBuild(context.Background(), "fixture-build")
			if err != nil || build == nil || build.ID != "fixture-build" || build.AppID != "fixture-app" || build.ArtifactKind != kind {
				t.Fatalf("build=%#v error=%v", build, err)
			}
		})
	}
}

func TestLatestWebBuildAndURLBuildCarryTheSelectedBranch(t *testing.T) {
	var selections []string
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/apps/resolve" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			selections = append(selections, fmt.Sprintf("%v|%v|%v", body["branch_id"], body["branch_name"], body["build_selection"]))
			_, _ = w.Write([]byte(fmt.Sprintf(`{"build_var_id":%q,"version_id":%q,"platform":"web","artifact_kind":"web_external","resolution_status":"resolved"}`, webFixtureAppID, webFixtureBuildID)))
			return
		}
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{"id":%q,"app_id":%q,"artifact_kind":"web_external"}`, webFixtureBuildID, webFixtureAppID)))
		}
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)
	for _, branch := range [][2]string{{"33333333-3333-4333-8333-333333333333", ""}, {"", "pr-42"}} {
		if _, err := client.GetLatestWebBuild(context.Background(), webFixtureAppID, BuildSelection{BranchID: branch[0], BranchName: branch[1]}); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(selections, []string{"33333333-3333-4333-8333-333333333333|<nil>|latest", "<nil>|pr-42|latest"}) {
		t.Fatalf("branch selections = %#v", selections)
	}
	if _, err := client.CreateExternalURLBuild(context.Background(), "fixture-app", &ExternalURLBuildRequest{BranchName: "pr-42", URL: "https://example.test/"}); err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || created["branch_name"] != "pr-42" || created["url"] != "https://example.test/" {
		t.Fatalf("URL build request = %#v", created)
	}
}
