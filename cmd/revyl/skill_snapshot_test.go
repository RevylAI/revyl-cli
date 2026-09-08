package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSkillSnapshotsRetainExactModificationTimeChecks(t *testing.T) {
	for name, snapshot := range map[string]func(*testing.T, string) map[string]string{
		"discovery": snapshotSkillDiscoveryTree,
		"upgrade":   snapshotUpgradeSkillTree,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "skill")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(file, []byte("unchanged content"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, root)
			for range 5 {
				if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
					t.Fatalf("read-only snapshots changed: before=%q after=%q", before, after)
				}
			}
			modified := time.Unix(1700000000, 0)
			for _, path := range []string{file, dir} {
				if err := os.Chtimes(path, modified, modified); err != nil {
					t.Fatal(err)
				}
				if after := snapshot(t, root); before[path] == after[path] {
					t.Fatalf("snapshot missed an mtime-only change to %s", path)
				}
			}
		})
	}
}
