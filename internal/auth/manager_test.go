package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGetOrCreateClientInstanceIDPersistsAcrossReads(t *testing.T) {
	mgr := NewManagerWithDir(t.TempDir())

	firstID, err := mgr.GetOrCreateClientInstanceID()
	if err != nil {
		t.Fatalf("GetOrCreateClientInstanceID() error = %v, want nil", err)
	}
	if firstID == "" {
		t.Fatal("GetOrCreateClientInstanceID() returned empty ID")
	}

	secondID, err := mgr.GetOrCreateClientInstanceID()
	if err != nil {
		t.Fatalf("GetOrCreateClientInstanceID() second call error = %v, want nil", err)
	}
	if secondID != firstID {
		t.Fatalf("client instance ID = %q, want %q", secondID, firstID)
	}
}

func TestGetOrCreateClientInstanceIDSurvivesCredentialLogout(t *testing.T) {
	mgr := NewManagerWithDir(t.TempDir())

	initialID, err := mgr.GetOrCreateClientInstanceID()
	if err != nil {
		t.Fatalf("GetOrCreateClientInstanceID() error = %v, want nil", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	if err := mgr.SaveCredentials(&Credentials{
		AccessToken: "token",
		ExpiresAt:   &expiresAt,
	}); err != nil {
		t.Fatalf("SaveCredentials() error = %v, want nil", err)
	}
	if err := mgr.ClearCredentials(); err != nil {
		t.Fatalf("ClearCredentials() error = %v, want nil", err)
	}

	nextID, err := mgr.GetOrCreateClientInstanceID()
	if err != nil {
		t.Fatalf("GetOrCreateClientInstanceID() after ClearCredentials error = %v, want nil", err)
	}
	if nextID != initialID {
		t.Fatalf("client instance ID after logout = %q, want %q", nextID, initialID)
	}
}

// ---------------------------------------------------------------------------
// Auth precedence tests
// ---------------------------------------------------------------------------

func TestGetCredentials_EnvWinsWhenNoOverride(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "env-key-123")

	mgr := NewManagerWithDir(t.TempDir())
	_ = mgr.SaveCredentials(&Credentials{
		APIKey:     "file-key-456",
		AuthMethod: "api_key",
	})

	creds, err := mgr.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials() error = %v", err)
	}
	if creds.APIKey != "env-key-123" {
		t.Fatalf("APIKey = %q, want %q", creds.APIKey, "env-key-123")
	}
	if creds.AuthMethod != "env" {
		t.Fatalf("AuthMethod = %q, want %q", creds.AuthMethod, "env")
	}
}

func TestGetCredentials_FileWinsWhenOverrideSet(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "env-key-123")

	mgr := NewManagerWithDir(t.TempDir())
	_ = mgr.SaveCredentials(&Credentials{
		APIKey:            "browser-key-789",
		Email:             "user@example.com",
		AuthMethod:        "browser_api_key",
		LocalAuthOverride: true,
	})

	creds, err := mgr.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials() error = %v", err)
	}
	if creds.APIKey != "browser-key-789" {
		t.Fatalf("APIKey = %q, want %q", creds.APIKey, "browser-key-789")
	}
	if creds.AuthMethod != "browser_api_key" {
		t.Fatalf("AuthMethod = %q, want %q", creds.AuthMethod, "browser_api_key")
	}
	if creds.Email != "user@example.com" {
		t.Fatalf("Email = %q, want %q", creds.Email, "user@example.com")
	}
}

func TestGetCredentials_OverrideIgnoredWhenFileCredsExpired(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "env-key-123")

	mgr := NewManagerWithDir(t.TempDir())
	expired := time.Now().Add(-time.Hour)
	_ = mgr.SaveCredentials(&Credentials{
		AccessToken:       "expired-token",
		ExpiresAt:         &expired,
		AuthMethod:        "browser",
		LocalAuthOverride: true,
	})

	creds, err := mgr.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials() error = %v", err)
	}
	if creds.AuthMethod != "env" {
		t.Fatalf("AuthMethod = %q, want %q (expired file creds should fall back to env)", creds.AuthMethod, "env")
	}
	if creds.APIKey != "env-key-123" {
		t.Fatalf("APIKey = %q, want %q", creds.APIKey, "env-key-123")
	}
}

func TestGetCredentials_NoEnvUsesFileDirectly(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "")
	os.Unsetenv("REVYL_API_KEY")

	mgr := NewManagerWithDir(t.TempDir())
	_ = mgr.SaveCredentials(&Credentials{
		APIKey:     "file-key",
		AuthMethod: "api_key",
	})

	creds, err := mgr.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials() error = %v", err)
	}
	if creds.APIKey != "file-key" {
		t.Fatalf("APIKey = %q, want %q", creds.APIKey, "file-key")
	}
}

