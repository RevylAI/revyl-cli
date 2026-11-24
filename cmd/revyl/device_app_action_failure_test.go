package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMobileWorkerFailureBackend(t *testing.T, action string, status int, body string) *int {
	t.Helper()
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/execution/device-sessions/" + flowSessionID:
			fmt.Fprintf(w, `{"id":%q,"org_id":"fixture-org","platform":"ios","status":"running","workflow_run_id":%q}`, flowSessionID, flowWorkflowRunID)
		case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/" + action:
			calls++
			w.WriteHeader(status)
			fmt.Fprint(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	return &calls
}

func decodeFailurePayload(t *testing.T, stdout string) map[string]string {
	t.Helper()
	var payload map[string]string
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	return payload
}

func TestDeviceInstallTimeoutJSONNamesSessionAndHintsInstallMayStillRun(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv(sessionIDEnvVar, "")
	calls := newMobileWorkerFailureBackend(t, "install", 524, "")
	cmd := newDeviceInstallTestCommand(context.Background())
	if err := cmd.ParseFlags([]string{"-s", flowSessionID, "--json", "--app-url", "https://downloads.example/app.ipa", "--bundle-id", "com.example.app"}); err != nil {
		t.Fatal(err)
	}

	var err error
	stdout, _ := captureStdoutAndStderrSeparate(t, func() { err = deviceInstallCmd.RunE(cmd, nil) })

	if err == nil || *calls != 1 {
		t.Fatalf("install 524 was swallowed or retried: calls=%d err=%v", *calls, err)
	}
	payload := decodeFailurePayload(t, stdout)
	if payload["status"] != "failed" || payload["session_id"] != flowSessionID || !strings.Contains(payload["error"], "524") {
		t.Fatalf("failure payload = %v", payload)
	}
	wantHint := "the install may still be running; check 'revyl device info --session-id " + flowSessionID + "'"
	if !strings.Contains(payload["hint"], wantHint) || !strings.Contains(err.Error(), wantHint) {
		t.Fatalf("hint missing: payload=%v err=%v", payload, err)
	}
}

func TestDeviceLaunchServerErrorJSONNamesSessionWithoutTimeoutHint(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv(sessionIDEnvVar, "")
	newMobileWorkerFailureBackend(t, "launch", http.StatusInternalServerError, "")
	cmd := newStopTestCommand(t, "-s", flowSessionID, "--json")
	cmd.Flags().String("bundle-id", "", "")

	var err error
	stdout, _ := captureStdoutAndStderrSeparate(t, func() { err = deviceLaunchCmd.RunE(cmd, nil) })

	if err == nil {
		t.Fatal("launch 500 was swallowed")
	}
	payload := decodeFailurePayload(t, stdout)
	if payload["status"] != "failed" || payload["session_id"] != flowSessionID || payload["error"] == "" {
		t.Fatalf("failure payload = %v", payload)
	}
	if _, ok := payload["hint"]; ok {
		t.Fatalf("non-timeout failure carried a still-running hint: %v", payload)
	}
}

func TestDeviceLaunchFailureWithoutJSONKeepsStdoutEmpty(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv(sessionIDEnvVar, "")
	newMobileWorkerFailureBackend(t, "launch", 524, "")
	cmd := newStopTestCommand(t, "-s", flowSessionID)
	cmd.Flags().String("bundle-id", "", "")

	var err error
	stdout, _ := captureStdoutAndStderrSeparate(t, func() { err = deviceLaunchCmd.RunE(cmd, nil) })

	if err == nil || !strings.Contains(err.Error(), "the launch may still be running") {
		t.Fatalf("launch 524 error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("human failure wrote stdout: %q", stdout)
	}
}

func TestMobileTapBare500WarnsTapMayNotHaveBeenDelivered(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv(sessionIDEnvVar, "")
	calls := newMobileWorkerFailureBackend(t, "tap", http.StatusInternalServerError, "")
	cmd := newStopTestCommand(t, "-s", flowSessionID)
	cmd.Flags().String("target", "", "")
	cmd.Flags().Int("x", 0, "")
	cmd.Flags().Int("y", 0, "")
	for name, value := range map[string]string{"x": "10", "y": "20"} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}

	var err error
	captureStdoutAndStderrSeparate(t, func() { err = deviceTapCmd.RunE(cmd, nil) })

	if err == nil || *calls != 1 {
		t.Fatalf("tap 500 was swallowed or retried: calls=%d err=%v", *calls, err)
	}
	if !strings.Contains(err.Error(), "the tap may not have been delivered; take a screenshot before retrying") {
		t.Fatalf("error %q lacks the delivery warning", err.Error())
	}
}

func TestDeviceDoctorWebSessionReportsBrowserHealth(t *testing.T) {
	cases := []struct {
		name       string
		health     string
		wantStatus string
		wantPassed bool
	}{
		{"ready", `{"ready":true,"first_frame_observed":true,"current_fence":true}`, "pass", true},
		{"before first frame", `{"ready":false,"first_frame_observed":false,"current_fence":true}`, "fail", false},
		{"no ready field", `{"status":"ok"}`, "unknown", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "fixture-key")
			t.Setenv(sessionIDEnvVar, "")
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/execution/device-sessions/" + flowSessionID:
					fmt.Fprintf(w, `{"id":%q,"org_id":"fixture-org","platform":"web","viewer_kind":"whep","status":"running","workflow_run_id":%q}`, flowSessionID, flowWorkflowRunID)
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/health":
					fmt.Fprint(w, tc.health)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backend.Close)
			t.Setenv("REVYL_BACKEND_URL", backend.URL)
			cmd := newStopTestCommand(t, "-s", flowSessionID, "--json")

			var err error
			stdout, _ := captureStdoutAndStderrSeparate(t, func() { err = deviceDoctorCmd.RunE(cmd, nil) })

			if err != nil {
				t.Fatal(err)
			}
			var output struct {
				Checks []struct {
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"checks"`
				AllPassed bool `json:"all_passed"`
			}
			if err := json.Unmarshal([]byte(stdout), &output); err != nil {
				t.Fatalf("doctor stdout is not JSON: %v\n%s", err, stdout)
			}
			deviceStatus := ""
			for _, check := range output.Checks {
				if check.Name == "device" {
					deviceStatus = check.Status
				}
			}
			if deviceStatus != tc.wantStatus || output.AllPassed != tc.wantPassed {
				t.Fatalf("device=%q all_passed=%t, want %q %t; stdout=%s", deviceStatus, output.AllPassed, tc.wantStatus, tc.wantPassed, stdout)
			}
		})
	}
}
