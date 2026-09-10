package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

func newPublicBuildTestCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := newBuildTestCommand()
	cmd.Flags().AddFlagSet(buildCmd.Flags())
	buildCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		previousValue, previousChanged := flag.Value.String(), flag.Changed
		if slice, ok := flag.Value.(pflag.SliceValue); ok {
			previousSlice := append([]string(nil), slice.GetSlice()...)
			t.Cleanup(func() {
				if err := slice.Replace(previousSlice); err != nil {
					t.Error(err)
				}
				flag.Changed = previousChanged
			})
			if err := slice.Replace(nil); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Cleanup(func() {
				if err := flag.Value.Set(previousValue); err != nil {
					t.Error(err)
				}
				flag.Changed = previousChanged
			})
			if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatal(err)
			}
		}
		flag.Changed = false
	})
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestBuildExecutionFlags(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "default cloud", want: "no app is configured for remote build"},
		{name: "explicit cloud", args: []string{"--remote"}, want: "no app is configured for remote build"},
		{name: "local false", args: []string{"--local=false"}, want: "no app is configured for remote build"},
		{name: "local", args: []string{"--local"}, want: "local builds are not supported on Windows"},
		{name: "remote false", args: []string{"--remote=false"}, want: "local builds are not supported on Windows"},
		{name: "matching local flags", args: []string{"--local", "--remote=false"}, want: "local builds are not supported on Windows"},
		{name: "conflicting flags", args: []string{"--local", "--remote"}, want: "--local and --remote cannot be used together"},
		{name: "reversed conflict", args: []string{"--remote=true", "--local=true"}, want: "--local and --remote cannot be used together"},
		{name: "platform", args: []string{"--platform", "windows"}, want: "--platform must be ios or android"},
		{name: "local env", args: []string{"--local", "--env", "PLAIN=value"}, want: "--env is only supported for cloud builds"},
		{name: "local image", args: []string{"--local", "--image", "android-test"}, want: "--image is only supported for cloud builds"},
		{name: "local timeout", args: []string{"--local", "--timeout=0"}, want: "--timeout is only supported for cloud builds"},
		{name: "local detach", args: []string{"--local", "--detach"}, want: "--detach is only supported for cloud builds"},
		{name: "local cache", args: []string{"--local", "--no-cache"}, want: "--no-cache is only supported for cloud builds"},
		{name: "remote false detach", args: []string{"--remote=false", "--detach"}, want: "--detach is only supported for cloud builds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("REVYL_API_KEY", "test-key")
			previousGOOS := buildHostGOOS
			buildHostGOOS = "windows"
			t.Cleanup(func() { buildHostGOOS = previousGOOS })
			root := t.TempDir()
			gitInitBuildRepository(t, root)
			writeProjectBuildConfig(t, root, projectBuildConfigYAML("development", "android", "build/app.apk", false))
			withWorkingDir(t, root)
			cmd := newPublicBuildTestCommand(t, append(test.args, "--json")...)
			output := captureStdout(t, func() {
				err := runBuild(cmd, nil)
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("runBuild() = %v, want %q", err, test.want)
				}
				if strings.Contains(err.Error(), "runs in the cloud by default") != (buildCommandRemote && !buildCommandLocal) {
					t.Fatalf("execution-mode guidance does not match selected mode: %v", err)
				}
			})
			if output != "" {
				t.Fatalf("validation polluted JSON stdout: %q", output)
			}
		})
	}
}

func TestDefaultBuildAuthenticationErrorExplainsNextSteps(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	cmd := newPublicBuildTestCommand(t, "--json")
	output := captureStdout(t, func() {
		err := runBuild(cmd, nil)
		if err == nil {
			t.Fatal("expected authentication failure")
		}
		for _, want := range []string{"not authenticated", "revyl auth login", "runs in the cloud by default", "revyl build --local", "still require authentication"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want %q", err, want)
			}
		}
	})
	if output != "" {
		t.Fatalf("authentication failure polluted JSON stdout: %q", output)
	}
}