func TestResolveCredentialsPlaceholderFallsThroughToCloudRuntimeContext(t *testing.T) {
	t.Setenv("REVYL_API_KEY", unresolvedAPIKeyEnvironmentPlaceholder)
	manager := NewManagerWithDir(t.TempDir())
	if err := manager.SaveCloudRuntimeContext("persisted-cloud-key", true); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() error = %v", err)
	}

	resolution, err := manager.ResolveCredentials()
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if !resolution.HeadlessCloud {
		t.Fatal("ResolveCredentials() did not retain the persisted Cloud marker")
	}
	if resolution.Credentials == nil {
		t.Fatal("ResolveCredentials() returned no credentials")
	}
	if resolution.Credentials.APIKey != "persisted-cloud-key" {
		t.Fatalf("APIKey = %q, want persisted Cloud key", resolution.Credentials.APIKey)
	}
	if resolution.Credentials.AuthMethod != "api_key" {
		t.Fatalf("AuthMethod = %q, want api_key", resolution.Credentials.AuthMethod)
	}
}

func TestGetActiveToken_HonorsOverride(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "env-key")

	mgr := NewManagerWithDir(t.TempDir())
	_ = mgr.SaveCredentials(&Credentials{
		APIKey:            "browser-key",
		AuthMethod:        "browser_api_key",
		LocalAuthOverride: true,
	})

	token, err := mgr.GetActiveToken()
	if err != nil {
		t.Fatalf("GetActiveToken() error = %v", err)
	}
	if token != "browser-key" {
		t.Fatalf("token = %q, want %q", token, "browser-key")
	}
}

func TestSetLocalAuthOverride_PersistsFlag(t *testing.T) {
	mgr := NewManagerWithDir(t.TempDir())
	_ = mgr.SaveCredentials(&Credentials{
		APIKey:     "some-key",
		AuthMethod: "browser_api_key",
	})

	if err := mgr.SetLocalAuthOverride(); err != nil {
		t.Fatalf("SetLocalAuthOverride() error = %v", err)
	}

	creds, _ := mgr.GetFileCredentials()
	if !creds.LocalAuthOverride {
		t.Fatal("LocalAuthOverride = false, want true after SetLocalAuthOverride()")
	}
}

func TestClearLocalAuthOverride_RemovesFlag(t *testing.T) {
	mgr := NewManagerWithDir(t.TempDir())
	_ = mgr.SaveCredentials(&Credentials{
		APIKey:            "some-key",
		AuthMethod:        "browser_api_key",
		LocalAuthOverride: true,
	})

	if err := mgr.ClearLocalAuthOverride(); err != nil {
		t.Fatalf("ClearLocalAuthOverride() error = %v", err)
	}

	creds, _ := mgr.GetFileCredentials()
	if creds.LocalAuthOverride {
		t.Fatal("LocalAuthOverride = true, want false after ClearLocalAuthOverride()")
	}
}

func TestClearCredentials_ImplicitlyClearsOverride(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "env-key")

	mgr := NewManagerWithDir(t.TempDir())
	_ = mgr.SaveCredentials(&Credentials{
		APIKey:            "browser-key",
		AuthMethod:        "browser_api_key",
		LocalAuthOverride: true,
	})

	_ = mgr.ClearCredentials()

	creds, err := mgr.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials() error = %v", err)
	}
	if creds.AuthMethod != "env" {
		t.Fatalf("AuthMethod = %q, want %q after logout", creds.AuthMethod, "env")
	}
}

func TestSaveAPIKeyCredentials_ClearsOverride(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "env-key")

	mgr := NewManagerWithDir(t.TempDir())
	_ = mgr.SaveCredentials(&Credentials{
		APIKey:            "browser-key",
		AuthMethod:        "browser_api_key",
		LocalAuthOverride: true,
	})

	_ = mgr.SaveAPIKeyCredentials("new-api-key", "user@test.com", "org-1", "user-1")

	creds, _ := mgr.GetFileCredentials()
	if creds.LocalAuthOverride {
		t.Fatal("LocalAuthOverride should be false after SaveAPIKeyCredentials (new struct)")
	}
}

