package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/build"
)

func TestExplicitUploadSCMContextAcrossRunners(t *testing.T) {
	for _, runner := range []string{"unknown", "github-actions", "buildkite"} {
		for _, source := range []string{"file", "url"} {
			for _, pr := range []int{0, 42} {
				t.Run(runner+"/"+source+"/"+strconv.Itoa(pr), func(t *testing.T) {
					t.Setenv("GITHUB_ACTIONS", "")
					t.Setenv("BUILDKITE", "")
					if runner == "github-actions" {
						t.Setenv("GITHUB_ACTIONS", "true")
					}
					if runner == "buildkite" {
						t.Setenv("BUILDKITE", "true")
					}
					t.Setenv("GITHUB_REPOSITORY", "wrong/repository")
					t.Setenv("GITHUB_SHA", "wrong-sha")
					eventPath := filepath.Join(t.TempDir(), "event.json")
					if err := os.WriteFile(eventPath, []byte(`{"pull_request":{"number":99,"head":{"sha":"wrong-head"},"base":{"sha":"wrong-base"}}}`), 0o600); err != nil {
						t.Fatal(err)
					}
					t.Setenv("GITHUB_EVENT_PATH", eventPath)
					t.Setenv("BUILDKITE_REPO", "git@github.com:wrong/repository.git")
					t.Setenv("BUILDKITE_PULL_REQUEST", "99")
					t.Setenv("BUILDKITE_COMMIT", "wrong-sha")
					sha := strings.Repeat("a", 40)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					ctx, err := build.WithUploadSCMContext(ctx, "acme/mobile", sha, pr)
					if err != nil {
						t.Fatal(err)
					}
					var capturedMetadata map[string]interface{}
					var capturedHeaders http.Header
					capture := func(w http.ResponseWriter, r *http.Request) {
						var body struct {
							Metadata map[string]interface{} `json:"metadata"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						capturedMetadata = body.Metadata
						capturedHeaders = r.Header.Clone()
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"id":"build-1","version":"v1"}`))
					}
					metadata := build.CollectMetadataWithContext(ctx, t.TempDir(), "", "android", 0)
					if source == "file" {
						client, artifactPath, _, _ := testUploadBuildClient(t, func(w http.ResponseWriter, r *http.Request) {
							if r.Header.Get("X-CI-System") != "" {
								t.Error("CI context leaked to artifact storage")
							}
							w.WriteHeader(http.StatusOK)
						}, capture)
						_, err = client.UploadBuild(ctx, &UploadBuildRequest{AppID: "app-1", Version: "v1", FilePath: artifactPath, Metadata: metadata})
					} else {
						server := httptest.NewServer(http.HandlerFunc(capture))
						defer server.Close()
						client := NewClientWithBaseURL("test-key", server.URL)
						_, err = client.CreateBuildFromURL(ctx, &CreateBuildFromURLRequest{AppID: "app-1", Version: "v1", FromURL: "https://artifacts.example/app.apk", Metadata: metadata})
					}
					if err != nil {
						t.Fatal(err)
					}
					if capturedMetadata["scm_head_sha"] != sha || capturedMetadata["scm_repo"] != "acme/mobile" || capturedMetadata["scm_provider"] != "github" || capturedMetadata["scm_platform"] != "android" {
						t.Fatalf("unexpected SCM metadata: %#v", capturedMetadata)
					}
					if capturedHeaders.Get("X-CI-Commit-SHA") != sha || capturedHeaders.Get("X-CI-Repository") != "acme/mobile" {
						t.Error("explicit headers did not override runner identity")
					}
					if _, exists := capturedMetadata["scm_base_sha"]; exists {
						t.Error("inherited base SHA from a different review")
					}
					if pr == 0 {
						if _, exists := capturedMetadata["scm_review_number"]; exists {
							t.Error("inherited PR number from runner")
						}
						if capturedHeaders.Get("X-CI-PR-Number") != "" {
							t.Error("inherited PR header from runner")
						}
					} else if capturedMetadata["scm_review_number"] != float64(pr) || capturedHeaders.Get("X-CI-PR-Number") != strconv.Itoa(pr) {
						t.Error("missing explicit PR number")
					}
				})
			}
		}
	}
}

