package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func newResolverTestClient(t *testing.T, handler http.HandlerFunc) (*api.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	return api.NewClientWithBaseURL("test-key", srv.URL), srv.Close
}

func TestGetTestStatus_OrphanedMissing(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_test_by_id/missing-id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"remote test not found"}`))
	})
	defer cleanup()

	cfg := &config.ProjectConfig{Tests: map[string]string{"login-flow": "missing-id"}}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

	status, err := resolver.getTestStatus(context.Background(), "login-flow")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusOrphaned {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusOrphaned.String())
	}
	if status.LinkIssue != RemoteLinkIssueMissing {
		t.Fatalf("link issue = %s, want %s", status.LinkIssue, RemoteLinkIssueMissing)
	}
}

func TestGetTestStatus_OrphanedInvalidID(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_test_by_id/invalid-id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"Invalid test ID format"}`))
	})
	defer cleanup()

	cfg := &config.ProjectConfig{Tests: map[string]string{"login-flow": "invalid-id"}}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

	status, err := resolver.getTestStatus(context.Background(), "login-flow")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusOrphaned {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusOrphaned.String())
	}
	if status.LinkIssue != RemoteLinkIssueInvalidID {
		t.Fatalf("link issue = %s, want %s", status.LinkIssue, RemoteLinkIssueInvalidID)
	}
}

func TestGetTestStatus_OrphanedForbidden(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_test_by_id/forbidden-id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Not authorized to access this test"}`))
	})
	defer cleanup()

	cfg := &config.ProjectConfig{Tests: map[string]string{"login-flow": "forbidden-id"}}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{})

	status, err := resolver.getTestStatus(context.Background(), "login-flow")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusOrphaned {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusOrphaned.String())
	}
	if status.LinkIssue != RemoteLinkIssueForbidden {
		t.Fatalf("link issue = %s, want %s", status.LinkIssue, RemoteLinkIssueForbidden)
	}
}

func TestGetTestStatus_FallbackToLocalRemoteID(t *testing.T) {
	client, cleanup := newResolverTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/get_test_by_id/stale-id":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
		case "/api/v1/tests/get_test_by_id/live-id":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"live-id","name":"Login","platform":"ios","tasks":[],"version":3}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	defer cleanup()

	local := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "live-id",
			RemoteVersion: 3,
			LocalVersion:  3,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Login", Platform: "ios"},
			Blocks:   []config.TestBlock{},
		},
	}

	cfg := &config.ProjectConfig{Tests: map[string]string{"login-flow": "stale-id"}}
	resolver := NewResolver(client, cfg, map[string]*config.LocalTest{"login-flow": local})

	status, err := resolver.getTestStatus(context.Background(), "login-flow")
	if err != nil {
		t.Fatalf("getTestStatus() error = %v", err)
	}
	if status.Status != StatusSynced {
		t.Fatalf("status = %s, want %s", status.Status.String(), StatusSynced.String())
	}
	if status.RemoteID != "live-id" {
		t.Fatalf("remote id = %s, want live-id", status.RemoteID)
	}
	if status.LinkIssue != RemoteLinkIssueNone {
		t.Fatalf("link issue = %s, want none", status.LinkIssue)
	}
}
