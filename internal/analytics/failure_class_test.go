package analytics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/orgguard"
	"github.com/revyl/cli/internal/ui"
)

func TestClassifyFailure(t *testing.T) {
	transportErr := &url.Error{Op: "Post", URL: "https://backend.example", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	for _, tc := range []struct {
		name       string
		err        error
		completion CommandCompletion
		want       FailureClass
	}{
		{name: "panic", err: ErrCommandPanicked, want: FailureClassInternal},
		{name: "context cancelled", err: fmt.Errorf("wait: %w", context.Canceled), want: FailureClassCancelled},
		{name: "selection cancelled", err: ui.ErrSelectionCancelled, want: FailureClassCancelled},
		{name: "deadline", err: fmt.Errorf("poll: %w", context.DeadlineExceeded), want: FailureClassTimeout},
		{name: "unauthorized", err: &api.APIError{StatusCode: 401}, want: FailureClassAuth},
		{name: "forbidden", err: fmt.Errorf("list apps: %w", &api.APIError{StatusCode: 403}), want: FailureClassAuth},
		{name: "allowance", err: &api.APIError{StatusCode: 402}, want: FailureClassQuota},
		{name: "rate limited", err: &api.APIError{StatusCode: 429}, want: FailureClassQuota},
		{name: "server error", err: &api.APIError{StatusCode: 503}, want: FailureClassServer},
		{name: "not found", err: &api.APIError{StatusCode: 404}, want: FailureClassUsage},
		{name: "rejected request", err: &api.APIError{StatusCode: 422}, want: FailureClassUsage},
		{name: "device authorization denied", err: auth.ErrDeviceAuthorizationDenied, want: FailureClassAuth},
		{name: "device authorization expired", err: auth.ErrDeviceAuthorizationExpired, want: FailureClassAuth},
		{name: "org mismatch", err: &orgguard.MismatchError{}, want: FailureClassAuth},
		{name: "config error", err: &config.ConfigError{Stage: "read", Code: "config_not_found"}, want: FailureClassConfig},
		{name: "missing project root", err: &config.MissingProjectRootError{}, want: FailureClassConfig},
		{name: "ambiguous project roots", err: &config.AmbiguousProjectRootsError{}, want: FailureClassConfig},
		{name: "build tool", err: &build.BuildToolError{Message: "bazel not found"}, want: FailureClassBuild},
		{name: "upload rejected", err: &api.UploadHTTPError{StatusCode: 403}, want: FailureClassUpload},
		{name: "device stop pending", err: &api.DeviceSessionStopPendingError{}, want: FailureClassDevice},
		{name: "transport", err: fmt.Errorf("request failed: %w", transportErr), want: FailureClassNetwork},
		{
			name: "marked exit path",
			err:  fmt.Errorf("connect: %w", WithFailureClass(errors.New("install not finished"), FailureClassGitHub)),
			want: FailureClassGitHub,
		},
		{
			name: "marker wins over its typed cause",
			err:  WithFailureClass(&api.APIError{StatusCode: 404}, FailureClassConfig),
			want: FailureClassConfig,
		},
		{
			name:       "build stage classifies an untyped failure",
			err:        errors.New("exit status 65"),
			completion: CommandCompletion{Properties: map[string]interface{}{"build_failure_stage": "build"}},
			want:       FailureClassBuild,
		},
		{
			name:       "typed cause beats the build stage",
			err:        fmt.Errorf("upload source: %w", transportErr),
			completion: CommandCompletion{Properties: map[string]interface{}{"build_failure_stage": "source_upload"}},
			want:       FailureClassNetwork,
		},
		{
			name:       "unknown build stage",
			err:        errors.New("failed"),
			completion: CommandCompletion{Properties: map[string]interface{}{"build_failure_stage": "unlisted"}},
			want:       FailureClassUnknown,
		},
		{name: "error text is never read", err: errors.New("401 unauthorized: network timeout"), want: FailureClassUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyFailure(tc.err, tc.completion); got != tc.want {
				t.Fatalf("classifyFailure() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildFailureStagesAllClassify(t *testing.T) {
	for _, stage := range []string{
		"validation", "authentication", "configuration", "secret_validation", "setup", "build",
		"artifact_resolution", "app_resolution", "upload", "source_archive", "source_upload", "enqueue", "poll",
	} {
		if _, known := buildFailureStageClasses[stage]; !known {
			t.Errorf("build_failure_stage %q has no failure class", stage)
		}
	}
}

func TestWithFailureClassPreservesTheReturnedError(t *testing.T) {
	original := errors.New("--file and --url are mutually exclusive")
	marked := WithFailureClass(original, FailureClassUsage)

	if marked.Error() != original.Error() {
		t.Fatalf("Error() = %q, want %q", marked.Error(), original.Error())
	}
	if !errors.Is(marked, original) {
		t.Fatal("marked error does not unwrap to the original")
	}
	if WithFailureClass(nil, FailureClassUsage) != nil {
		t.Fatal("marking a nil error must stay nil")
	}
}

func TestFailedEventCarriesOnlyTheBoundedFailureClass(t *testing.T) {
	recorder := testRecorder()
	run := testCommandRun(recorder)

	run.Complete(fmt.Errorf("upload customer-app.ipa: %w", &api.APIError{StatusCode: 402, Message: "customer detail"}))

	event := lastEvent(t, recorder)
	if event.Event != CliCommandFailedEvent {
		t.Fatalf("event = %q, want %q", event.Event, CliCommandFailedEvent)
	}
	if got := event.Properties["failure_class"]; got != string(FailureClassQuota) {
		t.Fatalf("failure_class = %v, want %q", got, FailureClassQuota)
	}
}

func TestOnlyFailedEventsCarryFailureClass(t *testing.T) {
	for _, err := range []error{
		nil,
		CompletedWithExitCode(errors.New("1 test failed"), CommandCompletion{ExitCode: 1, Domain: "test_run", DomainStatus: "failed"}),
	} {
		recorder := testRecorder()
		run := testCommandRun(recorder)
		run.Complete(err)
		if _, exists := lastEvent(t, recorder).Properties["failure_class"]; exists {
			t.Fatalf("completed event for %v carries failure_class", err)
		}
	}
}
