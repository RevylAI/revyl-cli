package auth

import (
	"os"
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
