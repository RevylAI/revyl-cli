// Package config provides project configuration management.
//
// This file contains a one-time migration from the legacy tests:/workflows:
// sections in config.yaml to file-based .revyl/tests/*.yaml stubs.
//
// DELETE THIS FILE once all users have been migrated. Also remove:
//   - Tests/Workflows fields from ProjectConfig in project.go
//   - The migration call-site in cmd/revyl/helpers.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// MigrateConfigTestAliases converts legacy tests: entries in config.yaml to
// .revyl/tests/*.yaml stub files, then rewrites config.yaml without those
// sections. Returns the number of newly created stub files.
//
// Safe to call on every config load -- returns 0 immediately when there is
// nothing to migrate. Idempotent: skips aliases that already have a local
// YAML file. Non-destructive: never overwrites existing test files.
//
// Parameters:
//   - configPath: absolute path to .revyl/config.yaml
//   - cfg: the already-parsed ProjectConfig (Tests map will be nil'd on success)
//
// Returns:
//   - int: number of stub files created
//   - error: any fatal error during migration (partial progress is kept)
func MigrateConfigTestAliases(configPath string, cfg *ProjectConfig) (int, error) {
	if cfg == nil || len(cfg.Tests) == 0 {
		return 0, nil
	}

	testsDir := filepath.Join(filepath.Dir(configPath), "tests")
	if err := os.MkdirAll(testsDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create tests directory: %w", err)
	}

	aliases := make([]string, 0, len(cfg.Tests))
	for alias := range cfg.Tests {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	created := 0
	for _, alias := range aliases {
		uuid := cfg.Tests[alias]
		dest := filepath.Join(testsDir, alias+".yaml")

		if _, err := os.Stat(dest); err == nil {
			continue
		}

		stub := LocalTest{
			Meta: TestMeta{RemoteID: uuid},
			Test: TestDefinition{
				Metadata: TestMetadata{Name: alias},
			},
		}
		if err := SaveLocalTest(dest, &stub); err != nil {
			return created, fmt.Errorf("failed to write stub for %q: %w", alias, err)
		}
		created++
	}

	cfg.Tests = nil
	cfg.Workflows = nil
	if err := WriteProjectConfig(configPath, cfg); err != nil {
		return created, fmt.Errorf("stubs created but failed to rewrite config.yaml: %w", err)
	}

	return created, nil
}
