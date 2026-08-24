package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/analytics"
)

func TestTestValidateCommandUsesBackendValidation(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")

	var gotReq map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/yaml/validate-yaml" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_valid":true,"validation_type":"full_test","errors":0,"warnings":0,"messages":[]}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	path := filepath.Join(t.TempDir(), "valid.yaml")
	if err := os.WriteFile(path, []byte("test:\n  metadata:\n    name: valid\n    platform: ios\n  blocks: []\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := newLeafCommand("validate", testValidateCmd.RunE)
	if err := testValidateCmd.Args(cmd, []string{path}); err != nil {
		t.Fatalf("Args() error = %v", err)
	}
	output := captureStdoutAndStderr(t, func() {
		if err := testValidateCmd.RunE(cmd, []string{path}); err != nil {
			t.Fatalf("RunE() error = %v", err)
		}
	})
	if !strings.Contains(output, "Validated 1 YAML test file") {
		t.Fatalf("output = %q, want validation success", output)
	}
	if gotReq["validation_type"] != "full_test" {
		t.Fatalf("validation_type = %v, want full_test", gotReq["validation_type"])
	}
	if gotReq["yaml_content"] == "" {
		t.Fatal("yaml_content was empty")
	}
}

func TestTestValidateCommandFailsOnBackendDiagnostics(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/yaml/validate-yaml" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"is_valid": false,
			"validation_type": "full_test",
			"errors": 1,
			"warnings": 0,
			"messages": [{
				"severity": "error",
				"code": "INVALID_BLOCK_TYPE",
				"message": "Invalid block type 'random_step'",
				"field_path": "test.blocks[0].type",
				"line": 5,
				"suggestion": "Use a supported block type."
			}]
		}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("test:\n  metadata:\n    name: invalid\n  blocks:\n    - type: random_step\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := newLeafCommand("validate", testValidateCmd.RunE)
	err := testValidateCmd.RunE(cmd, []string{path})
	if err == nil {
		t.Fatal("RunE() error = nil, want validation failure")
	}
	var completed *analytics.CompletedError
	if !errors.As(err, &completed) {
		t.Fatalf("RunE() error = %T, want CompletedError", err)
	}
	completion := completed.Completion()
	if completion.Domain != "test_validation" || completion.DomainStatus != "invalid" || completion.ExitCode != 1 {
		t.Fatalf("completion = %#v, want invalid test validation completion", completion)
	}
	if got := completion.Properties["validation_file_count"]; got != 1 {
		t.Fatalf("validation_file_count = %v, want 1", got)
	}
	if got := completion.Properties["validation_invalid_files"]; got != 1 {
		t.Fatalf("validation_invalid_files = %v, want 1", got)
	}
	if got := completion.Properties["validation_error_count"]; got != 1 {
		t.Fatalf("validation_error_count = %v, want 1", got)
	}
}
