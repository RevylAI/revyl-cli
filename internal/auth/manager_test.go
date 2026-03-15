package auth

import (
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
