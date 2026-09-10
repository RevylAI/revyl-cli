package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcppkg "github.com/revyl/cli/internal/mcp"
)

func TestLoadPushPayloadFile(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"valid", `{"aps":{"alert":"hi"}}`, ""},
		{"not json", `nope`, "not valid JSON"},
		{"missing aps", `{"alert":"hi"}`, "must contain"},
		{"too large", `{"aps":{"alert":"` + string(make([]byte, 5000)) + `"}}`, "4096"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".apns")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadPushPayloadFile(path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
		})
	}
}

func TestBuildPushPayloadFromFlags(t *testing.T) {
	payload := buildPushPayloadFromFlags("Title", "Body", 3, true)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	aps, ok := decoded["aps"].(map[string]any)
	if !ok {
		t.Fatalf("expected aps object, got %v", decoded)
	}
	alert, ok := aps["alert"].(map[string]any)
	if !ok {
		t.Fatalf("expected alert object, got %v", aps)
	}
	if alert["title"] != "Title" || alert["body"] != "Body" {
		t.Fatalf("unexpected alert %v", alert)
	}
	if aps["badge"].(float64) != 3 {
		t.Fatalf("unexpected badge %v", aps["badge"])
	}
}

func TestBuildPushPayloadFromFlagsIncludesExplicitZeroBadge(t *testing.T) {
	payload := buildPushPayloadFromFlags("", "", 0, true)
	aps := payload["aps"].(map[string]any)
	badge, ok := aps["badge"]
	if !ok {
		t.Fatalf("expected badge to be present, got %v", aps)
	}
	if badge != 0 {
		t.Fatalf("unexpected badge %v", badge)
	}
}

func TestBuildPushPayloadFromFlagsOmitsEmptyAlert(t *testing.T) {
	payload := buildPushPayloadFromFlags("", "", 3, true)
	aps := payload["aps"].(map[string]any)
	if _, ok := aps["alert"]; ok {
		t.Fatalf("expected alert to be omitted for a badge-only push, got %v", aps)
	}
	if aps["badge"] != 3 {
		t.Fatalf("unexpected badge %v", aps["badge"])
	}
}

func TestBuildPushPayloadFromFlagsOmitsBadgeWhenUnset(t *testing.T) {
	payload := buildPushPayloadFromFlags("Title", "", 0, false)
	aps := payload["aps"].(map[string]any)
	if _, ok := aps["badge"]; ok {
		t.Fatalf("expected badge to be omitted, got %v", aps)
	}
}

func TestLoadPushPayloadFilePreservesLargeIntegers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large-int.apns")
	const largeInt = "9007199254740993"
	content := `{"aps":{"alert":"hi"},"custom_id":` + largeInt + `}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := loadPushPayloadFile(path)
	if err != nil {
		t.Fatalf("loadPushPayloadFile() error = %v", err)
	}
	number, ok := payload["custom_id"].(json.Number)
	if !ok {
		t.Fatalf("expected custom_id to be json.Number, got %T", payload["custom_id"])
	}
	if number.String() != largeInt {
		t.Fatalf("custom_id round trip = %s, want %s", number.String(), largeInt)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(largeInt)) {
		t.Fatalf("re-encoded payload lost precision: %s", encoded)
	}
}

type fakePushRequester struct {
	respBody []byte
}

func (f *fakePushRequester) WorkerRequestOnSession(
	ctx context.Context,
	session *mcppkg.DeviceSession,
	path string,
	body interface{},
) ([]byte, error) {
	return f.respBody, nil
}

func TestPushNotificationToDeviceUsesResolvedBundleID(t *testing.T) {
	requester := &fakePushRequester{
		respBody: []byte(`{"success":true,"action":"push_notification","bundle_id":"com.resolved.app"}`),
	}
	payload := map[string]any{"aps": map[string]any{"alert": "hi"}}

	got, err := pushNotificationToDevice(context.Background(), requester, testSession(1), payload, "")
	if err != nil {
		t.Fatalf("pushNotificationToDevice() error = %v", err)
	}
	if got != "com.resolved.app" {
		t.Fatalf("bundle id = %q, want com.resolved.app", got)
	}
}

func TestResolvePushBundleIDPrefersFileKey(t *testing.T) {
	payload := map[string]any{
		"aps":                     map[string]any{},
		"Simulator Target Bundle": "com.file.app",
	}
	if got := resolvePushBundleID(payload, "com.flag.app"); got != "com.file.app" {
		t.Fatalf("expected file key to win, got %q", got)
	}
	if got := resolvePushBundleID(map[string]any{"aps": map[string]any{}}, "com.flag.app"); got != "com.flag.app" {
		t.Fatalf("expected flag value, got %q", got)
	}
}
