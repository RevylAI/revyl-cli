package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/publishenv"
)

func TestResolveIOSCredentialInputPrecedence(t *testing.T) {
	env := map[string]string{
		publishenv.ASCKeyID:       "ENV_KEY",
		publishenv.ASCIssuerID:    "ENV_ISSUER",
		publishenv.ASCPrivatePath: "/env/path/AuthKey.p8",
	}
	getenv := func(key string) string { return env[key] }

	input := resolveIOSCredentialInput("FLAG_KEY", "", "/flag/path/AuthKey.p8", getenv)
	if input.KeyID != "FLAG_KEY" {
		t.Fatalf("expected flag key-id to win, got %q", input.KeyID)
	}
	if input.IssuerID != "ENV_ISSUER" {
		t.Fatalf("expected issuer-id from env, got %q", input.IssuerID)
	}
	if input.PrivateKeyPath != "/flag/path/AuthKey.p8" {
		t.Fatalf("expected private key path from flag, got %q", input.PrivateKeyPath)
	}
}

func TestResolveIOSCredentialInputRawKeyFallback(t *testing.T) {
	env := map[string]string{
		publishenv.ASCKeyID:      "ENV_KEY",
		publishenv.ASCIssuerID:   "ENV_ISSUER",
		publishenv.ASCPrivateKey: "-----BEGIN PRIVATE KEY-----\\nabc\\n-----END PRIVATE KEY-----",
	}
	getenv := func(key string) string { return env[key] }

	input := resolveIOSCredentialInput("", "", "", getenv)
	if input.PrivateKeyRaw == "" {
		t.Fatal("expected raw private key from env")
	}
	if input.PrivateKeyPath != "" {
		t.Fatalf("expected empty private key path when raw key provided, got %q", input.PrivateKeyPath)
	}
}

func TestSplitCSVValues(t *testing.T) {
	values := splitCSVValues(" Internal,External, Internal , ,Beta ")
	if len(values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(values))
	}
	if values[0] != "Internal" || values[1] != "External" || values[2] != "Beta" {
		t.Fatalf("unexpected parsed values: %#v", values)
	}
}

func TestExtractIPAMetadata(t *testing.T) {
	tempDir := t.TempDir()
	ipaPath := filepath.Join(tempDir, "Test.ipa")

	file, err := os.Create(ipaPath)
	if err != nil {
		t.Fatalf("failed to create test IPA: %v", err)
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	w, err := zipWriter.Create("Payload/Test.app/Info.plist")
	if err != nil {
		t.Fatalf("failed to create plist entry: %v", err)
	}

	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key><string>com.example.test</string>
  <key>CFBundleShortVersionString</key><string>1.2.3</string>
  <key>CFBundleVersion</key><string>42</string>
</dict>
</plist>`
	if _, err := w.Write([]byte(plist)); err != nil {
		t.Fatalf("failed to write plist content: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("failed to finalize test IPA: %v", err)
	}

	meta, err := extractIPAMetadata(ipaPath)
	if err != nil {
		t.Fatalf("extractIPAMetadata returned error: %v", err)
	}
	if meta.BundleID != "com.example.test" {
		t.Fatalf("expected bundle ID com.example.test, got %q", meta.BundleID)
	}
	if meta.Version != "1.2.3" {
		t.Fatalf("expected version 1.2.3, got %q", meta.Version)
	}
	if meta.BuildNumber != "42" {
		t.Fatalf("expected build number 42, got %q", meta.BuildNumber)
	}
}
