package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConcurrencyLimitErrorHints(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantHint   bool
	}{
		{"test limit", 429, `{"detail":"Concurrency limit reached for org example (2/2)"}`, true},
		{"workflow limit", 429, `{"detail":"Org concurrency is fully utilized. Please upgrade your plan to continue."}`, true},
		{"structured detail", 429, `{"detail":{"message":"Concurrency limit reached","retry_allowed":true}}`, true},
		{"Atlas concurrency", 409, `{"detail":"Not enough concurrency: this exploration needs 3 slots."}`, true},
		{"top-level code", 409, `{"code":"CONCURRENCY_LIMIT_EXCEEDED","message":"Capacity denied"}`, true},
		{"detail code", 429, `{"detail":{"code":"CONCURRENCY_LIMIT_REACHED"}}`, true},
		{"detail error type", 409, `{"detail":{"error_type":"concurrency_limit_exceeded"}}`, true},
		{"unrelated conflict", 409, `{"detail":"Configuration changed"}`, false},
		{"unrelated code", 429, `{"code":"NO_WORKERS_AVAILABLE"}`, false},
		{"rate limit", 429, `{"detail":"Too many requests"}`, false},
		{"lease service outage", 503, `{"detail":"Concurrency service unavailable"}`, false},
		{"allowance exhausted", 402, `{"detail":"Usage allowance exhausted"}`, false},
		{"permission denied", 403, `{"detail":"Permission denied"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiErr := parseAPIErrorBody(tt.statusCode, []byte(tt.body))
			if got := strings.Contains(apiErr.Error(), ConcurrencyUpgradeHint); got != tt.wantHint {
				t.Fatalf("error = %q, want concurrency hint: %v", apiErr.Error(), tt.wantHint)
			}
			if apiErr.StatusCode != tt.statusCode {
				t.Fatalf("status = %d, want %d", apiErr.StatusCode, tt.statusCode)
			}
			if tt.name == "structured detail" && (!apiErr.DetailBool("retry_allowed") || apiErr.Detail != "Concurrency limit reached") {
				t.Fatalf("structured detail was not preserved: %+v", apiErr)
			}
		})
	}
}

func TestConcurrencyHintStatusAndCarrierMatrix(t *testing.T) {
	for _, status := range []struct {
		code int
		hint string
	}{
		{409, ConcurrencyUpgradeHint},
		{429, ConcurrencyUpgradeHint},
		{400, ""},
		{401, "Session may have expired. Run 'revyl auth login' to re-authenticate."},
		{402, "Your workspace has used its included allowance. Choose a plan with more monthly usage:\n  → revyl auth billing"},
		{403, ""},
		{422, ""},
		{500, ""},
		{503, ""},
	} {
		for _, evidence := range []struct {
			name     string
			values   []string
			carriers []string
		}{
			{
				name:   "message",
				values: []string{"Concurrency limit reached", "Concurrency limit exceeded", "Org concurrency is fully utilized", "Not enough concurrency", " CONCURRENCY LIMIT REACHED "},
				carriers: []string{
					`{"error":%q}`, `{"message":%q}`, `{"detail":%q}`, `{"detail":{"message":%q,"retry_allowed":true}}`,
				},
			},
			{
				name:   "code",
				values: []string{"CONCURRENCY_LIMIT", "CONCURRENCY_LIMIT_EXCEEDED", "CONCURRENCY_LIMIT_REACHED", " concurrency_limit_exceeded "},
				carriers: []string{
					`{"code":%q,"message":"Capacity denied"}`, `{"detail":{"code":%q}}`, `{"detail":{"error_type":%q}}`,
				},
			},
		} {
			for _, value := range evidence.values {
				for carrierIndex, carrier := range evidence.carriers {
					t.Run(fmt.Sprintf("%d/%s/%s/%d", status.code, evidence.name, value, carrierIndex), func(t *testing.T) {
						apiErr := parseAPIErrorBody(status.code, []byte(fmt.Sprintf(carrier, value)))
						if apiErr.Hint != status.hint || apiErr.StatusCode != status.code {
							t.Fatalf("error = %+v, want status %d and hint %q", apiErr, status.code, status.hint)
						}
					})
				}
			}
		}
	}
}

func TestConcurrencyHTTPHintIsNotDuplicated(t *testing.T) {
	for _, carrier := range []string{`{"error":%q}`, `{"message":%q}`, `{"detail":%q}`, `{"detail":{"message":%q}}`} {
		for _, status := range []int{409, 429} {
			t.Run(fmt.Sprintf("%d/%s", status, carrier), func(t *testing.T) {
				message := "Concurrency limit reached. " + ConcurrencyUpgradeHint
				apiErr := parseAPIErrorBody(status, []byte(fmt.Sprintf(carrier, message)))
				if got := apiErr.Error(); got != message {
					t.Fatalf("error = %q, want unchanged %q", got, message)
				}
			})
		}
	}
}

func TestStartDeviceConcurrencyRejectionPreservesResponseContract(t *testing.T) {
	for _, message := range []string{
		"Concurrency limit reached. Please upgrade your plan to continue.",
		"Concurrency limit reached. " + ConcurrencyUpgradeHint,
		"No healthy devices available",
	} {
		t.Run(message, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/telemetry/cli-traces" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.URL.Path != "/api/v1/execution/start_device" {
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"status": "cancelled", "error": message, "retry_allowed": true, "platform": "ios",
				})
			}))
			defer server.Close()
			client := NewClientWithBaseURL("test-key", server.URL)
			response, err := client.StartDevice(context.Background(), &StartDeviceRequest{Platform: "ios"})
			if err != nil {
				t.Fatal(err)
			}
			if response.Error == nil || *response.Error != withConcurrencyUpgradeHint(message) {
				t.Fatalf("response error = %v", response.Error)
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]interface{}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded["status"] != "cancelled" || decoded["retry_allowed"] != true || decoded["platform"] != "ios" || response.WorkflowRunId != nil {
				t.Fatalf("response contract changed: %s", encoded)
			}
			wantCount := 0
			if strings.Contains(message, "Concurrency limit reached") {
				wantCount = 1
			}
			if strings.Count(*response.Error, ConcurrencyUpgradeHint) != wantCount {
				t.Fatalf("unexpected hint count: %q", *response.Error)
			}
		})
	}
}

func TestStartDevicePreservesLaunchVariableErrorDetail(t *testing.T) {
	for _, code := range []LaunchEnvVarErrorCode{
		LaunchEnvVarErrorCodeLaunchVariableInvalid,
		LaunchEnvVarErrorCodeLaunchVariableServiceError,
		LaunchEnvVarErrorCodeLaunchVariableServiceUnavailable,
	} {
		t.Run(string(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/telemetry/cli-traces" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.URL.Path != "/api/v1/execution/start_device" {
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"status": "cancelled", "platform": "ios", "error": "Launch variable resolution failed", "retry_allowed": false,
					"error_detail": map[string]interface{}{"code": code, "message": "Launch variable resolution failed", "retryable": false},
				})
			}))
			defer server.Close()
			client := NewClientWithBaseURL("test-key", server.URL)
			response, err := client.StartDevice(context.Background(), &StartDeviceRequest{Platform: "ios"})
			if err != nil {
				t.Fatal(err)
			}
			if response.ErrorDetail == nil || response.ErrorDetail.Code != code || response.ErrorDetail.Message != "Launch variable resolution failed" || response.ErrorDetail.Retryable {
				t.Fatalf("error detail changed: %+v", response.ErrorDetail)
			}
			if response.Status != AsyncStatusCancelled || response.RetryAllowed == nil || *response.RetryAllowed || response.Error == nil || *response.Error != "Launch variable resolution failed" {
				t.Fatalf("response changed: %+v", response)
			}
		})
	}
}
