package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

const webFailureMessage = "web_session_failed: Failed during app_start; app_failure=app_exited_before_ready; Your app exited before it was ready (exit code 1)."
const webFailureSentence = "Your app exited before it was ready (exit code 1)."

func webFailureBackend(t *testing.T, status, platform string, message *string) *DeviceSessionManager {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/execution/device-sessions/"+startIDSessionID {
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		detail := runtimeProjectedSessionDetail(startIDSessionID, status)
		detail["platform"] = platform
		detail["error_message"] = message
		detail["ended_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		if err := json.NewEncoder(w).Encode(detail); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(backend.Close)
	return NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
}

func TestReadRuntimeReadyReportsWebFailureReason(t *testing.T) {
	message := webFailureMessage
	for _, tc := range []struct {
		name, status string
		message      *string
		want         string
	}{
		{name: "failed with reason", status: "failed", message: &message, want: "web session failed before it was ready: " + webFailureSentence},
		{name: "cancelled with reason", status: "cancelled", message: &message, want: "web session ended before it was ready: " + webFailureSentence},
		{name: "failed without reason", status: "failed", want: "web session ended before a ready observation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := webFailureBackend(t, tc.status, "web", tc.message)
			session := &DeviceSession{Index: UnattachedSessionIndex, SessionID: startIDSessionID, Platform: "web"}
			_, _, err := manager.readRuntimeReadyLocked(context.Background(), session)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCheckSessionAliveReportsWebFailureReason(t *testing.T) {
	message := webFailureMessage
	for _, tc := range []struct {
		name    string
		message *string
		want    string
	}{
		{name: "with reason", message: &message, want: "web session failed: " + webFailureSentence},
		{name: "without reason", want: "web session has ended or is stopping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := webFailureBackend(t, "failed", "web", tc.message)
			alive, reason := manager.CheckSessionAlive(context.Background(), &DeviceSession{SessionID: startIDSessionID, Platform: "web"})
			if alive || reason != tc.want {
				t.Fatalf("alive=%t reason=%q, want dead %q", alive, reason, tc.want)
			}
		})
	}
}

func TestResolveSessionByIDReportsWebFailureReasonOnly(t *testing.T) {
	message := webFailureMessage
	manager := webFailureBackend(t, "failed", "web", &message)
	_, err := manager.ResolveSessionByID(context.Background(), startIDSessionID)
	if err == nil || !strings.Contains(err.Error(), "terminal state: failed") || !strings.HasSuffix(err.Error(), "because it failed: "+webFailureSentence) || strings.Count(err.Error(), webFailureSentence) != 1 {
		t.Fatalf("err = %v, want terminal state with web failure reason", err)
	}

	ios := webFailureBackend(t, "failed", "ios", &message)
	_, err = ios.ResolveSessionByID(context.Background(), startIDSessionID)
	if err == nil || strings.Contains(err.Error(), webFailureSentence) {
		t.Fatalf("non-web error must not carry the web reason: %v", err)
	}
}

func TestEndedSessionCarriesWebFailureReason(t *testing.T) {
	message := webFailureMessage
	platform := "web"
	detail := &api.DeviceSessionDetail{Status: "failed", Platform: &platform, ErrorMessage: &message}
	ended := endedSessionFromPrune(&DeviceSession{Index: 0, SessionID: startIDSessionID, Platform: "web"}, detail, time.Now())
	if ended.FailureReason != webFailureSentence {
		t.Fatalf("FailureReason = %q", ended.FailureReason)
	}
	if got := ended.endedPhrase(time.Now()); !strings.HasSuffix(got, " because it failed: "+webFailureSentence) {
		t.Fatalf("endedPhrase() = %q", got)
	}

	mobile := "ios"
	detail.Platform = &mobile
	ended = endedSessionFromPrune(&DeviceSession{SessionID: startIDSessionID, Platform: "ios"}, detail, time.Now())
	if ended.FailureReason != "" || !strings.HasSuffix(ended.endedPhrase(time.Now()), "because it failed on the device worker") {
		t.Fatalf("iOS wording changed: %+v", ended)
	}
}
