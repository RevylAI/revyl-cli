// Package main provides tests for the helper functions.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// TestLooksLikeUUID tests the UUID-like structure detection function.
func TestLooksLikeUUID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid UUID lowercase",
			input:    "027b91de-4a21-4bca-acfe-32db2a628f51",
			expected: true,
		},
		{
			name:     "valid UUID uppercase",
			input:    "027B91DE-4A21-4BCA-ACFE-32DB2A628F51",
			expected: true,
		},
		{
			name:     "too short",
			input:    "027b91de-4a21-4bca-acfe",
			expected: false,
		},
		{
			name:     "too long",
			input:    "027b91de-4a21-4bca-acfe-32db2a628f51-extra",
			expected: false,
		},
		{
			name:     "missing dashes",
			input:    "027b91de4a214bcaacfe32db2a628f51",
			expected: false,
		},
		{
			name:     "wrong dash positions",
			input:    "027b91de4-a21-4bca-acfe-32db2a628f51",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "test name (not UUID)",
			input:    "login-flow",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := looksLikeUUID(tt.input)
			if result != tt.expected {
				t.Errorf("looksLikeUUID(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestValidateResourceName tests the name validation function.
func TestValidateResourceName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		kind      string
		wantError bool
	}{
		{name: "valid simple name", input: "login-flow", kind: "test", wantError: false},
		{name: "valid with underscores", input: "login_flow", kind: "test", wantError: false},
		{name: "valid with numbers", input: "test-123", kind: "test", wantError: false},
		{name: "valid all digits and letters", input: "abc123def", kind: "test", wantError: false},
		{name: "empty name", input: "", kind: "test", wantError: true},
		{name: "has spaces", input: "login flow", kind: "test", wantError: false},
		{name: "has path separator", input: "tests/login", kind: "test", wantError: true},
		{name: "has backslash", input: "tests\\login", kind: "test", wantError: true},
		{name: "ends with .yaml", input: "login-flow.yaml", kind: "test", wantError: true},
		{name: "ends with .yml", input: "login-flow.yml", kind: "test", wantError: true},
		{name: "ends with .json", input: "login-flow.json", kind: "test", wantError: true},
		{name: "uppercase letters", input: "Login-Flow", kind: "test", wantError: false},
		{name: "special chars", input: "login@flow", kind: "test", wantError: false},
		{name: "has parentheses", input: "login(v2)", kind: "test", wantError: false},
		{name: "has brackets", input: "test[ios]", kind: "test", wantError: false},
		{name: "reserved word run", input: "run", kind: "test", wantError: true},
		{name: "reserved word create", input: "create", kind: "test", wantError: true},
		{name: "reserved word delete", input: "delete", kind: "test", wantError: true},
		{name: "reserved word list", input: "list", kind: "test", wantError: true},
		{name: "reserved word rename", input: "rename", kind: "test", wantError: true},
		{name: "reserved word help", input: "help", kind: "test", wantError: true},
		{name: "workflow kind", input: "smoke-tests", kind: "workflow", wantError: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResourceName(tt.input, tt.kind)
			if (err != nil) != tt.wantError {
				t.Errorf("validateResourceName(%q, %q) error = %v, wantError %v", tt.input, tt.kind, err, tt.wantError)
			}
		})
	}
}

// TestValidateResourceNameMaxLength tests that names exceeding the max length are rejected.
func TestValidateResourceNameMaxLength(t *testing.T) {
	// Build a name exactly at the limit
	name := ""
	for i := 0; i < maxResourceNameLen; i++ {
		name += "a"
	}
	if err := validateResourceName(name, "test"); err != nil {
		t.Errorf("validateResourceName(128 chars) unexpected error: %v", err)
	}

	// One char over
	name += "a"
	if err := validateResourceName(name, "test"); err == nil {
		t.Error("validateResourceName(129 chars) expected error, got nil")
	}
}

