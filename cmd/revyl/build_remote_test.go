package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

func withFastRemoteBuildPolling(t *testing.T) {
	t.Helper()
	previous := remoteBuildPollInterval
	remoteBuildPollInterval = time.Millisecond
	t.Cleanup(func() {
		remoteBuildPollInterval = previous
	})
}

func remoteBuildStatusServer(t *testing.T, status api.RemoteBuildStatusResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/remote/job-1/status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(status); err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
}

func TestPollBuildStatusTreatsCancelledAsTerminalError(t *testing.T) {
	withFastRemoteBuildPolling(t)
	errMsg := "Build cancelled"
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "cancelled",
		Error:  &errMsg,
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	err := pollBuildStatus(context.Background(), client, "job-1")

	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("pollBuildStatus() error = %v, want cancelled", err)
	}
}

func TestPollBuildStatusRejectsSuccessWithoutVersionID(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "success",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	err := pollBuildStatus(context.Background(), client, "job-1")

	if err == nil || !strings.Contains(err.Error(), "no build version ID") {
		t.Fatalf("pollBuildStatus() error = %v, want missing version ID", err)
	}
}

func TestPollRemoteBuildStatusResultTreatsCancelledAsTerminalError(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "cancelled",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1")

	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want cancelled", err)
	}
}

func TestPollRemoteBuildStatusResultRejectsSuccessWithoutVersionID(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "success",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1")

	if err == nil || !strings.Contains(err.Error(), "no build version ID") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want missing version ID", err)
	}
}
