package orgguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/testutil"
)

func writeConfigFile(t *testing.T, dir, content string) string {
	t.Helper()
	cfgDir := filepath.Join(dir, ".revyl")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return cfgPath
}

func newValidateServer(t *testing.T, orgID string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"` + orgID + `","email":"zak@example.com","concurrency_limit":1}`))
	}))
}

func writeCredentialsFile(t *testing.T, homeDir, content string) string {
	t.Helper()
	revylDir := filepath.Join(homeDir, ".revyl")
	if err := os.MkdirAll(revylDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl) error = %v", err)
	}
	path := filepath.Join(revylDir, "credentials.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(credentials.json) error = %v", err)
	}
	return path
}

func initializeGuardGitWorktree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return root
}

func TestCheck_ConfigMissing(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", "http://127.0.0.1:1")

	result := Check(context.Background(), t.TempDir(), false)
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.ConfigExists {
		t.Fatal("ConfigExists = true, want false")
	}
	if result.Mismatch != nil {
		t.Fatalf("Mismatch = %+v, want nil", result.Mismatch)
	}
}

func TestCheck_ProjectOrgUnset(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", "http://127.0.0.1:1")

	dir := initializeGuardGitWorktree(t)
	writeConfigFile(t, dir, "project:\n  id: 11111111-1111-4111-8111-111111111111\n")

	result := Check(context.Background(), dir, false)
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.ConfigExists || !result.ConfigParsed {
		t.Fatalf("config state invalid: exists=%v parsed=%v", result.ConfigExists, result.ConfigParsed)
	}
	if result.ProjectOrgID != "" {
		t.Fatalf("ProjectOrgID = %q, want empty", result.ProjectOrgID)
	}
	if result.Mismatch != nil {
		t.Fatalf("Mismatch = %+v, want nil", result.Mismatch)
	}
}

func TestCheck_ConfigParseFailureDoesNotBlock(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", "http://127.0.0.1:1")

	dir := initializeGuardGitWorktree(t)
	writeConfigFile(t, dir, "project: [\n")

	result := Check(context.Background(), dir, false)
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.ConfigExists {
		t.Fatal("ConfigExists = false, want true")
	}
	if result.ConfigParsed {
		t.Fatal("ConfigParsed = true, want false")
	}
	if result.Mismatch != nil {
		t.Fatalf("Mismatch = %+v, want nil", result.Mismatch)
	}
}

func TestCheck_LegacyOrgBindingIsNotConsumed(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", "http://127.0.0.1:1")

	dir := initializeGuardGitWorktree(t)
	writeConfigFile(t, dir, "project:\n  name: demo\n  org_id: org-a\n")

	result := Check(context.Background(), dir, false)
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.ConfigExists || result.ConfigParsed {
		t.Fatalf("config state invalid: exists=%v parsed=%v", result.ConfigExists, result.ConfigParsed)
	}
	if result.Mismatch != nil {
		t.Fatalf("Mismatch = %+v, want nil", result.Mismatch)
	}
}

func TestCheck_CanonicalConfigDoesNotPerformAuthOrgLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected auth-org lookup: %s", r.URL.Path)
	}))
	defer srv.Close()

	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", srv.URL)

	dir := initializeGuardGitWorktree(t)
	writeConfigFile(t, dir, "project:\n  id: 11111111-1111-4111-8111-111111111111\n")

	result := Check(context.Background(), dir, false)
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Mismatch != nil {
		t.Fatalf("Mismatch = %+v, want nil", result.Mismatch)
	}
	if !result.ConfigExists || !result.ConfigParsed {
		t.Fatalf("config state invalid: exists=%v parsed=%v", result.ConfigExists, result.ConfigParsed)
	}
	if result.AuthOrgID != "" {
		t.Fatalf("AuthOrgID = %q, want empty", result.AuthOrgID)
	}
}

func TestResolveCreateOrgID_IgnoresLegacyProjectConfigAndUsesLiveAuth(t *testing.T) {
	homeDir := t.TempDir()
	testutil.SetHomeDir(t, homeDir)
	validateCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			t.Fatalf("unexpected validate call: %s", r.URL.Path)
		}
		validateCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)
	cfg := &config.ProjectConfig{
		Project: config.Project{OrgID: "org-config"},
	}

	got, err := ResolveCreateOrgID(context.Background(), client, cfg)
	if err != nil {
		t.Fatalf("ResolveCreateOrgID() error = %v", err)
	}
	if got != "org-live" {
		t.Fatalf("ResolveCreateOrgID() = %q, want %q", got, "org-live")
	}
	if validateCalls != 1 {
		t.Fatalf("validate calls = %d, want 1", validateCalls)
	}
}

func TestResolveCreateOrgID_PrefersLiveAuthOrgOverFileCredentials(t *testing.T) {
	homeDir := t.TempDir()
	testutil.SetHomeDir(t, homeDir)
	t.Setenv("REVYL_API_KEY", "env-token")
	writeCredentialsFile(t, homeDir, `{"api_key":"file-key","org_id":"org-file"}`)

	validateCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			t.Fatalf("unexpected validate call: %s", r.URL.Path)
		}
		validateCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)

	got, err := ResolveCreateOrgID(context.Background(), client, &config.ProjectConfig{})
	if err != nil {
		t.Fatalf("ResolveCreateOrgID() error = %v", err)
	}
	if got != "org-live" {
		t.Fatalf("ResolveCreateOrgID() = %q, want %q", got, "org-live")
	}
	if validateCalls != 1 {
		t.Fatalf("validate calls = %d, want 1", validateCalls)
	}
}

func TestResolveCreateOrgID_FallsBackToValidateAPIKey(t *testing.T) {
	homeDir := t.TempDir()
	testutil.SetHomeDir(t, homeDir)

	validateCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		validateCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)

	got, err := ResolveCreateOrgID(context.Background(), client, &config.ProjectConfig{})
	if err != nil {
		t.Fatalf("ResolveCreateOrgID() error = %v", err)
	}
	if got != "org-live" {
		t.Fatalf("ResolveCreateOrgID() = %q, want %q", got, "org-live")
	}
	if validateCalls != 1 {
		t.Fatalf("validate calls = %d, want 1", validateCalls)
	}
}

func TestResolveCreateOrgID_FallsBackToFileCredentialsWhenValidateFails(t *testing.T) {
	homeDir := t.TempDir()
	testutil.SetHomeDir(t, homeDir)
	writeCredentialsFile(t, homeDir, `{"api_key":"file-key","org_id":"org-file"}`)

	validateCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		validateCalls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid api key"}`))
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)

	got, err := ResolveCreateOrgID(context.Background(), client, &config.ProjectConfig{})
	if err != nil {
		t.Fatalf("ResolveCreateOrgID() error = %v", err)
	}
	if got != "org-file" {
		t.Fatalf("ResolveCreateOrgID() = %q, want %q", got, "org-file")
	}
	if validateCalls != 1 {
		t.Fatalf("validate calls = %d, want 1", validateCalls)
	}
}

func TestResolveCreateOrgID_ReturnsHelpfulErrorWhenValidateHasNoOrg(t *testing.T) {
	homeDir := t.TempDir()
	testutil.SetHomeDir(t, homeDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"","email":"test@example.com","concurrency_limit":1}`))
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)

	_, err := ResolveCreateOrgID(context.Background(), client, &config.ProjectConfig{})
	if err == nil {
		t.Fatal("ResolveCreateOrgID() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "could not resolve organization ID for test creation") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "revyl auth login") {
		t.Fatalf("expected remediation hint in error, got: %v", err)
	}
}
