package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

type fakeRemoteBuildLogBackend struct {
	mu             sync.Mutex
	messages       []string
	statuses       []string
	messagesAtPoll map[int]string
	polls          int
}

func (b *fakeRemoteBuildLogBackend) appendMessage(message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, message)
}

func (b *fakeRemoteBuildLogBackend) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/remote/job-1/logs":
			after := 0
			if cursor := r.URL.Query().Get("after_id"); cursor != "" {
				after, _ = strconv.Atoi(strings.TrimSuffix(cursor, "-0"))
			}
			events := []api.RemoteBuildLogEvent{}
			for i := after; i < len(b.messages); i++ {
				events = append(events, api.RemoteBuildLogEvent{Id: strconv.Itoa(i+1) + "-0", Message: b.messages[i]})
			}
			next := strconv.Itoa(len(b.messages)) + "-0"
			_ = json.NewEncoder(w).Encode(api.RemoteBuildLogsResponse{Events: &events, NextCursor: &next})
		case "/api/v1/apps/remote/job-1/status":
			if message, ok := b.messagesAtPoll[b.polls]; ok {
				b.messages = append(b.messages, message)
			}
			status := b.statuses[min(b.polls, len(b.statuses)-1)]
			b.polls++
			_ = json.NewEncoder(w).Encode(api.RemoteBuildStatusResponse{Status: status})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Build job not found"}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runBuildLogsForTest(t *testing.T, backendURL, jobID string, follow, jsonOutput bool) (string, string, error) {
	t.Helper()
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", backendURL)
	previousFollow, previousJSON, previousInterval := buildLogsFollow, buildLogsJSON, remoteBuildLogPollInterval
	buildLogsFollow, buildLogsJSON, remoteBuildLogPollInterval = follow, jsonOutput, time.Millisecond
	t.Cleanup(func() {
		buildLogsFollow, buildLogsJSON, remoteBuildLogPollInterval = previousFollow, previousJSON, previousInterval
	})
	buildLogsCmd.SetContext(context.Background())
	var err error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		err = runBuildLogs(buildLogsCmd, []string{jobID})
	})
	return stdout, stderr, err
}

func TestBuildLogsPrintsTheWholeLogFromTheStart(t *testing.T) {
	backend := &fakeRemoteBuildLogBackend{statuses: []string{"failed"}}
	for i := range 600 {
		backend.appendMessage("CompileSwift line " + strconv.Itoa(i))
	}
	backend.appendMessage("error: cannot find 'axisBehavior' in scope")
	server := backend.server(t)

	stdout, stderr, err := runBuildLogsForTest(t, server.URL, "job-1", false, false)

	if err != nil {
		t.Fatalf("runBuildLogs() error = %v", err)
	}
	output := stdout + stderr
	for _, want := range []string{"CompileSwift line 0", "CompileSwift line 599", "cannot find 'axisBehavior' in scope"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output is missing %q:\n%s", want, output)
		}
	}
	if backend.polls != 0 {
		t.Fatalf("status polled %d times without --follow", backend.polls)
	}
}

func TestBuildLogsFollowStreamsUntilTheBuildEnds(t *testing.T) {
	backend := &fakeRemoteBuildLogBackend{
		statuses:       []string{"running", "running", "failed"},
		messagesAtPoll: map[int]string{1: "error: late failure"},
	}
	backend.appendMessage("step one")
	server := backend.server(t)

	stdout, stderr, err := runBuildLogsForTest(t, server.URL, "job-1", true, false)

	if err != nil {
		t.Fatalf("runBuildLogs() error = %v", err)
	}
	output := stdout + stderr
	if !strings.Contains(output, "step one") || !strings.Contains(output, "late failure") {
		t.Fatalf("followed output is missing events:\n%s", output)
	}
	if !strings.Contains(output, "Build failed (job-1)") {
		t.Fatalf("follow did not report the terminal state:\n%s", output)
	}
	if backend.polls != 3 {
		t.Fatalf("status polls = %d, want 3 (stop at the first terminal state)", backend.polls)
	}
}

func TestBuildLogsJSONWritesEachEventAsOneLine(t *testing.T) {
	backend := &fakeRemoteBuildLogBackend{statuses: []string{"success"}}
	backend.appendMessage("first")
	backend.appendMessage("second")
	server := backend.server(t)

	stdout, _, err := runBuildLogsForTest(t, server.URL, "job-1", true, true)

	if err != nil {
		t.Fatalf("runBuildLogs() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout lines = %d, want one per event:\n%s", len(lines), stdout)
	}
	for i, want := range []string{"first", "second"} {
		var event api.RemoteBuildLogEvent
		if err := json.Unmarshal([]byte(lines[i]), &event); err != nil {
			t.Fatalf("line %d is not a JSON event: %v\n%s", i, err, lines[i])
		}
		if event.Message != want || event.Id != strconv.Itoa(i+1)+"-0" {
			t.Fatalf("event %d = %+v, want message %q", i, event, want)
		}
	}
}

func TestBuildLogsUnknownJobFails(t *testing.T) {
	backend := &fakeRemoteBuildLogBackend{statuses: []string{"failed"}}
	server := backend.server(t)

	_, _, err := runBuildLogsForTest(t, server.URL, "missing-job", true, false)

	if err == nil || !strings.Contains(err.Error(), "failed to fetch build logs") {
		t.Fatalf("runBuildLogs() error = %v, want a fetch failure", err)
	}
}

func TestBuildFailureLogTailPointsToTheFullLog(t *testing.T) {
	backend := &fakeRemoteBuildLogBackend{statuses: []string{"failed"}}
	backend.appendMessage("error: no such module 'DemoKit'")
	server := backend.server(t)
	client := api.NewClientWithBaseURL("test-key", server.URL)

	output := captureStdoutAndStderr(t, func() {
		printRemoteBuildLogTail(context.Background(), client, "job-1")
	})

	if !strings.Contains(output, "Full log: revyl build logs job-1") {
		t.Fatalf("failure tail does not point to the full log:\n%s", output)
	}
}
