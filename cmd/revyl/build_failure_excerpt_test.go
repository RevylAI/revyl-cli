package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestExtractBuildFailureExcerptFindsXcodebuildErrorsBeforeTheEnvDump(t *testing.T) {
	log := []string{
		"step:start build-ios",
		"CompileSwift normal arm64 /src/App/ContentView.swift",
		"/src/App/ContentView.swift:42:17: error: cannot find 'axisBehavior' in scope",
		"        .scrollBounceBehavior(axisBehavior)",
		"                              ^~~~~~~~~~~~",
		"CompileSwift normal arm64 /src/App/Other.swift",
		"",
		"** BUILD FAILED **",
		"",
		"The following build commands failed:",
		"\tCompileSwift normal arm64 /src/App/ContentView.swift",
		"(1 failure)",
		"GCC_TREAT_WARNINGS_AS_ERRORS=NO",
		"SWIFT_SUPPRESS_ERRORS=NO",
		"PATH=/usr/bin:/bin",
	}

	got := extractBuildFailureExcerpt(log)

	want := []string{
		"/src/App/ContentView.swift:42:17: error: cannot find 'axisBehavior' in scope",
		"        .scrollBounceBehavior(axisBehavior)",
		"                              ^~~~~~~~~~~~",
		"...",
		"** BUILD FAILED **",
		"The following build commands failed:",
		"\tCompileSwift normal arm64 /src/App/ContentView.swift",
		"(1 failure)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("excerpt =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestExtractBuildFailureExcerptFindsGradleFailureBlockAndKotlinErrors(t *testing.T) {
	log := []string{
		"> Task :app:compileDebugKotlin FAILED",
		"e: file:///src/app/src/main/java/com/example/MainActivity.kt:12:5 Unresolved reference: Greeting",
		"",
		"FAILURE: Build failed with an exception.",
		"",
		"* What went wrong:",
		"Execution failed for task ':app:compileDebugKotlin'.",
		"> A failure occurred while executing org.jetbrains.kotlin.compilerRunner.GradleCompilerRunnerWithWorkers$GradleKotlinCompilerWorkAction",
		"* Try:",
		"BUILD FAILED in 41s",
		"ANDROID_HOME=/opt/android-sdk",
	}

	got := extractBuildFailureExcerpt(log)

	want := []string{
		"e: file:///src/app/src/main/java/com/example/MainActivity.kt:12:5 Unresolved reference: Greeting",
		"FAILURE: Build failed with an exception.",
		"* What went wrong:",
		"Execution failed for task ':app:compileDebugKotlin'.",
		"...",
		"BUILD FAILED in 41s",
		"ANDROID_HOME=/opt/android-sdk",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("excerpt =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestExtractBuildFailureExcerptIsBoundedInMatchesAndLineLength(t *testing.T) {
	var log []string
	for i := range 30 {
		log = append(log, "error: failure "+strconv.Itoa(i)+" "+strings.Repeat("x", 1000))
	}

	got := extractBuildFailureExcerpt(log)

	wantLines := buildFailureExcerptMaxMatches + buildFailureExcerptContextLines
	if len(got) != wantLines {
		t.Fatalf("excerpt has %d lines, want %d", len(got), wantLines)
	}
	if !strings.HasPrefix(got[0], "error: failure 0 ") || !strings.HasPrefix(got[9], "error: failure 9 ") {
		t.Fatalf("excerpt does not keep the first errors in order: %q ... %q", got[0][:20], got[9][:20])
	}
	if runes := len([]rune(got[0])); runes != buildFailureExcerptMaxLineRunes+1 {
		t.Fatalf("line length = %d runes, want %d plus an ellipsis", runes, buildFailureExcerptMaxLineRunes)
	}
}

func TestExtractBuildFailureExcerptReturnsNothingForACleanLog(t *testing.T) {
	got := extractBuildFailureExcerpt([]string{"Compiling", "Linking", "SWIFT_TREAT_WARNINGS_AS_ERRORS=NO", "** BUILD SUCCEEDED **"})

	if len(got) != 0 {
		t.Fatalf("excerpt = %q, want none", got)
	}
}

func TestFetchRemoteBuildFailureExcerptStopsPagingOnceTheExcerptIsComplete(t *testing.T) {
	var messages []string
	for i := range 200 {
		messages = append(messages, "error: failure "+strconv.Itoa(i))
	}
	const pageSize = 5
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		after, _ := strconv.Atoi(strings.TrimSuffix(r.URL.Query().Get("after_id"), "-0"))
		end := min(after+pageSize, len(messages))
		events := []api.RemoteBuildLogEvent{}
		for i := after; i < end; i++ {
			events = append(events, api.RemoteBuildLogEvent{Id: strconv.Itoa(i+1) + "-0", Message: messages[i]})
		}
		next := strconv.Itoa(end) + "-0"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RemoteBuildLogsResponse{Events: &events, NextCursor: &next})
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	got := fetchRemoteBuildFailureExcerpt(context.Background(), api.NewClient("test-key"), "job-1")

	if len(got) != buildFailureExcerptMaxMatches+buildFailureExcerptContextLines || got[0] != "error: failure 0" {
		t.Fatalf("excerpt = %q, want the first %d errors and their context", got, buildFailureExcerptMaxMatches)
	}
	if requests != 3 {
		t.Fatalf("log pages read = %d, want 3 (stop once the excerpt is complete)", requests)
	}
}

func TestBuildStatusJSONAddsErrorExcerptForAFailedBuild(t *testing.T) {
	backend := &fakeRemoteBuildLogBackend{statuses: []string{"failed"}}
	backend.appendMessage("/src/App/ContentView.swift:42:17: error: cannot find 'axisBehavior' in scope")
	for i := range 50 {
		backend.appendMessage("ENV_VAR_" + strconv.Itoa(i) + "=value")
	}
	server := backend.server(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	previousJSON, previousFollow := buildStatusJSON, buildStatusFollow
	buildStatusJSON, buildStatusFollow = true, false
	t.Cleanup(func() { buildStatusJSON, buildStatusFollow = previousJSON, previousFollow })
	buildStatusCmd.SetContext(context.Background())

	var err error
	stdout, _ := captureStdoutAndStderrSeparate(t, func() {
		err = runBuildStatus(buildStatusCmd, []string{"job-1"})
	})

	if err != nil {
		t.Fatalf("runBuildStatus() error = %v", err)
	}
	output := parseJSON(t, stdout)
	assertJSONString(t, output, "status", "failed")
	excerpt, ok := output["error_excerpt"].([]interface{})
	if !ok || len(excerpt) == 0 || !strings.Contains(excerpt[0].(string), "cannot find 'axisBehavior' in scope") {
		t.Fatalf("error_excerpt = %v, want the compiler error", output["error_excerpt"])
	}
}

func TestBuildStatusJSONOmitsErrorExcerptForASuccessfulBuild(t *testing.T) {
	backend := &fakeRemoteBuildLogBackend{statuses: []string{"success"}}
	backend.appendMessage("warning: error: looks scary but the build passed")
	server := backend.server(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	previousJSON, previousFollow := buildStatusJSON, buildStatusFollow
	buildStatusJSON, buildStatusFollow = true, false
	t.Cleanup(func() { buildStatusJSON, buildStatusFollow = previousJSON, previousFollow })
	buildStatusCmd.SetContext(context.Background())

	var err error
	stdout, _ := captureStdoutAndStderrSeparate(t, func() {
		err = runBuildStatus(buildStatusCmd, []string{"job-1"})
	})

	if err != nil {
		t.Fatalf("runBuildStatus() error = %v", err)
	}
	output := parseJSON(t, stdout)
	if _, present := output["error_excerpt"]; present {
		t.Fatalf("successful build has error_excerpt: %v", output["error_excerpt"])
	}
}

func TestFetchRemoteBuildFailureExcerptFallsBackToTheLatestPageForALongLog(t *testing.T) {
	var messages []string
	for i := range (buildFailureExcerptMaxLogPages + 10) * 5 {
		messages = append(messages, "Compiling file "+strconv.Itoa(i))
	}
	messages = append(messages, "/src/App.swift:3:1: error: expected declaration", "** BUILD FAILED **")
	const pageSize = 5
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := len(messages) - pageSize
		if cursor := r.URL.Query().Get("after_id"); cursor != "" {
			start, _ = strconv.Atoi(strings.TrimSuffix(cursor, "-0"))
		}
		end := min(start+pageSize, len(messages))
		events := []api.RemoteBuildLogEvent{}
		for i := start; i < end; i++ {
			events = append(events, api.RemoteBuildLogEvent{Id: strconv.Itoa(i+1) + "-0", Message: messages[i]})
		}
		next := strconv.Itoa(end) + "-0"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RemoteBuildLogsResponse{Events: &events, NextCursor: &next})
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	got := fetchRemoteBuildFailureExcerpt(context.Background(), api.NewClient("test-key"), "job-1")

	if len(got) != 2 || got[0] != "/src/App.swift:3:1: error: expected declaration" || got[1] != "** BUILD FAILED **" {
		t.Fatalf("excerpt = %q, want the errors from the latest page", got)
	}
}
