package orgguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	srv := newValidateServer(t, "org-a")
	defer srv.Close()

	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", srv.URL)

	dir := t.TempDir()
	writeConfigFile(t, dir, "project:\n  name: demo\n")

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
	srv := newValidateServer(t, "org-a")
	defer srv.Close()

	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", srv.URL)

	dir := t.TempDir()
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

func TestCheck_MismatchDetected(t *testing.T) {
	srv := newValidateServer(t, "org-b")
	defer srv.Close()

	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", srv.URL)

	dir := t.TempDir()
	cfgPath := writeConfigFile(t, dir, "project:\n  name: demo\n  org_id: org-a\n")

	result := Check(context.Background(), dir, false)
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Mismatch == nil {
		t.Fatal("Mismatch = nil, want mismatch")
	}
	if result.Mismatch.ProjectOrgID != "org-a" {
		t.Fatalf("ProjectOrgID = %q, want org-a", result.Mismatch.ProjectOrgID)
	}
	if result.Mismatch.AuthOrgID != "org-b" {
		t.Fatalf("AuthOrgID = %q, want org-b", result.Mismatch.AuthOrgID)
	}
	if result.Mismatch.ConfigPath != cfgPath {
		t.Fatalf("ConfigPath = %q, want %q", result.Mismatch.ConfigPath, cfgPath)
	}
	if !strings.Contains(result.Mismatch.UserMessage(), "Project is bound to") {
		t.Fatalf("unexpected message: %s", result.Mismatch.UserMessage())
	}
}

func TestCheck_Match(t *testing.T) {
	srv := newValidateServer(t, "org-a")
	defer srv.Close()

	t.Setenv("REVYL_API_KEY", "dummy")
	t.Setenv("REVYL_BACKEND_URL", srv.URL)

	dir := t.TempDir()
	writeConfigFile(t, dir, "project:\n  name: demo\n  org_id: org-a\n")

	result := Check(context.Background(), dir, false)
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Mismatch != nil {
		t.Fatalf("Mismatch = %+v, want nil", result.Mismatch)
	}
	if result.AuthOrgID != "org-a" {
		t.Fatalf("AuthOrgID = %q, want org-a", result.AuthOrgID)
	}
}
