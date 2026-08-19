package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildPreviewURLUsesRoutedAppURL(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "https://app.example.test/")

	got := buildPreviewURL(false, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	want := "https://app.example.test/sessions/launch?buildId=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	if got != want {
		t.Fatalf("buildPreviewURL() = %q, want %q", got, want)
	}
}

func TestBuildPreviewURLUsesDevRoutedAppURL(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "")

	got := buildPreviewURL(true, "build-1")
	if !strings.HasPrefix(got, "http://localhost:") {
		t.Fatalf("buildPreviewURL(dev) = %q, want localhost prefix", got)
	}
	if !strings.HasSuffix(got, "/sessions/launch?buildId=build-1") {
		t.Fatalf("buildPreviewURL(dev) = %q, want sessions launch query suffix", got)
	}
}

func TestBuildPreviewURLEmptyBuildID(t *testing.T) {
	if got := buildPreviewURL(false, "  "); got != "" {
		t.Fatalf("buildPreviewURL(blank) = %q, want empty", got)
	}
}

func TestBuildUploadJSONOmitsPreviewURLByDefault(t *testing.T) {
	build := BuildUploadJSONBuild{
		PlatformKey: "ios",
		Platform:    "ios",
		AppID:       "app-1",
		BuildID:     "build-1",
	}
	data, err := json.Marshal(build)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "preview_url") {
		t.Fatalf("JSON = %s, did not want preview_url", data)
	}
}

func TestBuildUploadJSONIncludesPreviewURLWhenSet(t *testing.T) {
	build := BuildUploadJSONBuild{
		PlatformKey: "ios",
		Platform:    "ios",
		AppID:       "app-1",
		BuildID:     "build-1",
		PreviewURL:  "https://app.revyl.ai/sessions/launch?buildId=build-1",
	}
	data, err := json.Marshal(build)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got, _ := decoded["preview_url"].(string); got != build.PreviewURL {
		t.Fatalf("preview_url = %q, want %q", got, build.PreviewURL)
	}
}

func TestApplyUploadPreviewRequiresFlag(t *testing.T) {
	original := uploadPreviewFlag
	t.Cleanup(func() { uploadPreviewFlag = original })
	uploadPreviewFlag = false

	if got := applyUploadPreview(nil, "build-1"); got != "" {
		t.Fatalf("applyUploadPreview() = %q, want empty", got)
	}
}

func TestURLUploadJSONKeepsOriginalKeysAndAddsPreviewURL(t *testing.T) {
	payload := urlUploadJSON{
		Platform:    "android",
		AppID:       "app-1",
		Version:     "1.2.3",
		VersionID:   "build-1",
		PackageName: "com.example.app",
		WasReused:   true,
		SourceURL:   "https://artifacts.example.com/app.apk",
		PreviewURL:  "https://app.example.test/sessions/launch?buildId=build-1",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	want := map[string]interface{}{
		"platform":     "android",
		"app_id":       "app-1",
		"version":      "1.2.3",
		"version_id":   "build-1",
		"package_name": "com.example.app",
		"was_reused":   true,
		"source_url":   "https://artifacts.example.com/app.apk",
		"preview_url":  payload.PreviewURL,
	}
	for key, expected := range want {
		if got := decoded[key]; got != expected {
			t.Fatalf("%s = %#v, want %#v", key, got, expected)
		}
	}
	for _, unexpected := range []string{"success", "count", "build", "builds", "build_id", "build_version", "package_id"} {
		if _, ok := decoded[unexpected]; ok {
			t.Fatalf("JSON = %s, did not want %s", data, unexpected)
		}
	}
}

func TestURLUploadJSONOmitsPreviewURLByDefault(t *testing.T) {
	data, err := json.Marshal(urlUploadJSON{
		Platform:  "ios",
		AppID:     "app-1",
		Version:   "1.0.0",
		VersionID: "build-1",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "preview_url") {
		t.Fatalf("JSON = %s, did not want preview_url", data)
	}
}

func TestApplyUploadPreviewReturnsURL(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "https://app.example.test")
	originalFlag := uploadPreviewFlag
	originalJSON := buildUploadJSON
	t.Cleanup(func() {
		uploadPreviewFlag = originalFlag
		buildUploadJSON = originalJSON
	})
	uploadPreviewFlag = true
	buildUploadJSON = true

	got := applyUploadPreview(nil, "build-1")
	want := "https://app.example.test/sessions/launch?buildId=build-1"
	if got != want {
		t.Fatalf("applyUploadPreview() = %q, want %q", got, want)
	}
}
