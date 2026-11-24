package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsExportedCLIRecognizesOnlyThePublishedRepository(t *testing.T) {
	for _, tc := range []struct {
		name     string
		paths    []string
		exported bool
	}{
		{"monorepo with every marker", []string{".github/workflows/ci.yml", "frontend/src/lib/", "gtm/apps/website/src/lib/", "revyl-cli/go.mod"}, false},
		{"monorepo without its CI workflow", []string{"frontend/src/lib/", "gtm/apps/website/src/lib/", "revyl-cli/go.mod"}, false},
		{"sparse monorepo without the prompt directories", []string{".github/workflows/ci.yml", "revyl-cli/go.mod"}, false},
		{"sparse monorepo holding only revyl-cli", []string{".git/", "revyl-cli/go.mod"}, false},
		{"published repository cloned beside other projects", []string{".git/", "frontend/", "revyl-cli/.git/", "revyl-cli/go.mod"}, true},
		{"published module with nothing beside it", []string{"revyl-cli/go.mod"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, path := range tc.paths {
				full := filepath.Join(root, filepath.FromSlash(path))
				if path[len(path)-1] == '/' {
					if err := os.MkdirAll(full, 0o755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := isExportedCLI(filepath.Join(root, "revyl-cli")); got != tc.exported {
				t.Errorf("isExportedCLI = %v, want %v", got, tc.exported)
			}
		})
	}
}
