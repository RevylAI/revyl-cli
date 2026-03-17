//go:build e2e

// Package e2e provides end-to-end regression tests for the Revyl CLI.
//
// Tests run against a real backend (local auto-detect or staging) and exercise
// the CLI binary end-to-end. All resources are created with unique names and
// cleaned up via t.Cleanup to ensure idempotency.
package e2e

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	prodBackendURL     = "https://backend.revyl.ai"
	stagingBackendURL  = "https://backend.staging.revyl.ai"
	defaultBackendPort = "8000"
	portCheckTimeout   = 200 * time.Millisecond
)

var commonBackendPorts = []string{"8000", "8001", "8080"}

// Resolved at TestMain time, shared across all tests.
var (
	revylBin    string // path to the built CLI binary
	backendURL  string // resolved backend URL
	apiKey      string // resolved API key
	fakeOpenDir string // dir containing no-op "open" script to suppress browser launches
)

// CLIResult holds the output of a CLI command execution.
type CLIResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// resolveBackendURL determines which backend to target.
//
// Priority: (1) REVYL_BACKEND_URL env, (2) local auto-detect, (3) staging.
// Refuses production to prevent data pollution.
func resolveBackendURL() string {
	if envURL := os.Getenv("REVYL_BACKEND_URL"); envURL != "" {
		if envURL == prodBackendURL {
			fmt.Fprintf(os.Stderr, "FATAL: e2e tests refuse to run against production (%s)\n", prodBackendURL)
			os.Exit(1)
		}
		return strings.TrimRight(envURL, "/")
	}

	port := detectLocalBackendPort()
	if port != "" {
		return fmt.Sprintf("http://localhost:%s", port)
	}

	return stagingBackendURL
}

// detectLocalBackendPort probes localhost for a running backend.
func detectLocalBackendPort() string {
	configuredPort := readBackendPortFromEnv()

	if isPortOpen("localhost", configuredPort) {
		return configuredPort
	}
	for _, port := range commonBackendPorts {
		if port != configuredPort && isPortOpen("localhost", port) {
			return port
		}
	}
	return ""
}

// readBackendPortFromEnv reads PORT from cognisim_backend/.env.
func readBackendPortFromEnv() string {
	root := findMonorepoRoot()
	if root == "" {
		return defaultBackendPort
	}
	envPath := filepath.Join(root, "cognisim_backend", ".env")
	return readEnvVar(envPath, "PORT", defaultBackendPort)
}

// resolveAPIKey determines the API key for authentication.
//
// Priority: (1) REVYL_API_KEY env, (2) ~/.revyl/credentials.json, (3) cognisim_backend/.env.
func resolveAPIKey() string {
	if key := os.Getenv("REVYL_API_KEY"); key != "" {
		return key
	}

	if key := readCredentialsFile(); key != "" {
		return key
	}

	root := findMonorepoRoot()
	if root == "" {
		return ""
	}
	envPath := filepath.Join(root, "cognisim_backend", ".env")
	return readEnvVar(envPath, "REVYL_API_KEY", "")
}

