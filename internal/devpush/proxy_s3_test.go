package devpush

import (
	"strings"
	"testing"
)

func TestParseInstallResponse_Success(t *testing.T) {
	body := []byte(`{"success":true,"bundle_id":"com.example.app","data_preserved":true,"install_method":"hot_swap","latency_ms":1234}`)
	result, err := parseInstallResponse(body)
	if err != nil {
		t.Fatalf("parseInstallResponse() error = %v, want nil", err)
	}
	if !result.Success {
		t.Fatal("expected Success=true")
	}
	if result.BundleID != "com.example.app" {
		t.Fatalf("BundleID = %q, want %q", result.BundleID, "com.example.app")
	}
	if !result.DataPreserved {
		t.Fatal("expected DataPreserved=true")
	}
	if result.InstallMethod != "hot_swap" {
		t.Fatalf("InstallMethod = %q, want %q", result.InstallMethod, "hot_swap")
	}
}

func TestParseInstallResponse_FailureWithError(t *testing.T) {
	body := []byte(`{"success":false,"action":"install","error":"disk full","latency_ms":500}`)
	result, err := parseInstallResponse(body)
	if err == nil {
		t.Fatal("parseInstallResponse() error = nil, want non-nil for success:false")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "disk full")
	}
	if result == nil {
		t.Fatal("expected non-nil result even on logical failure")
	}
	if result.Success {
		t.Fatal("expected Success=false in returned result")
	}
}

func TestParseInstallResponse_FailureWithoutError(t *testing.T) {
	body := []byte(`{"success":false,"latency_ms":100}`)
	result, err := parseInstallResponse(body)
	if err == nil {
		t.Fatal("parseInstallResponse() error = nil, want non-nil for success:false")
	}
	if !strings.Contains(err.Error(), "install action failed") {
		t.Fatalf("error = %q, want generic fallback message", err.Error())
	}
	if result == nil {
		t.Fatal("expected non-nil result even on logical failure")
	}
}

func TestParseInstallResponse_MalformedJSON(t *testing.T) {
	body := []byte(`not-json`)
	result, err := parseInstallResponse(body)
	if err == nil {
		t.Fatal("parseInstallResponse() error = nil, want non-nil for bad JSON")
	}
	if result != nil {
		t.Fatalf("expected nil result for malformed JSON, got %+v", result)
	}
	if !strings.Contains(err.Error(), "failed to parse") {
		t.Fatalf("error = %q, want parse error", err.Error())
	}
}

func TestParseInstallResponse_EmptyBody(t *testing.T) {
	body := []byte(`{}`)
	_, err := parseInstallResponse(body)
	if err == nil {
		t.Fatal("parseInstallResponse() error = nil, want non-nil for zero-value success (false)")
	}
	if !strings.Contains(err.Error(), "install action failed") {
		t.Fatalf("error = %q, want generic fallback message", err.Error())
	}
}
