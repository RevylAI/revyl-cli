package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revyl/cli/internal/asc"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/publishenv"
	"github.com/revyl/cli/internal/store"
	"github.com/revyl/cli/internal/ui"
)

type iosCredentialInput struct {
	KeyID          string
	IssuerID       string
	PrivateKeyPath string
	PrivateKeyRaw  string
}

type ipaMetadata struct {
	BundleID    string
	Version     string
	BuildNumber string
}

func resolveIOSCredentialInput(keyIDFlag, issuerIDFlag, privateKeyFlag string, getenv func(string) string) iosCredentialInput {
	keyID := strings.TrimSpace(keyIDFlag)
	if keyID == "" {
		keyID = strings.TrimSpace(getenv(publishenv.ASCKeyID))
	}

	issuerID := strings.TrimSpace(issuerIDFlag)
	if issuerID == "" {
		issuerID = strings.TrimSpace(getenv(publishenv.ASCIssuerID))
	}

	privatePath := strings.TrimSpace(privateKeyFlag)
	if privatePath == "" {
		privatePath = strings.TrimSpace(getenv(publishenv.ASCPrivatePath))
	}

	privateRaw := ""
	if privatePath == "" {
		privateRaw = strings.TrimSpace(getenv(publishenv.ASCPrivateKey))
	}

	return iosCredentialInput{
		KeyID:          keyID,
		IssuerID:       issuerID,
		PrivateKeyPath: privatePath,
		PrivateKeyRaw:  privateRaw,
	}
}

func hasAnyIOSCredentialEnv(getenv func(string) string) bool {
	return strings.TrimSpace(getenv(publishenv.ASCKeyID)) != "" ||
		strings.TrimSpace(getenv(publishenv.ASCIssuerID)) != "" ||
		strings.TrimSpace(getenv(publishenv.ASCPrivatePath)) != "" ||
		strings.TrimSpace(getenv(publishenv.ASCPrivateKey)) != ""
}

func missingIOSCredentialFields(in iosCredentialInput) []string {
	missing := make([]string, 0, 3)
	if in.KeyID == "" {
		missing = append(missing, "key-id")
	}
	if in.IssuerID == "" {
		missing = append(missing, "issuer-id")
	}
	if in.PrivateKeyPath == "" && in.PrivateKeyRaw == "" {
		missing = append(missing, "private-key")
	}
	return missing
}

func normalizePrivateKeyValue(raw string) string {
	normalized := strings.ReplaceAll(raw, "\\r\\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\\n", "\n")
	if !strings.HasSuffix(normalized, "\n") {
		normalized += "\n"
	}
	return normalized
}

func materializePrivateKeyFromEnv(raw string) (string, error) {
	normalized := normalizePrivateKeyValue(raw)
	sum := sha256.Sum256([]byte(normalized))
	hash := hex.EncodeToString(sum[:])[:12]

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}

	keysDir := filepath.Join(homeDir, ".revyl", "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create key directory: %w", err)
	}

	path := filepath.Join(keysDir, fmt.Sprintf("asc-key-%s.p8", hash))
	if err := os.WriteFile(path, []byte(normalized), 0o600); err != nil {
		return "", fmt.Errorf("failed to write private key file: %w", err)
	}
	return path, nil
}

func resolveIOSPrivateKeyPath(in iosCredentialInput) (string, error) {
	if in.PrivateKeyPath != "" {
		absPath, err := filepath.Abs(in.PrivateKeyPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve private key path: %w", err)
		}
		return absPath, nil
	}
	if in.PrivateKeyRaw == "" {
		return "", fmt.Errorf("private key is required")
	}
	return materializePrivateKeyFromEnv(in.PrivateKeyRaw)
}

func loadPublishConfig() *config.ProjectConfig {
	cfg, err := config.LoadProjectConfig(".revyl/config.yaml")
	if err != nil {
		return nil
	}
	return cfg
}

