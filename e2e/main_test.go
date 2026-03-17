//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	backendURL = resolveBackendURL()
	apiKey = resolveAPIKey()

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "SKIP: no API key found (set REVYL_API_KEY or run from monorepo root with cognisim_backend/.env)")
		os.Exit(0)
	}

	if backendURL == prodBackendURL {
		fmt.Fprintf(os.Stderr, "FATAL: refusing to run e2e tests against production (%s)\n", prodBackendURL)
		os.Exit(1)
	}

	keySource := "REVYL_API_KEY env"
	if os.Getenv("REVYL_API_KEY") == "" {
		if readCredentialsFile() != "" {
			keySource = "~/.revyl/credentials.json"
		} else {
			keySource = "cognisim_backend/.env"
		}
	}
	urlSource := "REVYL_BACKEND_URL env"
	if os.Getenv("REVYL_BACKEND_URL") == "" {
		if strings.HasPrefix(backendURL, "http://localhost") {
			urlSource = "local auto-detect"
		} else {
			urlSource = "staging fallback"
		}
	}

	// Build the CLI binary once
	tmpDir, err := os.MkdirTemp("", "revyl-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	revylBin = filepath.Join(tmpDir, "revyl")
	cliRoot := findCLIRoot()
	if cliRoot == "" {
		fmt.Fprintln(os.Stderr, "FATAL: cannot find revyl-cli directory (need cmd/revyl)")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  ┌─────────────────────────────────────────────────┐\n")
	fmt.Fprintf(os.Stderr, "  │            Revyl CLI E2E Regression             │\n")
	fmt.Fprintf(os.Stderr, "  ├─────────────────────────────────────────────────┤\n")
	fmt.Fprintf(os.Stderr, "  │  Target:  %-37s │\n", backendURL)
	fmt.Fprintf(os.Stderr, "  │  URL src: %-37s │\n", urlSource)
	fmt.Fprintf(os.Stderr, "  │  Key src: %-37s │\n", keySource)
	fmt.Fprintf(os.Stderr, "  └─────────────────────────────────────────────────┘\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Building CLI binary...\n")

	buildCmd := exec.Command("go", "build", "-o", revylBin, "./cmd/revyl")
	buildCmd.Dir = cliRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to build CLI: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "  Binary: %s\n\n", revylBin)

	// Create a fake "open" binary so macOS doesn't launch a browser.
	// ui.OpenBrowser calls exec.Command("open", url) directly, ignoring BROWSER env.
	fakeOpenDir, err = os.MkdirTemp("", "revyl-e2e-fakeopen-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to create fake-open dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(fakeOpenDir, "open"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to write fake open script: %v\n", err)
		os.Exit(1)
	}

	// Run tests, then clean up temp dirs explicitly.
	// os.Exit skips deferred functions, so we must clean up before calling it.
	code := m.Run()
	os.RemoveAll(fakeOpenDir)
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// findCLIRoot locates the revyl-cli directory (which contains cmd/revyl/).
func findCLIRoot() string {
	root := findMonorepoRoot()
	if root != "" {
		candidate := filepath.Join(root, "revyl-cli")
		if _, err := os.Stat(filepath.Join(candidate, "cmd", "revyl")); err == nil {
			return candidate
		}
	}

	// Fallback: walk up from cwd looking for cmd/revyl/
	cwd, _ := os.Getwd()
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "revyl")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