func TestPublicBuildDefaultsToRemoteSubmission(t *testing.T) {
	for _, test := range []struct {
		name, platform, status string
		args                   []string
		failEnqueue            bool
		bare                   bool
	}{
		{name: "bare build", platform: "android", status: "success", bare: true},
		{name: "android waits", platform: "android", status: "success"},
		{name: "ios waits", platform: "ios", status: "success"},
		{name: "detached", platform: "android", status: "pending", args: []string{"--detach"}},
		{name: "remote compatibility", platform: "ios", status: "pending", args: []string{"--remote", "--detach", "--profile=release", "--platform=ios"}},
		{name: "no fallback", platform: "android", failEnqueue: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			withFastRemoteBuildPolling(t)
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-key")
			t.Setenv("CI", "true")
			root := t.TempDir()
			gitInitBuildRepository(t, root)
			configYAML := projectBuildConfigYAML("release", test.platform, "build/app.apk", true)
			configYAML = strings.Replace(configYAML, `build_commands: ["true"]`, `build_commands: ["touch local-build-ran"]`, 1)
			writeProjectBuildConfig(t, root, configYAML)
			withWorkingDir(t, root)
			var request api.RemoteBuildRequest
			var appLookups, statusCalls, submissions int
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/00000000-0000-4000-8000-000000000001":
					appLookups++
					_ = json.NewEncoder(w).Encode(api.App{
						ID: "00000000-0000-4000-8000-000000000001", Platform: strings.ToUpper(test.platform),
						LatestVersion: "1.0", VersionsCount: 1,
					})
				case "/api/v1/apps/remote/upload-url":
					_ = json.NewEncoder(w).Encode(api.RemoteBuildSourceUploadResponse{UploadUrl: server.URL + "/source", SourceKey: "source.tar.gz"})
				case "/source":
					_, _ = io.Copy(io.Discard, r.Body)
				case "/api/v1/apps/remote":
					submissions++
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if test.failEnqueue {
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"detail":"cloud build access denied"}`)
						return
					}
					_, _ = io.WriteString(w, `{"build_job_id":"job-1"}`)
				case "/api/v1/apps/remote/job-1/status":
					statusCalls++
					_, _ = io.WriteString(w, `{"status":"success","version_id":"version-1","version":"1.2.3"}`)
				case "/api/v1/apps/remote/job-1/logs":
					_, _ = io.WriteString(w, `{"events":[]}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			args := append([]string{"--json", "--image=test-image", "--env=PLAIN=value", "--secret=BUILD_TOKEN", "--timeout=90", "--no-cache", "--no-set-current", "--version=1.2.3"}, test.args...)
			if test.bare {
				args = nil
			}
			cmd := newPublicBuildTestCommand(t, args...)
			var captured analytics.TelemetryPayload
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { captured = payload })
			run := recorder.StartCommand(cmd, nil)
			cmd.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
			var buildErr error
			output := captureStdout(t, func() { buildErr = runBuild(cmd, nil) })
			run.Complete(buildErr)
			recorder.Flush()
			if test.failEnqueue {
				if buildErr == nil || !strings.Contains(buildErr.Error(), "cloud build access denied") || output != "" {
					t.Fatalf("build error = %v, stdout = %q", buildErr, output)
				}
				var apiError *api.APIError
				if !errors.As(buildErr, &apiError) || apiError.StatusCode != http.StatusForbidden {
					t.Fatalf("cloud failure lost its API error: %v", buildErr)
				}
				for _, want := range []string{"runs in the cloud by default", "revyl build --local", "upload the artifact"} {
					if !strings.Contains(buildErr.Error(), want) {
						t.Errorf("cloud error = %q, want %q", buildErr, want)
					}
				}
			} else {
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				if test.bare {
					if output != "" {
						t.Fatalf("human build progress must stay on stderr: %q", output)
					}
				} else {
					result := parseJSON(t, output)
					assertJSONString(t, result, "status", test.status)
					assertJSONString(t, result, "build_job_id", "job-1")
					assertJSONString(t, result, "profile", "release")
					assertJSONString(t, result, "platform", test.platform)
				}
			}
			if appLookups != 1 || submissions != 1 || (statusCalls > 0) != (test.status == "success") {
				t.Fatalf("app lookups = %d, submissions = %d, status calls = %d", appLookups, submissions, statusCalls)
			}
			if request.Config.Platform == nil || string(*request.Config.Platform) != test.platform || request.Config.Steps == nil || len(*request.Config.Steps) != 2 || (*request.Config.Steps)[1].Command == nil || *(*request.Config.Steps)[1].Command != "touch local-build-ran" {
				t.Fatalf("recipe not preserved: %+v", request.Config)
			}
			if test.bare {
				if request.Image != nil || request.TimeoutSeconds != nil || request.Version != nil || request.SetAsCurrent == nil || !*request.SetAsCurrent {
					t.Fatalf("remote defaults not preserved: %+v", request)
				}
			} else {
				if request.Image == nil || *request.Image != "test-image" || request.TimeoutSeconds == nil || *request.TimeoutSeconds != 90 || request.SetAsCurrent == nil || *request.SetAsCurrent || request.CleanBuild == nil || !*request.CleanBuild {
					t.Fatalf("remote options not preserved: %+v", request)
				}
				if request.Config.Env == nil || (*request.Config.Env)["PLAIN"] != "value" || request.Config.SecretRefs == nil || !reflect.DeepEqual(*request.Config.SecretRefs, []string{"BUILD_TOKEN"}) {
					t.Fatalf("environment and secret references not preserved: %+v", request.Config)
				}
				if request.Version == nil || *request.Version != "1.2.3" {
					t.Fatalf("version not preserved: %+v", request)
				}
			}
			if request.BuildDefinitionHash == nil || *request.BuildDefinitionHash == "" {
				t.Fatal("build definition hash missing")
			}
			if _, err := os.Stat(filepath.Join(root, "local-build-ran")); !os.IsNotExist(err) {
				t.Fatalf("local recipe ran: %v", err)
			}
			if len(captured.Events) != 2 || captured.Events[1].Properties["build_mode"] != "remote" {
				t.Fatalf("expected one remote lifecycle: %+v", captured.Events)
			}
			wantDomainStatus := "completed"
			if test.failEnqueue {
				wantDomainStatus = "failed"
			} else if test.status == "pending" {
				wantDomainStatus = "queued"
			}
			if got := captured.Events[1].Properties["domain_status"]; got != wantDomainStatus {
				t.Fatalf("domain status = %v, want %s", got, wantDomainStatus)
			}
		})
	}
}

