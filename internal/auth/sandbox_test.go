package auth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

func TestHeadlessAuthenticationThroughProxy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SSL_CERT_FILE configures the Linux system trust store")
	}
	if os.Getenv("REVYL_TEST_SUBPROCESS") == t.Name() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		auth := NewDeviceAuth(DeviceAuthConfig{
			BackendURL:       "https://sandbox.invalid",
			ClientInstanceID: "fixture-client",
		})
		auth.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
		authorization, err := auth.CreateAuthorization(ctx)
		if err != nil {
			t.Fatal(err)
		}
		credential, err := auth.WaitForApproval(ctx, authorization)
		if err != nil {
			t.Fatal(err)
		}
		client := api.NewClientWithBaseURL(credential.Token, "https://sandbox.invalid")
		if _, err := client.ValidateAPIKey(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	device := &deviceServer{pollStates: []string{"pending", "approved"}}
	upstream, certificatePath := testutil.StartTrustedTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credential leaked to the backend")
		}
		if r.URL.Path == "/api/v1/entity/users/get_user_uuid" {
			if r.Header.Get("Authorization") != "Bearer minted-token" {
				t.Error("API request did not use the approved credential")
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"user_id":"fixture-user","org_id":"fixture-org"}`)
			return
		}
		device.handler().ServeHTTP(w, r)
	}))
	proxy, requests := testutil.NewConnectProxy(t, upstream.URL)
	testutil.RunInSubprocess(t, map[string]string{
		"HTTPS_PROXY":   strings.Replace(proxy.URL, "http://", "http://fixture:fixture@", 1),
		"SSL_CERT_FILE": certificatePath,
	})
	if requests.Load() == 0 {
		t.Fatal("authentication did not use the proxy")
	}
	if device.pollCount != 2 {
		t.Fatalf("approval polls = %d, want 2", device.pollCount)
	}
}

func TestPrivateStateFailureExplainsWritableDirectory(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "blocked-config")
	if err := os.WriteFile(configPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := NewManagerWithDir(configPath).SaveCloudRuntimeContext("fixture-private-key", true)
	if err == nil || !strings.Contains(err.Error(), "writable, owner-only directory") {
		t.Fatalf("expected actionable storage error, got %v", err)
	}
	if strings.Contains(err.Error(), "fixture-private-key") {
		t.Fatal("storage error disclosed the credential")
	}
}
