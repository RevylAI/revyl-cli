package api

import "testing"

func TestDeviceSessionDetailFailureReason(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name    string
		message *string
		want    string
	}{
		{name: "nil", message: nil, want: ""},
		{name: "blank", message: str("   "), want: ""},
		{name: "contract", message: str("web_session_failed: Failed during app_start; app_failure=app_exited_before_ready; Your app exited before it was ready (exit code 1)."), want: "Your app exited before it was ready (exit code 1)."},
		{name: "execution", message: str("web_session_failed: Failed during execution; app_failure=app_exited; Your app exited during the session (killed by SIGKILL)."), want: "Your app exited during the session (killed by SIGKILL)."},
		{name: "unknown code", message: str("web_session_failed: Failed during x; app_failure=brand_new_code; Something new happened."), want: "Something new happened."},
		{name: "sentence with semicolons", message: str("web_session_failed: Failed during x; app_failure=app_exited; Exited; see logs."), want: "Exited; see logs."},
		{name: "no match", message: str("  Session idle timeout  "), want: "Session idle timeout"},
		{name: "uppercase code is not the contract", message: str("a; app_failure=BAD; text"), want: "a; app_failure=BAD; text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (DeviceSessionDetail{ErrorMessage: tc.message}).FailureReason(); got != tc.want {
				t.Fatalf("FailureReason() = %q, want %q", got, tc.want)
			}
		})
	}
}