func TestUploadBuildBuildkiteSCMContext(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		pullRequest string
		headCommit  string
		wantSHA     string
	}{
		{name: "pull request", pullRequest: "42", wantSHA: "commit-sha"},
		{name: "merge commit", pullRequest: "42", headCommit: "pr-head-sha", wantSHA: "pr-head-sha"},
		{name: "push without PR number", pullRequest: "false", wantSHA: "commit-sha"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", "")
			t.Setenv("BUILDKITE", "true")
			t.Setenv("BUILDKITE_REPO", "git@github.com:acme/mobile.git")
			t.Setenv("BUILDKITE_PULL_REQUEST_REPO", "git@github.com:contributor/mobile.git")
			t.Setenv("BUILDKITE_PULL_REQUEST", testCase.pullRequest)
			t.Setenv("BUILDKITE_PULL_REQUEST_HEAD_COMMIT", testCase.headCommit)
			t.Setenv("BUILDKITE_COMMIT", "commit-sha")

			var capturedMetadata map[string]interface{}
			var capturedHeaders http.Header
			client, artifactPath, _, _ := testUploadBuildClient(t,
				func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-CI-System") != "" {
						t.Error("CI context leaked to artifact storage")
					}
					w.WriteHeader(http.StatusOK)
				},
				func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Metadata map[string]interface{} `json:"metadata"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					capturedMetadata = body.Metadata
					capturedHeaders = r.Header.Clone()
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"build-1","version":"v1"}`))
				},
			)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := client.UploadBuild(ctx, &UploadBuildRequest{
				AppID: "app-1", Version: "v1", FilePath: artifactPath,
				Metadata: build.CollectMetadata(t.TempDir(), "", "android", 0),
			})
			if err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]interface{}{
				"scm_provider": "github", "scm_repo": "acme/mobile",
				"scm_head_sha": testCase.wantSHA, "scm_platform": "android", "ci_system": "buildkite",
			} {
				if got := capturedMetadata[key]; got != want {
					t.Errorf("metadata %s = %v, want %v", key, got, want)
				}
			}
			if capturedHeaders.Get("X-CI-Commit-SHA") != testCase.wantSHA || capturedHeaders.Get("X-CI-System") != "buildkite" {
				t.Error("upload headers do not match the Buildkite body context")
			}
			if testCase.pullRequest == "false" {
				if _, exists := capturedMetadata["scm_review_number"]; exists {
					t.Error("push metadata includes a PR number")
				}
				if capturedHeaders.Get("X-CI-PR-Number") != "" {
					t.Error("push headers include a PR number")
				}
			} else if capturedMetadata["scm_review_number"] != float64(42) || capturedHeaders.Get("X-CI-PR-Number") != "42" {
				t.Error("upload is missing its PR number")
			}
		})
	}
}

// TestExecuteWorkflow_SendsCIHeaders is a wire-level smoke test: it spins up a
// fake backend, runs the real ExecuteWorkflow code path against it with
// faked GitHub Actions env vars, and asserts the X-CI-* headers arrived
// correctly. This is the closest we can get to option (a) without a real
// staging backend / API key.
func TestExecuteWorkflow_SendsCIHeaders(t *testing.T) {
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	prPayload := `{"pull_request":{"html_url":"https://github.com/acme/web/pull/482","number":482}}`
	if err := os.WriteFile(eventPath, []byte(prPayload), 0o600); err != nil {
		t.Fatalf("write event: %v", err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "acme/web")
	t.Setenv("GITHUB_ACTOR", "janedoe")
	t.Setenv("GITHUB_SHA", "9f2c1ab0")
	t.Setenv("GITHUB_REF_NAME", "feat/checkout-rework")
	t.Setenv("GITHUB_RUN_ID", "12345")
	t.Setenv("GITHUB_EVENT_PATH", eventPath)

	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Snapshot headers off the request before responding.
		captured = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"task_id": "abc-123",
			"status":  "queued",
		})
	}))
	defer server.Close()

	client := &Client{
		baseURL:        server.URL,
		apiKey:         "test-key",
		version:        "test",
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		maxRetries:     0,
		retryBaseDelay: time.Millisecond,
		retryMaxDelay:  time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.ExecuteWorkflow(ctx, &ExecuteWorkflowRequest{
		WorkflowID: "wf-abc",
		Retries:    1,
	})
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}
	if resp.TaskID != "abc-123" {
		t.Fatalf("task_id = %q, want abc-123", resp.TaskID)
	}

	want := map[string]string{
		"X-Revyl-Client":  "cli",
		"X-Ci-System":     "github-actions",
		"X-Ci-Commit-Sha": "9f2c1ab0",
		"X-Ci-Branch":     "feat/checkout-rework",
		"X-Ci-Run-Id":     "12345",
		"X-Ci-Repository": "acme/web",
		"X-Ci-Actor":      "janedoe",
		"X-Ci-Actor-Url":  "https://github.com/janedoe",
		"X-Ci-Run-Url":    "https://github.com/acme/web/actions/runs/12345",
		"X-Ci-Pr-Url":     "https://github.com/acme/web/pull/482",
		"X-Ci-Pr-Number":  "482",
	}
	for key, expected := range want {
		got := captured.Get(key)
		if got != expected {
			t.Errorf("header %s = %q, want %q", key, got, expected)
		}
	}

	if got := captured.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
}

// TestExecuteWorkflow_NoCIHeadersOutsideSupportedCI confirms manual CLI runs are
// completely unchanged — no X-CI-* headers leak into the request.
func TestExecuteWorkflow_NoCIHeadersOutsideSupportedCI(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("BUILDKITE", "")

	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"task_id": "abc-123"})
	}))
	defer server.Close()

	client := &Client{
		baseURL:        server.URL,
		apiKey:         "test-key",
		version:        "test",
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		maxRetries:     0,
		retryBaseDelay: time.Millisecond,
		retryMaxDelay:  time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.ExecuteWorkflow(ctx, &ExecuteWorkflowRequest{WorkflowID: "wf-abc"}); err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}

	for key := range captured {
		if len(key) >= 5 && (key[:5] == "X-Ci-" || key[:5] == "X-CI-") {
			t.Errorf("unexpected CI header outside supported CI: %s = %q", key, captured.Get(key))
		}
	}
	// Sanity: X-Revyl-Client always sent.
	if got := captured.Get("X-Revyl-Client"); got != "cli" {
		t.Errorf("X-Revyl-Client = %q, want cli", got)
	}
}
