package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// deviceServer records what the CLI sent and replays scripted poll responses.
type deviceServer struct {
	pollStates   []string
	pollCount    int
	createBody   map[string]string
	redeemBodies []map[string]string
	pollStatus   int
}

func (s *deviceServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(deviceAuthorizationsPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&s.createBody)
		writeJSON(w, http.StatusOK, map[string]any{
			"device_code":               "device-code-secret",
			"user_code":                 "ABCD-2345",
			"verification_uri":          "https://app.revyl.ai/cli/device",
			"verification_uri_complete": "https://app.revyl.ai/cli/device?code=ABCD-2345",
			"expires_in":                600,
			// Zero keeps the tests fast; the CLI treats it as "use the default",
			// so the interval is overridden on the handler side below.
			"interval": 0,
		})
	})
	mux.HandleFunc(deviceCredentialsPath, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.redeemBodies = append(s.redeemBodies, body)

		if s.pollStatus != 0 {
			writeJSON(w, s.pollStatus, map[string]any{"detail": "gone"})
			return
		}

		state := s.pollStates[min(s.pollCount, len(s.pollStates)-1)]
		s.pollCount++
		if state != "approved" {
			writeJSON(w, http.StatusOK, map[string]any{"state": state})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"state":         "approved",
			"api_key_token": "minted-token",
			"api_key_id":    "minted-key",
			"email":         "dev@revyl.ai",
			"org_id":        "org-1",
			"user_id":       "user-1",
		})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// newTestDeviceAuth wires a handler to a DeviceAuth that polls without waiting.
func newTestDeviceAuth(t *testing.T, server *deviceServer) (*DeviceAuth, func()) {
	t.Helper()
	httpServer := httptest.NewServer(server.handler())
	auth := NewDeviceAuth(DeviceAuthConfig{
		BackendURL:       httpServer.URL,
		ClientInstanceID: "client-1",
		DeviceLabel:      "Work Mac",
	})
	// Drive the polling loop without spending the real interval, while keeping
	// cancellation observable so the cancellation test stays honest.
	auth.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return auth, httpServer.Close
}

// fastAuthorization keeps polling immediate so tests do not sleep.
func fastAuthorization(deviceCode string) *DeviceAuthorization {
	return &DeviceAuthorization{
		DeviceCode: deviceCode,
		UserCode:   "ABCD-2345",
		ExpiresIn:  600,
		Interval:   0,
	}
}

func TestCreateAuthorizationSendsDeviceIdentity(t *testing.T) {
	server := &deviceServer{}
	auth, done := newTestDeviceAuth(t, server)
	defer done()

	authorization, err := auth.CreateAuthorization(context.Background())
	if err != nil {
		t.Fatalf("CreateAuthorization: %v", err)
	}

	if authorization.UserCode != "ABCD-2345" {
		t.Errorf("user code = %q, want ABCD-2345", authorization.UserCode)
	}
	if !strings.Contains(authorization.VerificationURIComplete, "code=ABCD-2345") {
		t.Errorf("complete URI %q should embed the user code", authorization.VerificationURIComplete)
	}
	if server.createBody["client_instance_id"] != "client-1" {
		t.Errorf("client_instance_id = %q, want client-1", server.createBody["client_instance_id"])
	}
	if server.createBody["device_label"] != "Work Mac" {
		t.Errorf("device_label = %q, want Work Mac", server.createBody["device_label"])
	}
}

func TestWaitForApprovalReturnsTheMintedCredential(t *testing.T) {
	server := &deviceServer{pollStates: []string{"pending", "pending", "approved"}}
	auth, done := newTestDeviceAuth(t, server)
	defer done()

	result, err := auth.WaitForApproval(context.Background(), fastAuthorization("device-code-secret"))
	if err != nil {
		t.Fatalf("WaitForApproval: %v", err)
	}

	if result.Token != "minted-token" || result.APIKeyID != "minted-key" {
		t.Errorf("credential = %+v, want the minted token and key id", result)
	}
	if result.AuthMethod != "api_key" {
		t.Errorf("auth method = %q, want api_key so it is stored as a persistent key", result.AuthMethod)
	}
	if server.pollCount != 3 {
		t.Errorf("polled %d times, want 3", server.pollCount)
	}
}

func TestTheDeviceCodeTravelsOnlyInTheRequestBody(t *testing.T) {
	server := &deviceServer{pollStates: []string{"approved"}}
	auth, done := newTestDeviceAuth(t, server)
	defer done()

	if _, err := auth.WaitForApproval(context.Background(), fastAuthorization("device-code-secret")); err != nil {
		t.Fatalf("WaitForApproval: %v", err)
	}

	if len(server.redeemBodies) != 1 {
		t.Fatalf("recorded %d redeem bodies, want 1", len(server.redeemBodies))
	}
	if server.redeemBodies[0]["device_code"] != "device-code-secret" {
		t.Error("the device code should be sent in the request body")
	}
}

func TestWaitForApprovalReportsDenial(t *testing.T) {
	server := &deviceServer{pollStates: []string{"denied"}}
	auth, done := newTestDeviceAuth(t, server)
	defer done()

	_, err := auth.WaitForApproval(context.Background(), fastAuthorization("device-code-secret"))

	if !errors.Is(err, ErrDeviceAuthorizationDenied) {
		t.Errorf("error = %v, want ErrDeviceAuthorizationDenied", err)
	}
}

func TestWaitForApprovalTreatsAnUnknownRequestAsExpired(t *testing.T) {
	server := &deviceServer{pollStatus: http.StatusNotFound}
	auth, done := newTestDeviceAuth(t, server)
	defer done()

	_, err := auth.WaitForApproval(context.Background(), fastAuthorization("device-code-secret"))

	if !errors.Is(err, ErrDeviceAuthorizationExpired) {
		t.Errorf("error = %v, want ErrDeviceAuthorizationExpired", err)
	}
}

func TestWaitForApprovalStopsWhenTheRequestHasExpired(t *testing.T) {
	server := &deviceServer{pollStates: []string{"pending"}}
	auth, done := newTestDeviceAuth(t, server)
	defer done()

	expired := &DeviceAuthorization{DeviceCode: "device-code-secret", ExpiresIn: 0}
	_, err := auth.WaitForApproval(context.Background(), expired)

	if !errors.Is(err, ErrDeviceAuthorizationExpired) {
		t.Errorf("error = %v, want ErrDeviceAuthorizationExpired", err)
	}
	if server.pollCount != 0 {
		t.Errorf("polled %d times after expiry, want 0", server.pollCount)
	}
}

func TestWaitForApprovalStopsOnCancellation(t *testing.T) {
	server := &deviceServer{pollStates: []string{"pending"}}
	auth, done := newTestDeviceAuth(t, server)
	defer done()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := auth.WaitForApproval(ctx, fastAuthorization("device-code-secret"))

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestPollingBacksOffWhilePendingButStaysBounded(t *testing.T) {
	interval := 5 * time.Second
	for i := 0; i < 20; i++ {
		next := nextPollInterval(interval)
		if next < interval {
			t.Fatalf("interval shrank from %v to %v", interval, next)
		}
		interval = next
	}

	if interval != maxDevicePollInterval {
		t.Errorf("backoff settled at %v, want the %v cap", interval, maxDevicePollInterval)
	}
}