func TestSaveCredentialsAtomicallyRestrictsExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission contract")
	}
	configDirectory := t.TempDir()
	credentialsPath := filepath.Join(configDirectory, "credentials.json")
	if err := os.WriteFile(credentialsPath, []byte(`{"api_key":"old"}`), 0o644); err != nil {
		t.Fatalf("write permissive credential fixture: %v", err)
	}
	manager := NewManagerWithDir(configDirectory)

	if err := manager.SaveAPIKeyCredentials("replacement", "", "", ""); err != nil {
		t.Fatalf("SaveAPIKeyCredentials() error = %v", err)
	}
	info, err := os.Stat(credentialsPath)
	if err != nil {
		t.Fatalf("stat replaced credentials: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestSaveCredentialsRejectsSymlinkDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	configDirectory := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outsidePath, []byte("unchanged"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(configDirectory, "credentials.json")); err != nil {
		t.Fatalf("create credential symlink: %v", err)
	}
	manager := NewManagerWithDir(configDirectory)

	if err := manager.SaveAPIKeyCredentials("must-not-write", "", "", ""); err == nil {
		t.Fatal("SaveAPIKeyCredentials() accepted a symlink destination")
	}
	content, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("read symlink target: %v", err)
	}
	if string(content) != "unchanged" {
		t.Fatal("credential write followed the symlink destination")
	}
}

func TestSaveCredentialsRejectsSymlinkConfigDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	realConfigDirectory := t.TempDir()
	symlinkPath := filepath.Join(t.TempDir(), "revyl-config")
	if err := os.Symlink(realConfigDirectory, symlinkPath); err != nil {
		t.Fatalf("create config directory symlink: %v", err)
	}
	manager := NewManagerWithDir(symlinkPath)

	err := manager.SaveAPIKeyCredentials("must-not-write", "", "", "")
	if err == nil {
		t.Fatal("SaveAPIKeyCredentials() accepted a symlinked config directory")
	}
	if !strings.Contains(err.Error(), "config directory must not be a symlink") {
		t.Fatalf("SaveAPIKeyCredentials() error = %q, want actionable symlink error", err)
	}
	if _, statErr := os.Stat(filepath.Join(realConfigDirectory, "credentials.json")); !os.IsNotExist(statErr) {
		t.Fatalf("credentials target exists after rejected symlinked config directory: %v", statErr)
	}
}

func TestUserCredentialSavesClearCloudRuntimeContext(t *testing.T) {
	testCases := []struct {
		name          string
		expectedToken string
		save          func(*Manager) error
	}{
		{
			name:          "manual API key",
			expectedToken: "new-api-key",
			save: func(manager *Manager) error {
				return manager.SaveAPIKeyCredentials("new-api-key", "user@example.com", "org", "user")
			},
		},
		{
			name:          "browser access token",
			expectedToken: "new-browser-token",
			save: func(manager *Manager) error {
				return manager.SaveBrowserCredentials(
					&BrowserAuthResult{Token: "new-browser-token"},
					time.Hour,
				)
			},
		},
		{
			name:          "browser API key",
			expectedToken: "new-browser-api-key",
			save: func(manager *Manager) error {
				return manager.SaveBrowserAPIKeyCredentials(
					&BrowserAuthResult{Token: "new-browser-api-key"},
					"key-id",
				)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("REVYL_API_KEY", "")
			configDirectory := t.TempDir()
			manager := NewManagerWithDir(configDirectory)
			if err := manager.SaveCloudRuntimeContext("old-cloud-key", true); err != nil {
				t.Fatalf("SaveCloudRuntimeContext() error = %v", err)
			}

			if err := testCase.save(manager); err != nil {
				t.Fatalf("save user credentials: %v", err)
			}
			resolution, err := manager.ResolveCredentials()
			if err != nil {
				t.Fatalf("ResolveCredentials() error = %v", err)
			}
			if resolution.HeadlessCloud {
				t.Fatalf("resolution = %+v, want explicit user login outside Cloud context", resolution)
			}
			token, err := manager.GetActiveToken()
			if err != nil {
				t.Fatalf("GetActiveToken() error = %v", err)
			}
			if token != testCase.expectedToken {
				t.Fatalf("active token = %q, want %q", token, testCase.expectedToken)
			}
			if _, statErr := os.Stat(filepath.Join(configDirectory, cloudRuntimeContextFilename)); !os.IsNotExist(statErr) {
				t.Fatalf("Cloud runtime context exists after explicit login: %v", statErr)
			}
		})
	}
}

