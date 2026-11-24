package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishProofCommentSendsBodyAndProblem(t *testing.T) {
	var (
		seenPath   string
		seenMethod string
		seenAuth   string
		seenBody   ProofCommentRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenMethod = r.Method
		seenAuth = r.Header.Get("Authorization")

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(raw, &seenBody); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	if err := client.PublishProofComment(
		context.Background(), "## Proof\n\nIt works.", "Saving a note clears the title", "",
	); err != nil {
		t.Fatalf("PublishProofComment() error = %v", err)
	}

	if seenMethod != http.MethodPut {
		t.Fatalf("expected PUT so a re-post replaces the write-up, got %s", seenMethod)
	}
	if seenPath != "/api/v1/scm/proof-runs/comment" {
		t.Fatalf("unexpected path %q", seenPath)
	}
	if seenAuth != "Bearer test-key" {
		t.Fatalf("expected the request to be authenticated, got %q", seenAuth)
	}
	if seenBody.Body != "## Proof\n\nIt works." {
		t.Fatalf("markdown did not survive the round trip: %q", seenBody.Body)
	}
	if seenBody.Problem != "Saving a note clears the title" {
		t.Fatalf("unexpected problem %q", seenBody.Problem)
	}
}

func TestPublishProofCommentSendsBlockedWithoutProblem(t *testing.T) {
	var seenBody ProofCommentRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &seenBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	if err := client.PublishProofComment(
		context.Background(),
		"No device session was produced.",
		"",
		"No device could be started, so nothing was exercised",
	); err != nil {
		t.Fatalf("PublishProofComment() error = %v", err)
	}

	// An agent that observed nothing has not found a bug, so the blockage must
	// not land in the slot that fails the pull request.
	if seenBody.Blocked != "No device could be started, so nothing was exercised" {
		t.Fatalf("unexpected blocked sentence %q", seenBody.Blocked)
	}
	if seenBody.Problem != "" {
		t.Fatalf("a blocked run must not also report a problem, got %q", seenBody.Problem)
	}
}

func TestPublishProofCommentOmitsAbsentOutcomes(t *testing.T) {
	var seenKeys map[string]json.RawMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &seenKeys); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	if err := client.PublishProofComment(context.Background(), "progress so far", "", ""); err != nil {
		t.Fatalf("PublishProofComment() error = %v", err)
	}

	// The usual case is nothing broken and nothing blocked, neither of which
	// must arrive as an empty sentence sitting in an outcome slot.
	for _, field := range []string{"problem", "blocked"} {
		if _, present := seenKeys[field]; present {
			t.Fatalf("expected no %s field when the agent reported neither", field)
		}
	}
}

func TestPublishProofCommentSurfacesBackendRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"detail": "Proof run has already finished",
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	err := client.PublishProofComment(context.Background(), "too late", "", "")
	if err == nil {
		t.Fatal("expected an error once the run has finished")
	}
	if !strings.Contains(err.Error(), "already finished") {
		t.Fatalf("expected the backend's reason to survive, got %v", err)
	}
}