func TestRemoteBuildRejectsInvalidAppBeforeSourceUpload(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{"missing app", http.StatusNotFound, `{"detail":"App not found"}`, "validate remote build app"},
		{"inaccessible app", http.StatusForbidden, `{"detail":"Forbidden"}`, "validate remote build app"},
		{"lookup failure", http.StatusBadGateway, `{"detail":"Unavailable"}`, "validate remote build app"},
		{"malformed response", http.StatusOK, `{`, "validate remote build app"},
		{"wrong platform", http.StatusOK, `{"platform":"iOS"}`, "revyl app list --platform android"},
		{"missing platform", http.StatusOK, `{}`, "revyl app list --platform android"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const appID = "00000000-0000-4000-8000-000000000001"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/apps/"+appID {
					t.Errorf("unexpected request before app validation: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("app lookup must be authenticated")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.statusCode)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			projectRoot := t.TempDir()
			progress := &buildProgress{}
			output := captureStdout(t, func() {
				err := runRemoteBuildWithOptions(newBuildTestCommand(), "test-key", remoteBuildOptions{
					ProjectRoot: projectRoot, WorktreeRoot: projectRoot, JSON: true,
					Resolved:        &remoteBuildPlatformConfig{AppID: appID, Platform: "android"},
					SetFailureStage: progress.markFailureStage,
				})
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("runRemoteBuildWithOptions() = %v, want %q", err, test.want)
				}
			})
			if output != "" || progress.failureStage != "app_resolution" {
				t.Fatalf("stdout = %q, failure stage = %q", output, progress.failureStage)
			}
		})
	}
}

func TestBuildExecutionHelpAndDevDefaults(t *testing.T) {
	if buildCmd.Flags().Lookup("remote").DefValue != "true" || buildCmd.Flags().Lookup("local").DefValue != "false" {
		t.Fatal("build execution flag defaults must be remote-first")
	}
	for _, text := range []string{"By default", "cloud build usage", "--local", "--remote=false", "without falling back"} {
		if !strings.Contains(buildCmd.Long, text) {
			t.Errorf("build help missing %q", text)
		}
	}
	if devCmd.Flags().Lookup("remote").DefValue != "false" || devCmd.Flags().Lookup("local") != nil {
		t.Fatal("build defaults must not change dev execution flags")
	}
	for _, name := range []string{"machine", "runner"} {
		if buildCmd.Flags().Lookup(name) != nil {
			t.Errorf("build must not expose --%s", name)
		}
	}
}
