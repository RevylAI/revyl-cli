package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/revyl/cli/internal/auth"
)

// TestPersistCloudEnvironmentCommand verifies the hidden command emits structured secret-free output.
func TestPersistCloudEnvironmentCommand(t *testing.T) {
	const apiKey = "command-output-secret-sentinel"
	homeDirectory := t.TempDir()
	t.Setenv("HOME", homeDirectory)
	t.Setenv("USERPROFILE", homeDirectory)
	t.Setenv("APPDATA", filepath.Join(homeDirectory, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(homeDirectory, "AppData", "Local"))
	t.Setenv(headlessCloudEnvironmentSignal, "1")
	t.Setenv("REVYL_API_KEY", apiKey)
	if !authPersistCloudEnvCmd.Hidden {
		t.Fatal("persist-cloud-env must remain hidden")
	}
	if err := authPersistCloudEnvCmd.Args(authPersistCloudEnvCmd, []string{"unexpected"}); err == nil {
		t.Fatal("persist-cloud-env accepted positional arguments")
	}

	if err := rootCmd.PersistentFlags().Set("json", "true"); err != nil {
		t.Fatalf("set JSON output: %v", err)
	}
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("json", "false")
	})
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create output pipe: %v", err)
	}
	originalStdout := os.Stdout
	os.Stdout = writer
	runErr := authPersistCloudEnvCmd.RunE(authPersistCloudEnvCmd, nil)
	_ = writer.Close()
	os.Stdout = originalStdout
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if runErr != nil {
		t.Fatalf("persist-cloud-env command error = %v", runErr)
	}
	if readErr != nil {
		t.Fatalf("read command output: %v", readErr)
	}
	if strings.Contains(string(output), apiKey) {
		t.Fatal("persist-cloud-env output exposed the API key")
	}

	var result authPersistCloudEnvironmentOutput
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode command output: %v", err)
	}
	if !result.CloudContextPersisted || !result.CredentialPersisted {
		t.Fatalf("command result = %+v, want persisted Cloud credential", result)
	}
}

