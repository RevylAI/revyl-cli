package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseHeaderFlags_ValidHeaders(t *testing.T) {
	flags := []string{
		"Authorization: Bearer secret-token",
		"X-Custom: value-with-colon:inside",
	}
	headers, err := parseHeaderFlags(flags)
	if err != nil {
		t.Fatalf("parseHeaderFlags() error = %v", err)
	}
	if headers["Authorization"] != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want %q", headers["Authorization"], "Bearer secret-token")
	}
	if headers["X-Custom"] != "value-with-colon:inside" {
		t.Fatalf("X-Custom = %q, want %q", headers["X-Custom"], "value-with-colon:inside")
	}
}

func TestParseHeaderFlags_EmptySlice(t *testing.T) {
	headers, err := parseHeaderFlags(nil)
	if err != nil {
		t.Fatalf("parseHeaderFlags(nil) error = %v", err)
	}
	if headers != nil {
		t.Fatalf("expected nil map, got %v", headers)
	}
}

func TestParseHeaderFlags_MalformedHeader(t *testing.T) {
	flags := []string{"NoColonHere"}
	_, err := parseHeaderFlags(flags)
	if err == nil {
		t.Fatal("parseHeaderFlags() expected error for malformed header, got nil")
	}
}

func TestParseHeaderFlags_EmptyName(t *testing.T) {
	flags := []string{": value"}
	_, err := parseHeaderFlags(flags)
	if err == nil {
		t.Fatal("parseHeaderFlags() expected error for empty header name, got nil")
	}
}

func TestParseHeaderFlags_WhitespaceOnlyName(t *testing.T) {
	flags := []string{" : value"}
	_, err := parseHeaderFlags(flags)
	if err == nil {
		t.Fatal("parseHeaderFlags() expected error for whitespace-only header name, got nil")
	}
}

func TestBuildUploadFlagsMutualExclusion(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		url     string
		headers []string
		wantErr string
	}{
		{
			name:    "file and url both set",
			file:    "app.apk",
			url:     "https://example.com/app.apk",
			wantErr: "--file and --url are mutually exclusive",
		},
		{
			name:    "header without url",
			headers: []string{"Authorization: Bearer tok"},
			wantErr: "--header requires --url",
		},
		{
			name: "file only is fine",
			file: "app.apk",
		},
		{
			name: "url only is fine",
			url:  "https://example.com/app.apk",
		},
		{
			name: "neither is fine",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUploadSourceFlags(tt.file, tt.url, tt.headers)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestURLUploadPrintsConfirmationLineOnStderr(t *testing.T) {
	const appID = "00000000-0000-4000-8000-000000000001"
	withWorkingDirectory(t, t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/apps/"+appID+"/builds/from-url" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"bv-9","app_id":"` + appID + `","version":"1.2.3"}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	previousURL, previousApp, previousVersion, previousJSON := uploadURLFlag, uploadAppFlag, buildVersion, buildUploadJSON
	t.Cleanup(func() {
		uploadURLFlag, uploadAppFlag, buildVersion, buildUploadJSON = previousURL, previousApp, previousVersion, previousJSON
	})
	uploadURLFlag, uploadAppFlag, buildVersion, buildUploadJSON = "https://artifacts.example/app.apk", appID, "1.2.3", false
	cmd := newLeafCommand("upload", nil)
	cmd.Flags().String("branch", "", "")
	cmd.Flags().Bool("dev", false, "")
	if err := cmd.Flags().Set("branch", "main"); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		if err := runURLUpload(cmd, "fixture-key", false); err != nil {
			t.Fatalf("runURLUpload: %v", err)
		}
	})

	if want := "Uploaded " + appID + " 1.2.3 → build bv-9"; !strings.Contains(stderr, want) {
		t.Fatalf("stderr %q is missing %q", stderr, want)
	}
	if strings.Contains(stdout, "Uploaded") {
		t.Fatalf("confirmation line leaked to stdout: %q", stdout)
	}
}