// readCredentialsFile reads the API key from ~/.revyl/credentials.json.
func readCredentialsFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	credPath := filepath.Join(home, ".revyl", "credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		return ""
	}
	var creds struct {
		APIKey      string `json:"api_key"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	if creds.APIKey != "" {
		return creds.APIKey
	}
	return creds.AccessToken
}

// findMonorepoRoot walks up from cwd looking for cognisim_backend/.
func findMonorepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "cognisim_backend")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// readEnvVar reads a KEY=VALUE from a .env file, returning fallback if not found.
func readEnvVar(path, key, fallback string) string {
	file, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer file.Close()

	prefix := key + "="
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimPrefix(line, prefix)
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return fallback
}

func isPortOpen(host, port string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), portCheckTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// uniqueName generates a collision-free name like "e2e-test-a3f8b21c".
func uniqueName(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

// runCLI executes the built CLI binary with the given args.
// Backend URL and API key are injected via environment.
func runCLI(t *testing.T, args ...string) CLIResult {
	t.Helper()
	cmd := exec.Command(revylBin, args...)
	cmd.Env = append(os.Environ(),
		"REVYL_BACKEND_URL="+backendURL,
		"REVYL_API_KEY="+apiKey,
		"NO_COLOR=1",
		"BROWSER=echo",
		"DISPLAY=",
		"PATH="+fakeOpenDir+":"+os.Getenv("PATH"),
	)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run CLI: %v", err)
		}
	}

	return CLIResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}

// runCLIJSON executes the CLI with --json and unmarshals stdout into T.
func runCLIJSON[T any](t *testing.T, args ...string) T {
	t.Helper()
	result := runCLI(t, args...)
	if result.ExitCode != 0 {
		t.Fatalf("CLI exited %d\nstdout: %s\nstderr: %s", result.ExitCode, result.Stdout, result.Stderr)
	}

	var out T
	raw := extractJSON(result.Stdout)
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nraw: %s", err, result.Stdout)
	}
	return out
}

// runCLIExpectFail executes the CLI and asserts non-zero exit.
func runCLIExpectFail(t *testing.T, args ...string) CLIResult {
	t.Helper()
	result := runCLI(t, args...)
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit but got 0\nstdout: %s", result.Stdout)
	}
	return result
}

// extractJSON finds the first JSON object or array in a string.
// Handles CLI output that may have non-JSON prefix lines.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	for i, ch := range s {
		if ch == '{' || ch == '[' {
			return s[i:]
		}
	}
	return s
}

// uuidPattern matches a standard UUID (8-4-4-4-12 hex).
var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// extractUUID finds the first UUID in a string.
// Useful for parsing CLI text output like "Created test: name (id: uuid)".
func extractUUID(s string) string {
	if m := uuidPattern.FindString(s); m != "" {
		return m
	}
	return ""
}

// step runs a named sub-step within a scenario test, logging timing and result.
// The callback receives the sub-test's *testing.T so t.Skip/t.Fatal work correctly
// without breaking the parent test.
func step(t *testing.T, name string, fn func(st *testing.T)) {
	t.Helper()
	start := time.Now()
	t.Run(name, func(st *testing.T) {
		st.Helper()
		fn(st)
	})
	elapsed := time.Since(start)
	t.Logf("    --- %-50s (%.1fs)", name, elapsed.Seconds())
}

// createTestFixture creates a test via CLI and registers cleanup to delete it.
//
// Returns the test ID.
func createTestFixture(t *testing.T, name, platform string) string {
	t.Helper()

	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, name+".yaml")
	yamlContent := fmt.Sprintf(`test:
  metadata:
    name: %s
    platform: %s
  build:
    name: e2e-placeholder
  blocks:
    - type: instructions
      step_description: "E2E test step"
    - type: validation
      step_description: "E2E validation step"
`, name, platform)
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write YAML fixture: %v", err)
	}

	// test create copies the YAML into .revyl/tests/ and pushes to remote.
	// The cwd for the CLI process is the e2e/ directory, so files land in e2e/.revyl/tests/.
	result := runCLI(t, "test", "create", name, "--from-file", yamlPath, "--platform", platform, "--no-open")
	if result.ExitCode != 0 {
		t.Fatalf("failed to create test fixture %q: %s\n%s", name, result.Stdout, result.Stderr)
	}

	// Extract the test ID. Priorities:
	// 1. UUID in stdout/stderr text (e.g. "Created test: name (id: uuid)")
	// 2. _meta.remote_id in the synced YAML file
	// 3. JSON parse fallback
	combined := result.Stdout + result.Stderr
	testID := extractUUID(combined)

	if testID == "" {
		// Read the synced YAML for the _meta.remote_id
		syncedPath := filepath.Join(".", ".revyl", "tests", name+".yaml")
		if data, err := os.ReadFile(syncedPath); err == nil {
			testID = extractUUID(string(data))
		}
	}

	if testID == "" {
		var resp struct {
			TestID string `json:"test_id"`
			ID     string `json:"id"`
		}
		raw := extractJSON(result.Stdout)
		if err := json.Unmarshal([]byte(raw), &resp); err == nil {
			testID = resp.TestID
			if testID == "" {
				testID = resp.ID
			}
		}
	}
	if testID == "" {
		t.Fatalf("create returned no parseable test ID\n%s\n%s", result.Stdout, result.Stderr)
	}

	t.Cleanup(func() {
		_ = runCLI(t, "test", "delete", testID, "--force", "--json")
	})

	return testID
}

// createAppFixture creates an app via CLI and registers cleanup to delete it.
//
// Returns the app ID.
func createAppFixture(t *testing.T, name, platform string) string {
	t.Helper()
	result := runCLI(t, "app", "create", "--name", name, "--platform", platform, "--json")
	if result.ExitCode != 0 {
		t.Fatalf("failed to create app fixture %q: %s\n%s", name, result.Stdout, result.Stderr)
	}

	combined := result.Stdout + result.Stderr
	appID := extractUUID(combined)
	if appID == "" {
		var resp struct {
			ID    string `json:"id"`
			AppID string `json:"app_id"`
		}
		raw := extractJSON(result.Stdout)
		if err := json.Unmarshal([]byte(raw), &resp); err == nil {
			appID = resp.ID
			if appID == "" {
				appID = resp.AppID
			}
		}
	}
	if appID == "" {
		t.Fatalf("create app returned no parseable ID\n%s", combined)
	}

	t.Cleanup(func() {
		_ = runCLI(t, "app", "delete", appID, "--force", "--json")
	})

	return appID
}

// createWorkflowFixture creates a workflow via CLI and registers cleanup.
//
// Returns the workflow ID.
func createWorkflowFixture(t *testing.T, name string, testIDs []string) string {
	t.Helper()
	args := []string{"workflow", "create", "--name", name, "--no-open"}
	if len(testIDs) > 0 {
		args = append(args, "--tests", strings.Join(testIDs, ","))
	}
	args = append(args, "--json")

	result := runCLI(t, args...)
	if result.ExitCode != 0 {
		t.Fatalf("failed to create workflow fixture %q: %s\n%s", name, result.Stdout, result.Stderr)
	}

	combined := result.Stdout + result.Stderr
	wfID := extractUUID(combined)
	if wfID == "" {
		var resp struct {
			ID         string `json:"id"`
			WorkflowID string `json:"workflow_id"`
		}
		raw := extractJSON(result.Stdout)
		if err := json.Unmarshal([]byte(raw), &resp); err == nil {
			wfID = resp.ID
			if wfID == "" {
				wfID = resp.WorkflowID
			}
		}
	}
	if wfID == "" {
		t.Fatalf("create workflow returned no parseable ID\n%s", combined)
	}

	t.Cleanup(func() {
		_ = runCLI(t, "workflow", "delete", wfID, "--force", "--json")
	})

	return wfID
}

// writeYAMLFixture writes the comprehensive YAML fixture to a temp directory
// and returns the file path.
func writeYAMLFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".yaml")
	content, err := os.ReadFile(filepath.Join(testdataDir(), "comprehensive.yaml"))
	if err != nil {
		t.Fatalf("failed to read comprehensive.yaml fixture: %v", err)
	}
	// Replace the fixture name with the unique name
	replaced := strings.Replace(string(content), "e2e-comprehensive", name, 1)
	if err := os.WriteFile(path, []byte(replaced), 0644); err != nil {
		t.Fatalf("failed to write YAML fixture: %v", err)
	}
	return path
}

// testdataDir returns the path to the e2e/testdata directory.
func testdataDir() string {
	// When running tests, the working directory is the package directory (e2e/)
	return "testdata"
}