// TestPersistCloudEnvironmentCommandProcess verifies the real command boundary with an isolated home.
func TestPersistCloudEnvironmentCommandProcess(t *testing.T) {
	const helperProcessVariable = "REVYL_AUTH_CLOUD_HELPER_PROCESS"
	const apiKey = "command-process-secret-sentinel"
	if os.Getenv(helperProcessVariable) == "1" {
		rootCmd.SetArgs([]string{"--json", "auth", "persist-cloud-env"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("execute persist-cloud-env: %v", err)
		}
		return
	}

	homeDirectory := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestPersistCloudEnvironmentCommandProcess$")
	command.Env = environmentWithoutVariables(
		helperProcessVariable,
		"HOME",
		"USERPROFILE",
		"APPDATA",
		"LOCALAPPDATA",
		headlessCloudEnvironmentSignal,
		"REVYL_API_KEY",
	)
	command.Env = append(
		command.Env,
		helperProcessVariable+"=1",
		"HOME="+homeDirectory,
		"USERPROFILE="+homeDirectory,
		"APPDATA="+filepath.Join(homeDirectory, "AppData", "Roaming"),
		"LOCALAPPDATA="+filepath.Join(homeDirectory, "AppData", "Local"),
		headlessCloudEnvironmentSignal+"=1",
		"REVYL_API_KEY="+apiKey,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("persist-cloud-env process error = %v\n%s", err, output)
	}
	if strings.Contains(string(output), apiKey) {
		t.Fatal("persist-cloud-env process output exposed the API key")
	}

	contextPath := filepath.Join(homeDirectory, ".revyl", "cloud-runtime.json")
	info, err := os.Stat(contextPath)
	if err != nil {
		t.Fatalf("stat persisted Cloud context: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("Cloud context permissions = %o, want 600", info.Mode().Perm())
	}
}

// environmentWithoutVariables returns the process environment without selected names.
//
// Parameters:
//   - variableNames: Exact environment variable names to remove.
//
// Returns:
//   - []string: Environment entries that do not use a selected name.
func environmentWithoutVariables(variableNames ...string) []string {
	environment := os.Environ()
	filteredEnvironment := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		removeEntry := false
		for _, variableName := range variableNames {
			if name == variableName {
				removeEntry = true
				break
			}
		}
		if !removeEntry {
			filteredEnvironment = append(filteredEnvironment, entry)
		}
	}
	return filteredEnvironment
}

// TestPersistCloudAuthenticationContext verifies Cloud credentials move through the protected store without arguments.
func TestPersistCloudAuthenticationContext(t *testing.T) {
	const apiKey = " cloud-runtime-secret-sentinel "
	credentialDirectory := filepath.Join(t.TempDir(), ".revyl")
	manager := auth.NewManagerWithDir(credentialDirectory)
	if err := manager.SaveAPIKeyCredentials("existing-user-key", "user@example.com", "org", "user"); err != nil {
		t.Fatalf("SaveAPIKeyCredentials() error = %v", err)
	}
	t.Setenv(headlessCloudEnvironmentSignal, "1")
	t.Setenv("REVYL_API_KEY", apiKey)

	result, err := persistCloudAuthenticationContext(manager)
	if err != nil {
		t.Fatalf("persistCloudAuthenticationContext() error = %v", err)
	}
	if !result.CloudContextPersisted || !result.CredentialPersisted || result.Source != "REVYL_API_KEY" {
		t.Fatalf("persistence result = %+v, want configured Cloud credential", result)
	}

	contextPath := filepath.Join(credentialDirectory, "cloud-runtime.json")
	info, err := os.Stat(contextPath)
	if err != nil {
		t.Fatalf("stat Cloud context: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("Cloud context permissions = %o, want 600", info.Mode().Perm())
	}
	userCredentials, err := manager.GetFileCredentials()
	if err != nil {
		t.Fatalf("GetFileCredentials() error = %v", err)
	}
	if userCredentials == nil || userCredentials.Email != "user@example.com" {
		t.Fatal("Cloud persistence replaced normal user credentials")
	}

	t.Setenv("REVYL_API_KEY", "")
	credentials, err := manager.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials() error = %v", err)
	}
	if credentials == nil || credentials.APIKey != apiKey || credentials.AuthMethod != "api_key" {
		t.Fatal("GetCredentials() did not return the exact Cloud Runtime Secret")
	}
}

// TestPersistCloudAuthenticationContextWithoutKey verifies missing secrets retain Cloud remediation context.
func TestPersistCloudAuthenticationContextWithoutKey(t *testing.T) {
	credentialDirectory := filepath.Join(t.TempDir(), ".revyl")
	manager := auth.NewManagerWithDir(credentialDirectory)
	t.Setenv(headlessCloudEnvironmentSignal, "1")
	t.Setenv("REVYL_API_KEY", "")
	if err := os.Unsetenv("REVYL_API_KEY"); err != nil {
		t.Fatalf("Unsetenv() error = %v", err)
	}

	result, err := persistCloudAuthenticationContext(manager)
	if err != nil {
		t.Fatalf("persistCloudAuthenticationContext() error = %v", err)
	}
	if !result.CloudContextPersisted || result.CredentialPersisted || result.Source != "" {
		t.Fatalf("persistence result = %+v, want Cloud context without credential", result)
	}
	credentials, err := manager.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials() error = %v", err)
	}
	if credentials != nil {
		t.Fatal("missing Runtime Secret unexpectedly produced credentials")
	}
	resolution, err := manager.ResolveCredentials()
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if !resolution.HeadlessCloud {
		t.Fatal("missing Runtime Secret did not retain Cloud context")
	}
}

// TestPersistCloudAuthenticationContextRejectsPlaceholder verifies unresolved host syntax is never stored as a key.
func TestPersistCloudAuthenticationContextRejectsPlaceholder(t *testing.T) {
	manager := auth.NewManagerWithDir(t.TempDir())
	t.Setenv(headlessCloudEnvironmentSignal, "1")
	t.Setenv("REVYL_API_KEY", "${env:REVYL_API_KEY}")

	result, err := persistCloudAuthenticationContext(manager)
	if err != nil {
		t.Fatalf("persistCloudAuthenticationContext() error = %v", err)
	}
	if result.CredentialPersisted || result.Source != "" {
		t.Fatalf("placeholder result = %+v, want no persisted credential", result)
	}
	t.Setenv("REVYL_API_KEY", "")
	resolution, err := manager.ResolveCredentials()
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if resolution.Credentials != nil || !resolution.HeadlessCloud {
		t.Fatalf("resolution = %+v, want headless Cloud without credentials", resolution)
	}
}

