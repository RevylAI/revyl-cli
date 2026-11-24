package mcp

import (
	"net/http"
	"strings"
	"testing"
)

func TestDoctorDeviceHealthReadsThePlatformsHealthField(t *testing.T) {
	cases := []struct {
		name       string
		platform   string
		body       string
		wantStatus string
		wantDetail string
	}{
		{"web ready", "web", `{"ready":true,"first_frame_observed":true,"current_fence":true}`, "pass", ""},
		{"web before first frame", "web", `{"ready":false,"first_frame_observed":false,"current_fence":true}`, "fail", "has not shown its first frame yet"},
		{"web stale frame", "web", `{"ready":false,"first_frame_observed":true,"current_fence":true}`, "fail", "not ready for input"},
		{"web without ready", "web", `{"device_connected":false}`, "unknown", "did not report ready"},
		{"web unparseable", "web", `not json`, "unknown", "did not report ready"},
		{"mobile connected", "ios", `{"device_connected":true}`, "pass", ""},
		{"mobile disconnected", "android", `{"device_connected":false}`, "fail", "device not connected"},
		{"mobile without device_connected", "ios", `{"status":"ok"}`, "unknown", "did not report device_connected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, detail := DoctorDeviceHealth(tc.platform, []byte(tc.body))

			if status != tc.wantStatus {
				t.Fatalf("status = %q, want %q (detail %q)", status, tc.wantStatus, detail)
			}
			if tc.wantDetail == "" && detail != "" || !strings.Contains(detail, tc.wantDetail) {
				t.Fatalf("detail = %q, want it to contain %q", detail, tc.wantDetail)
			}
		})
	}
}

func TestWorkerHTTPErrorWarnsBareInputFailureMayNotHaveBeenDelivered(t *testing.T) {
	cases := []struct {
		name string
		err  *WorkerHTTPError
		want string
	}{
		{"bare 500 tap", &WorkerHTTPError{StatusCode: http.StatusInternalServerError, Path: "/tap"}, "the tap may not have been delivered; take a screenshot before retrying"},
		{"cloudflare 524 swipe", &WorkerHTTPError{StatusCode: 524, Path: "/swipe"}, "the swipe may not have been delivered; take a screenshot before retrying"},
		{"detail-only 500 long press", &WorkerHTTPError{StatusCode: http.StatusInternalServerError, Path: "/longpress", proxyDetail: "worker crashed"}, "the longpress may not have been delivered; take a screenshot before retrying"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if message := tc.err.Error(); !strings.Contains(message, tc.want) {
				t.Fatalf("error %q is missing %q", message, tc.want)
			}
		})
	}
}

func TestWorkerHTTPErrorKeepsOtherFailuresFreeOfUndeliveredInputWarning(t *testing.T) {
	cases := []struct {
		name string
		err  *WorkerHTTPError
	}{
		{"read action", &WorkerHTTPError{StatusCode: http.StatusInternalServerError, Path: "/screenshot"}},
		{"worker gone", &WorkerHTTPError{StatusCode: http.StatusBadGateway, Path: "/tap", proxyCode: "worker_gone"}},
		{"other proxy code", &WorkerHTTPError{StatusCode: http.StatusInternalServerError, Path: "/tap", proxyCode: "install_failed"}},
		{"client error", &WorkerHTTPError{StatusCode: http.StatusBadRequest, Path: "/tap"}},
		{"gateway", &WorkerHTTPError{StatusCode: http.StatusBadGateway, Path: "/tap"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if message := tc.err.Error(); strings.Contains(message, "may not have been delivered") {
				t.Fatalf("error %q warned about undelivered input", message)
			}
		})
	}
}