func splitCSVValues(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func resolveAppIDFromSources(cmdAppID string, cfg *config.ProjectConfig, getenv func(string) string) string {
	if strings.TrimSpace(cmdAppID) != "" {
		return strings.TrimSpace(cmdAppID)
	}
	if v := strings.TrimSpace(getenv(publishenv.ASCAppID)); v != "" {
		return v
	}
	if cfg != nil && strings.TrimSpace(cfg.Publish.IOS.ASCAppID) != "" {
		return strings.TrimSpace(cfg.Publish.IOS.ASCAppID)
	}
	return ""
}

func resolveBundleIDFromSources(cfg *config.ProjectConfig, getenv func(string) string, ipaBundleID string) string {
	if v := strings.TrimSpace(getenv(publishenv.ASCBundleID)); v != "" {
		return v
	}
	if cfg != nil && strings.TrimSpace(cfg.Publish.IOS.BundleID) != "" {
		return strings.TrimSpace(cfg.Publish.IOS.BundleID)
	}
	if strings.TrimSpace(ipaBundleID) != "" {
		return strings.TrimSpace(ipaBundleID)
	}
	return ""
}

func resolveExecutionIOSCredentials(mgr *store.Manager) (*store.IOSCredentials, error) {
	input := resolveIOSCredentialInput("", "", "", os.Getenv)
	if hasAnyIOSCredentialEnv(os.Getenv) {
		if missing := missingIOSCredentialFields(input); len(missing) > 0 {
			return nil, fmt.Errorf("incomplete ASC env credentials (missing: %s)", strings.Join(missing, ", "))
		}

		privateKeyPath, err := resolveIOSPrivateKeyPath(input)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(privateKeyPath); err != nil {
			return nil, fmt.Errorf("private key file not found: %w", err)
		}
		if _, err := asc.LoadPrivateKey(privateKeyPath); err != nil {
			return nil, fmt.Errorf("invalid private key: %w", err)
		}

		return &store.IOSCredentials{
			KeyID:          input.KeyID,
			IssuerID:       input.IssuerID,
			PrivateKeyPath: privateKeyPath,
		}, nil
	}

	if err := mgr.ValidateIOSCredentials(); err != nil {
		return nil, err
	}
	creds, err := mgr.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}
	return creds.IOS, nil
}

func resolveASCAppIDOrLookup(
	ctx context.Context,
	client *asc.Client,
	cmdAppID string,
	cfg *config.ProjectConfig,
	ipaBundleID string,
) (string, error) {
	appID := resolveAppIDFromSources(cmdAppID, cfg, os.Getenv)
	if appID != "" {
		return appID, nil
	}

	bundleID := resolveBundleIDFromSources(cfg, os.Getenv, ipaBundleID)
	if bundleID == "" {
		return "", fmt.Errorf(
			"app ID is required: set --app-id, %s, publish.ios.asc_app_id, or provide a bundle ID (%s / publish.ios.bundle_id)",
			publishenv.ASCAppID,
			publishenv.ASCBundleID,
		)
	}

	app, err := client.FindAppByBundleID(ctx, bundleID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve app by bundle ID %q: %w", bundleID, err)
	}
	if app == nil {
		return "", fmt.Errorf("no App Store Connect app found for bundle ID %q", bundleID)
	}

	ui.PrintInfo("Resolved App Store Connect app ID %s from bundle ID %s", app.ID, bundleID)
	return app.ID, nil
}

func findUploadedBuild(builds []asc.Build, buildNumber string, uploadedAfter time.Time) *asc.Build {
	cutoff := uploadedAfter.Add(-10 * time.Minute)
	var best *asc.Build

	for i := range builds {
		b := &builds[i]
		if strings.TrimSpace(buildNumber) != "" && b.Attributes.Version != buildNumber {
			continue
		}
		if b.Attributes.UploadedDate != nil && b.Attributes.UploadedDate.Before(cutoff) {
			continue
		}
		if best == nil {
			best = b
			continue
		}
		if b.Attributes.UploadedDate == nil {
			continue
		}
		if best.Attributes.UploadedDate == nil || b.Attributes.UploadedDate.After(*best.Attributes.UploadedDate) {
			best = b
		}
	}

	return best
}

