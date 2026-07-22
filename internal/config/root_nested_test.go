package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectRootReturnsMissingErrorWithoutConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".revyl"), 0o755); err != nil {
		t.Fatalf("mkdir .revyl: %v", err)
	}

	_, err := FindProjectRoot(root)
	var missing *MissingProjectRootError
	if !errors.As(err, &missing) {
		t.Fatalf("FindProjectRoot() error = %v, want MissingProjectRootError", err)
	}
	if missing.WorkingDirectory != root {
		t.Fatalf("missing working directory = %q, want %q", missing.WorkingDirectory, root)
	}
	wantMessage := fmt.Sprintf(
		"no initialized Revyl project found from %q; run \"revyl init --non-interactive\" in that directory",
		root,
	)
	if err.Error() != wantMessage {
		t.Fatalf("FindProjectRoot() error = %q, want %q", err.Error(), wantMessage)
	}
}

func TestFindProjectRootFindsSingleNestedProject(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "apps", "mobile")
	writeProjectConfig(t, appRoot)
	writeProjectConfig(t, filepath.Join(root, "node_modules", "ignored"))

	got, err := FindProjectRoot(root)
	if err != nil {
		t.Fatalf("FindProjectRoot(): %v", err)
	}
	if got != appRoot {
		t.Fatalf("FindProjectRoot() = %q, want %q", got, appRoot)
	}
}

func TestFindProjectRootFindsProjectAtMaximumSearchDepth(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "clients", "acme", "apps", "mobile")
	writeProjectConfig(t, appRoot)

	got, err := FindProjectRoot(root)
	if err != nil {
		t.Fatalf("FindProjectRoot(): %v", err)
	}
	if got != appRoot {
		t.Fatalf("FindProjectRoot() = %q, want %q", got, appRoot)
	}
}

func TestFindProjectRootExcludesProjectBeyondMaximumSearchDepth(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "clients", "acme", "apps", "mobile", "ios")
	writeProjectConfig(t, appRoot)

	if _, err := FindProjectRoot(root); err == nil {
		t.Fatalf("FindProjectRoot() found project beyond depth %d", nestedProjectSearchDepth)
	}
}

func TestFindProjectRootReturnsAmbiguousCandidates(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "apps", "ios")
	second := filepath.Join(root, "apps", "android")
	writeProjectConfig(t, first)
	writeProjectConfig(t, second)

	_, err := FindProjectRoot(root)
	var ambiguous *AmbiguousProjectRootsError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("FindProjectRoot() error = %v, want AmbiguousProjectRootsError", err)
	}
	if len(ambiguous.Roots) != 2 {
		t.Fatalf("ambiguous roots = %v, want two", ambiguous.Roots)
	}
	if ambiguous.WorkingDirectory != root {
		t.Fatalf("ambiguous working directory = %q, want %q", ambiguous.WorkingDirectory, root)
	}
	if ambiguous.Roots[0] != second || ambiguous.Roots[1] != first {
		t.Fatalf("ambiguous roots = %v, want deterministic roots [%q %q]", ambiguous.Roots, second, first)
	}
	wantMessage := fmt.Sprintf(
		"multiple initialized Revyl projects found under %q: %s, %s; retry with project_dir set to one candidate root",
		root,
		second,
		first,
	)
	if err.Error() != wantMessage {
		t.Fatalf("FindProjectRoot() error = %q, want %q", err.Error(), wantMessage)
	}
}

func TestFindProjectRootPrefersNearestParent(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeProjectConfig(t, filepath.Join(root, "nested"))
	child := filepath.Join(root, "Sources")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	got, err := FindProjectRoot(child)
	if err != nil {
		t.Fatalf("FindProjectRoot(): %v", err)
	}
	if got != root {
		t.Fatalf("FindProjectRoot() = %q, want nearest parent %q", got, root)
	}
}

// writeProjectConfig creates the minimum initialized Revyl project fixture.
func writeProjectConfig(t *testing.T, root string) {
	t.Helper()
	configDir := filepath.Join(root, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("project:\n  name: fixture\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