func TestUserCredentialSaveReportsCloudContextCleanupFailure(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "")
	configDirectory := t.TempDir()
	contextDirectory := filepath.Join(configDirectory, cloudRuntimeContextFilename)
	if err := os.Mkdir(contextDirectory, 0o700); err != nil {
		t.Fatalf("create blocking Cloud context directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDirectory, "entry"), []byte("blocking"), 0o600); err != nil {
		t.Fatalf("populate blocking Cloud context directory: %v", err)
	}
	manager := NewManagerWithDir(configDirectory)

	err := manager.SaveAPIKeyCredentials("new-api-key", "", "", "")
	if err == nil {
		t.Fatal("SaveAPIKeyCredentials() ignored Cloud-context cleanup failure")
	}
	if !strings.Contains(err.Error(), "credentials were saved but could not become active") {
		t.Fatalf("SaveAPIKeyCredentials() error = %q, want activation guidance", err)
	}
	fileCredentials, readErr := manager.GetFileCredentials()
	if readErr != nil {
		t.Fatalf("GetFileCredentials() error = %v", readErr)
	}
	if fileCredentials == nil || fileCredentials.APIKey != "new-api-key" {
		t.Fatalf("file credentials = %+v, want saved user credential", fileCredentials)
	}
}

func TestSaveCloudRuntimeContextClearsStaleKey(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "")
	manager := NewManagerWithDir(t.TempDir())
	if err := manager.SaveCloudRuntimeContext("stale-key", true); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() initial error = %v", err)
	}
	if err := manager.SaveCloudRuntimeContext("", false); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() clear error = %v", err)
	}

	credentials, err := manager.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials() error = %v", err)
	}
	if credentials != nil {
		t.Fatal("missing Runtime Secret retained a stale imported key")
	}
}

func TestClearCloudRuntimeContextRemovesImportedKey(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "")
	manager := NewManagerWithDir(t.TempDir())
	if err := manager.SaveCloudRuntimeContext("cloud-key", true); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() error = %v", err)
	}

	if err := manager.ClearCloudRuntimeContext(); err != nil {
		t.Fatalf("ClearCloudRuntimeContext() error = %v", err)
	}
	resolution, err := manager.ResolveCredentials()
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if resolution.Credentials != nil || resolution.HeadlessCloud {
		t.Fatalf("resolution after clear = %+v, want local unauthenticated state", resolution)
	}
}

func TestClearAuthenticationStatePreservesUserCredentialsWhenCloudCleanupFails(t *testing.T) {
	configDirectory := t.TempDir()
	manager := NewManagerWithDir(configDirectory)
	if err := manager.SaveCredentials(&Credentials{
		APIKey:     "user-key",
		AuthMethod: "api_key",
	}); err != nil {
		t.Fatalf("SaveCredentials() error = %v", err)
	}
	contextDirectory := filepath.Join(configDirectory, cloudRuntimeContextFilename)
	if err := os.Mkdir(contextDirectory, 0o700); err != nil {
		t.Fatalf("create blocking Cloud context directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDirectory, "entry"), []byte("blocking"), 0o600); err != nil {
		t.Fatalf("populate blocking Cloud context directory: %v", err)
	}

	if err := manager.ClearAuthenticationState(); err == nil {
		t.Fatal("ClearAuthenticationState() ignored Cloud-context cleanup failure")
	}
	fileCredentials, err := manager.GetFileCredentials()
	if err != nil {
		t.Fatalf("GetFileCredentials() error = %v", err)
	}
	if fileCredentials == nil || fileCredentials.APIKey != "user-key" {
		t.Fatalf("file credentials = %+v, want untouched user credentials", fileCredentials)
	}
}

func TestClearAuthenticationStateClearsCloudAndUserCredentials(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "")
	configDirectory := t.TempDir()
	manager := NewManagerWithDir(configDirectory)
	if err := manager.SaveCredentials(&Credentials{
		APIKey:     "user-key",
		AuthMethod: "api_key",
	}); err != nil {
		t.Fatalf("SaveCredentials() error = %v", err)
	}
	if err := manager.SaveCloudRuntimeContext("cloud-key", true); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() error = %v", err)
	}

	if err := manager.ClearAuthenticationState(); err != nil {
		t.Fatalf("ClearAuthenticationState() error = %v", err)
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
}

func TestResolveCredentialsEnvironmentWinsMalformedCloudContext(t *testing.T) {
	configDirectory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(configDirectory, cloudRuntimeContextFilename),
		[]byte("not-json"),
		0o600,
	); err != nil {
		t.Fatalf("write malformed Cloud context: %v", err)
	}
	t.Setenv("REVYL_API_KEY", "environment-key")
	manager := NewManagerWithDir(configDirectory)

	resolution, err := manager.ResolveCredentials()
	if err != nil {
		t.Fatalf("ResolveCredentials() error = %v", err)
	}
	if resolution.Credentials == nil ||
		resolution.Credentials.APIKey != "environment-key" ||
		!resolution.HeadlessCloud {
		t.Fatalf("resolution = %+v, want environment auth in headless Cloud", resolution)
	}
}