func waitForUploadedBuild(
	ctx context.Context,
	client *asc.Client,
	appID string,
	buildNumber string,
	uploadedAfter time.Time,
	timeout time.Duration,
) (*asc.Build, error) {
	deadline := time.Now().Add(timeout)
	lastStatus := ""
	lastBuildID := ""
	lastMissingPrint := time.Time{}

	for {
		builds, err := client.ListBuilds(ctx, appID, 50)
		if err != nil {
			return nil, fmt.Errorf("failed to list builds: %w", err)
		}

		match := findUploadedBuild(builds, buildNumber, uploadedAfter)
		if match != nil {
			if match.ID != lastBuildID || match.Attributes.ProcessingState != lastStatus {
				ui.PrintInfo("Build %s status: %s", match.ID, match.Attributes.ProcessingState)
				lastBuildID = match.ID
				lastStatus = match.Attributes.ProcessingState
			}

			switch match.Attributes.ProcessingState {
			case asc.ProcessingStateValid:
				return match, nil
			case asc.ProcessingStateFailed, asc.ProcessingStateInvalid:
				return match, fmt.Errorf("build processing failed (state: %s)", match.Attributes.ProcessingState)
			}
		} else if time.Since(lastMissingPrint) > 30*time.Second {
			ui.PrintInfo("Waiting for uploaded build to appear in App Store Connect...")
			lastMissingPrint = time.Now()
		}

		if time.Now().After(deadline) {
			if match != nil {
				return match, fmt.Errorf("timed out waiting for build processing (state: %s)", match.Attributes.ProcessingState)
			}
			return nil, fmt.Errorf("timed out waiting for uploaded build to appear")
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Second):
		}
	}
}

func extractIPAMetadata(ipaPath string) (*ipaMetadata, error) {
	reader, err := zip.OpenReader(ipaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open IPA: %w", err)
	}
	defer reader.Close()

	var plistFile *zip.File
	for _, f := range reader.File {
		name := f.Name
		if strings.HasPrefix(name, "Payload/") && strings.HasSuffix(name, ".app/Info.plist") {
			plistFile = f
			break
		}
	}
	if plistFile == nil {
		return nil, fmt.Errorf("Info.plist not found inside IPA")
	}

	rc, err := plistFile.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open Info.plist: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read Info.plist: %w", err)
	}

	if bytes.HasPrefix(data, []byte("bplist")) {
		return nil, fmt.Errorf("binary Info.plist is not supported for metadata extraction")
	}

	values, err := parseInfoPlistValues(data)
	if err != nil {
		return nil, err
	}

	return &ipaMetadata{
		BundleID:    strings.TrimSpace(values["CFBundleIdentifier"]),
		Version:     strings.TrimSpace(values["CFBundleShortVersionString"]),
		BuildNumber: strings.TrimSpace(values["CFBundleVersion"]),
	}, nil
}

func parseInfoPlistValues(data []byte) (map[string]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	values := make(map[string]string)
	currentKey := ""

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse Info.plist XML: %w", err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "key":
			var key string
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return nil, fmt.Errorf("failed to decode plist key: %w", err)
			}
			currentKey = strings.TrimSpace(key)
		case "string", "integer":
			if currentKey == "" {
				var discard string
				if err := decoder.DecodeElement(&discard, &start); err != nil {
					return nil, fmt.Errorf("failed to decode plist value: %w", err)
				}
				continue
			}
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return nil, fmt.Errorf("failed to decode plist value for %s: %w", currentKey, err)
			}
			values[currentKey] = strings.TrimSpace(value)
			currentKey = ""
		default:
			if currentKey != "" {
				currentKey = ""
			}
		}
	}

	return values, nil
}