func TestAuthLogoutPreservesUserCredentialsWhenCloudCleanupFails(t *testing.T) {
	homeDirectory := t.TempDir()
	t.Setenv("HOME", homeDirectory)
	t.Setenv("USERPROFILE", homeDirectory)
	t.Setenv("APPDATA", filepath.Join(homeDirectory, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(homeDirectory, "AppData", "Local"))
	t.Setenv("REVYL_API_KEY", "")
	var revokeRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		revokeRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	manager := auth.NewManager()
	if err := manager.SaveCredentials(&auth.Credentials{
		APIKey:     "user-key",
		AuthMethod: "browser_api_key",
		APIKeyID:   "user-key-id",
	}); err != nil {
		t.Fatalf("SaveCredentials() error = %v", err)
	}
	contextDirectory := filepath.Join(homeDirectory, ".revyl", "cloud-runtime.json")
	if err := os.Mkdir(contextDirectory, 0o700); err != nil {
		t.Fatalf("create blocking Cloud context directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDirectory, "entry"), []byte("blocking"), 0o600); err != nil {
		t.Fatalf("populate blocking Cloud context directory: %v", err)
	}

	if err := authLogoutCmd.RunE(authLogoutCmd, nil); err == nil {
		t.Fatal("auth logout ignored Cloud-context cleanup failure")
	}
	fileCredentials, err := manager.GetFileCredentials()
	if err != nil {
		t.Fatalf("GetFileCredentials() error = %v", err)
	}
	if fileCredentials == nil || fileCredentials.APIKey != "user-key" {
		t.Fatalf("file credentials = %+v, want untouched user credentials", fileCredentials)
	}
	if revokeRequests.Load() != 0 {
		t.Fatal("auth logout revoked the user key before Cloud-context cleanup succeeded")
	}
}

func TestAuthLogoutClearsCloudAndUserCredentials(t *testing.T) {
	homeDirectory := t.TempDir()
	t.Setenv("HOME", homeDirectory)
	t.Setenv("USERPROFILE", homeDirectory)
	t.Setenv("APPDATA", filepath.Join(homeDirectory, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(homeDirectory, "AppData", "Local"))
	t.Setenv("REVYL_API_KEY", "")
	credentialsPath := filepath.Join(homeDirectory, ".revyl", "credentials.json")
	contextPath := filepath.Join(homeDirectory, ".revyl", "cloud-runtime.json")
	var (
		revokeRequests          atomic.Int32
		credentialPresentAtCall atomic.Bool
		contextPresentAtCall    atomic.Bool
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		revokeRequests.Add(1)
		if _, err := os.Stat(credentialsPath); err == nil {
			credentialPresentAtCall.Store(true)
		}
		if _, err := os.Stat(contextPath); err == nil {
			contextPresentAtCall.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	manager := auth.NewManager()
	if err := manager.SaveCredentials(&auth.Credentials{
		APIKey:     "user-key",
		AuthMethod: "browser_api_key",
		APIKeyID:   "user-key-id",
	}); err != nil {
		t.Fatalf("SaveCredentials() error = %v", err)
	}
	if err := manager.SaveCloudRuntimeContext("cloud-key", true); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() error = %v", err)
	}

	if err := authLogoutCmd.RunE(authLogoutCmd, nil); err != nil {
		t.Fatalf("auth logout error = %v", err)
	}
	fileCredentials, err := manager.GetFileCredentials()
	if err != nil {
		t.Fatalf("GetFileCredentials() error = %v", err)
	}
	if fileCredentials != nil {
		t.Fatalf("file credentials = %+v, want nil", fileCredentials)
	}
	resolution, err := manager.ResolveCredentials()
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if resolution.Credentials != nil || resolution.HeadlessCloud {
		t.Fatalf("resolution after logout = %+v, want local unauthenticated state", resolution)
	}
	if revokeRequests.Load() != 1 {
		t.Fatalf("revoke request count = %d, want 1", revokeRequests.Load())
	}
	if credentialPresentAtCall.Load() || contextPresentAtCall.Load() {
		t.Fatal("auth logout revoked the user key before clearing both local stores")
	}
}

// TestPersistCloudAuthenticationContextRejectsLocalUse verifies the bridge is Cloud-only and secret-free.
func TestPersistCloudAuthenticationContextRejectsLocalUse(t *testing.T) {
	const apiKey = "rejected-cloud-secret-sentinel"
	credentialDirectory := filepath.Join(t.TempDir(), ".revyl")
	manager := auth.NewManagerWithDir(credentialDirectory)
	t.Setenv(headlessCloudEnvironmentSignal, "")
	t.Setenv("REVYL_API_KEY", apiKey)

	_, err := persistCloudAuthenticationContext(manager)
	if err == nil {
		t.Fatal("persistCloudAuthenticationContext() succeeded, want validation error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatal("validation error exposed the API key")
	}
	if _, statErr := os.Stat(filepath.Join(credentialDirectory, "cloud-runtime.json")); !os.IsNotExist(statErr) {
		t.Fatalf("Cloud context exists after rejected persistence: %v", statErr)
	}
}
