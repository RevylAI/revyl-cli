package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

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
		name, response string
		wantError      bool
	}{
		{"empty", `{"items":[]}`, false},
		{"bundle", `{"items":[{"id":"build","artifact_kind":"web_bundle"}]}`, true},
		{"missing id", `{"items":[{"artifact_kind":"web_external"}]}`, true},
		{"multiple", `{"items":[{"id":"one","artifact_kind":"web_external"},{"id":"two","artifact_kind":"web_external"}]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("artifact_kind") != "web_external" || r.URL.Query().Get("page_size") != "1" || r.URL.Query().Get("include_jobs") != "" {
					t.Error("incorrect build filter")
				}
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			build, err := NewClientWithBaseURL("fixture-key", server.URL).GetLatestExternalURLBuild(context.Background(), "fixture-app")
			if (err != nil) != test.wantError || build != nil {
				t.Fatalf("build=%#v error=%v", build, err)
			}
		})
	}
}

func TestLatestWebBuildAcceptsURLAndBundle(t *testing.T) {
	for _, test := range []struct {
		name, response, wantKind string
		wantError                bool
	}{
		{name: "URL", response: `{"items":[{"id":"build","artifact_kind":"web_external"}]}`, wantKind: "web_external"},
		{name: "bundle", response: `{"items":[{"id":"build","artifact_kind":"web_bundle"}]}`, wantKind: "web_bundle"},
		{name: "empty", response: `{"items":[]}`},
		{name: "phone", response: `{"items":[{"id":"build","artifact_kind":"android_apk"}]}`, wantError: true},
		{name: "unknown", response: `{"items":[{"id":"build","artifact_kind":"unknown"}]}`, wantError: true},
		{name: "missing id", response: `{"items":[{"artifact_kind":"web_bundle"}]}`, wantError: true},
		{name: "multiple", response: `{"items":[{"id":"one","artifact_kind":"web_bundle"},{"id":"two","artifact_kind":"web_external"}]}`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/apps/fixture-app/builds" || query.Get("artifact_kind") != "" || !slices.Equal(query["artifact_kinds"], []string{"web_external", "web_bundle"}) || query.Get("page_size") != "1" || query.Get("page") != "1" || query.Get("include_jobs") != "false" || query.Has("branch_id") || query.Has("branch_name") {
					t.Errorf("incorrect Web build lookup: %s %s", r.Method, r.URL.String())
				}
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			build, err := NewClientWithBaseURL("fixture-key", server.URL).GetLatestWebBuild(context.Background(), "fixture-app", "", "")
			if (err != nil) != test.wantError {
				t.Fatalf("build=%#v error=%v", build, err)
			}
			if test.wantKind != "" {
				if build == nil || build.ArtifactKind != test.wantKind || build.ID != "build" {
					t.Fatalf("build=%#v, want %s", build, test.wantKind)
				}
			} else if build != nil {
				t.Fatalf("unexpected build: %#v", build)
			}
		})
	}
}

func TestLatestWebBuildSkipsNewerPendingUpload(t *testing.T) {
	kinds := []string{"web_external", "web_bundle"}
	for i, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			builds := []BuildVersion{
				{ID: "pending", ArtifactKind: "unknown"},
				{ID: "usable", ArtifactKind: kind},
				{ID: "older", ArtifactKind: kinds[1-i]},
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				query := r.URL.Query()
				if query.Get("page_size") != "1" || query.Get("page") != "1" {
					t.Errorf("unbounded Web build lookup: %s", r.URL.String())
				}
				for _, build := range builds {
					if kinds := query["artifact_kinds"]; len(kinds) != 0 && !slices.Contains(kinds, build.ArtifactKind) {
						continue
					}
					_ = json.NewEncoder(w).Encode(BuildVersionsPage{Items: []BuildVersion{build}})
					return
				}
				_ = json.NewEncoder(w).Encode(BuildVersionsPage{})
			}))
			defer server.Close()
			build, err := NewClientWithBaseURL("fixture-key", server.URL).GetLatestWebBuild(context.Background(), "fixture-app", "", "")
			if err != nil || build == nil || build.ID != "usable" || build.ArtifactKind != kind || requests != 1 {
				t.Fatalf("build=%#v requests=%d error=%v", build, requests, err)
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
	var lookups []string
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"id":"build","app_id":"fixture-app","artifact_kind":"web_external"}`))
			return
		}
		query := r.URL.Query()
		lookups = append(lookups, query.Get("branch_id")+"|"+query.Get("branch_name"))
		_, _ = w.Write([]byte(`{"items":[{"id":"build","artifact_kind":"web_external"}]}`))
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)
	for _, branch := range [][2]string{{"33333333-3333-4333-8333-333333333333", ""}, {"", "pr-42"}} {
		if _, err := client.GetLatestWebBuild(context.Background(), "fixture-app", branch[0], branch[1]); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(lookups, []string{"33333333-3333-4333-8333-333333333333|", "|pr-42"}) {
		t.Fatalf("branch lookups = %#v", lookups)
	}
	if _, err := client.CreateExternalURLBuild(context.Background(), "fixture-app", &ExternalURLBuildRequest{BranchName: "pr-42", URL: "https://example.test/"}); err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || created["branch_name"] != "pr-42" || created["url"] != "https://example.test/" {
		t.Fatalf("URL build request = %#v", created)
	}
}
