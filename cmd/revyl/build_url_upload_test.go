package main

import (
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