func TestResolveTestID_PrioritizesAliasAndUUID(t *testing.T) {
	t.Run("local yaml alias", func(t *testing.T) {
		tmp := t.TempDir()
		testsDir := filepath.Join(tmp, ".revyl", "tests")
		if err := os.MkdirAll(testsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		lt := &config.LocalTest{
			Meta: config.TestMeta{RemoteID: "test-alias-001"},
			Test: config.TestDefinition{
				Metadata: config.TestMetadata{Name: "login-flow"},
			},
		}
		if err := config.SaveLocalTest(filepath.Join(testsDir, "login-flow.yaml"), lt); err != nil {
			t.Fatalf("SaveLocalTest: %v", err)
		}

		origWD, _ := os.Getwd()
		if err := os.Chdir(tmp); err != nil {
			t.Fatalf("Chdir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chdir(origWD) })

		client := api.NewClientWithBaseURL("test-key", "http://127.0.0.1:1")
		testID, testName, err := resolveTestID(context.Background(), "login-flow", nil, client)
		if err != nil {
			t.Fatalf("resolveTestID() error = %v", err)
		}
		if testID != "test-alias-001" {
			t.Fatalf("testID = %q, want test-alias-001", testID)
		}
		if testName != "login-flow" {
			t.Fatalf("testName = %q, want login-flow", testName)
		}
	})

	t.Run("uuid passthrough", func(t *testing.T) {
		client := api.NewClientWithBaseURL("test-key", "http://127.0.0.1:1")
		testUUID := "027b91de-4a21-4bca-acfe-32db2a628f51"

		testID, testName, err := resolveTestID(context.Background(), testUUID, nil, client)
		if err != nil {
			t.Fatalf("resolveTestID() error = %v", err)
		}
		if testID != testUUID {
			t.Fatalf("testID = %q, want %q", testID, testUUID)
		}
		if testName != "" {
			t.Fatalf("testName = %q, want empty string", testName)
		}
	})
}

func TestResolveTestID_SearchesAllRemotePages(t *testing.T) {
	ui.SetQuietMode(true)
	t.Cleanup(func() { ui.SetQuietMode(false) })

	var requestedOffsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_simple_tests" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		offset := r.URL.Query().Get("offset")
		requestedOffsets = append(requestedOffsets, offset)

		w.Header().Set("Content-Type", "application/json")
		switch offset {
		case "0":
			tests := make([]api.SimpleTest, 200)
			for i := range tests {
				tests[i] = api.SimpleTest{
					ID:       fmt.Sprintf("test-%03d", i),
					Name:     fmt.Sprintf("dummy-test-%03d", i),
					Platform: "ios",
				}
			}
			if err := json.NewEncoder(w).Encode(api.CLISimpleTestListResponse{
				Tests: tests,
				Count: 201,
			}); err != nil {
				t.Fatalf("encode first page: %v", err)
			}
		case "200":
			if err := json.NewEncoder(w).Encode(api.CLISimpleTestListResponse{
				Tests: []api.SimpleTest{
					{ID: "target-test-id", Name: "remote-target", Platform: "ios"},
				},
				Count: 201,
			}); err != nil {
				t.Fatalf("encode second page: %v", err)
			}
		default:
			t.Fatalf("unexpected offset: %s", offset)
		}
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)

	testID, testName, err := resolveTestID(context.Background(), "remote-target", nil, client)
	if err != nil {
		t.Fatalf("resolveTestID() error = %v", err)
	}
	if testID != "target-test-id" {
		t.Fatalf("testID = %q, want target-test-id", testID)
	}
	if testName != "remote-target" {
		t.Fatalf("testName = %q, want remote-target", testName)
	}

	wantOffsets := []string{"0", "200"}
	if !reflect.DeepEqual(requestedOffsets, wantOffsets) {
		t.Fatalf("requested offsets = %v, want %v", requestedOffsets, wantOffsets)
	}
}

func TestRunWorkflowRemoveTests_RemovesOnlyWorkflowMembership(t *testing.T) {
	ui.SetQuietMode(true)
	t.Cleanup(func() { ui.SetQuietMode(false) })

	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if err := config.WriteProjectConfig(filepath.Join(configDir, "config.yaml"), &config.ProjectConfig{}); err != nil {
		t.Fatalf("WriteProjectConfig() error = %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	t.Setenv("REVYL_API_KEY", "test-key")

	var requestedOffsets []string
	var updatedTestIDs []string
	deleteCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflows/get_with_last_status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"wf-uuid-001","name":"smoke-tests"}]}`))

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflows/get_workflow_info":
			if got := r.URL.Query().Get("workflow_id"); got != "wf-uuid-001" {
				t.Fatalf("workflow_id = %q, want wf-uuid-001", got)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(api.Workflow{
				ID:    "wf-uuid-001",
				Name:  "smoke-tests",
				Tests: []string{"test-keep", "test-remove"},
			}); err != nil {
				t.Fatalf("encode workflow: %v", err)
			}

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tests/get_simple_tests":
			offset := r.URL.Query().Get("offset")
			requestedOffsets = append(requestedOffsets, offset)

			w.Header().Set("Content-Type", "application/json")
			switch offset {
			case "0":
				tests := make([]api.SimpleTest, 200)
				for i := range tests {
					tests[i] = api.SimpleTest{
						ID:       fmt.Sprintf("dummy-%03d", i),
						Name:     fmt.Sprintf("dummy-test-%03d", i),
						Platform: "ios",
					}
				}
				if err := json.NewEncoder(w).Encode(api.CLISimpleTestListResponse{
					Tests: tests,
					Count: 201,
				}); err != nil {
					t.Fatalf("encode first page: %v", err)
				}
			case "200":
				if err := json.NewEncoder(w).Encode(api.CLISimpleTestListResponse{
					Tests: []api.SimpleTest{
						{ID: "test-remove", Name: "remote-target", Platform: "ios"},
					},
					Count: 201,
				}); err != nil {
					t.Fatalf("encode second page: %v", err)
				}
			default:
				t.Fatalf("unexpected offset: %s", offset)
			}

		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/workflows/update_tests/wf-uuid-001":
			if err := json.NewDecoder(r.Body).Decode(&updatedTestIDs); err != nil {
				t.Fatalf("decode update body: %v", err)
			}
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/tests/delete/test-remove":
			deleteCalls++
			t.Fatalf("unexpected delete request: %s %s", r.Method, r.URL.Path)

		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("dev", false, "dev mode")

	if err := runWorkflowRemoveTests(cmd, []string{"smoke-tests", "remote-target"}); err != nil {
		t.Fatalf("runWorkflowRemoveTests() error = %v", err)
	}

	if deleteCalls != 0 {
		t.Fatalf("deleteCalls = %d, want 0", deleteCalls)
	}

	wantTestIDs := []string{"test-keep"}
	if !reflect.DeepEqual(updatedTestIDs, wantTestIDs) {
		t.Fatalf("updated test IDs = %v, want %v", updatedTestIDs, wantTestIDs)
	}

	wantOffsets := []string{"0", "200", "0", "200"}
	if !reflect.DeepEqual(requestedOffsets, wantOffsets) {
		t.Fatalf("requested offsets = %v, want %v", requestedOffsets, wantOffsets)
	}
}
