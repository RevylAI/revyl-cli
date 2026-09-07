//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
)

func TestDeviceArtifactExportsAtWindowsDriveRoot(t *testing.T) {
	volumeRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	reference, err := os.CreateTemp(volumeRoot, "revyl-artifact-root-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) && os.Getenv("CI") == "" {
			t.Skipf("drive root is not writable: %v", err)
		}
		t.Fatal(err)
	}
	referencePath := reference.Name()
	t.Cleanup(func() { _ = os.Remove(referencePath) })
	if err := reference.Close(); err != nil {
		t.Fatal(err)
	}
	inheritedDACL := windowsFileDACL(t, referencePath)
	for _, pathMode := range []string{"absolute", "relative"} {
		t.Run(pathMode, func(t *testing.T) {
			t.Chdir(volumeRoot)
			for _, command := range []*cobra.Command{deviceScreenshotCmd, deviceHierarchyCmd} {
				t.Run(command.Name(), func(t *testing.T) {
					path := referencePath + "-" + pathMode + "-" + command.Name()
					t.Cleanup(func() { _ = os.Remove(path) })
					out := path
					if pathMode == "relative" {
						out = filepath.Base(path)
					}
					for _, state := range []string{"create", "overwrite"} {
						t.Run(state, func(t *testing.T) {
							if state == "overwrite" {
								if err := os.WriteFile(path, []byte("legacy artifact with enough trailing bytes to detect a missing truncate operation"), 0o600); err != nil {
									t.Fatal(err)
								}
							}
							output, err := runDeviceArtifactExport(t, command, out)
							if err != nil {
								t.Fatalf("export at drive root: %v", err)
							}
							contents, err := os.ReadFile(path)
							want := `{"success":true,"action":"` + command.Name() + `"}`
							if err != nil || string(contents) != want {
								t.Fatalf("artifact contents=%q error=%v", contents, err)
							}
							var result map[string]string
							if err := json.Unmarshal([]byte(output), &result); err != nil {
								t.Fatal(err)
							}
							if result["path"] != out || result["bytes"] != strconv.Itoa(len(contents)) {
								t.Fatalf("unexpected export result: %v", result)
							}
							if got := windowsFileDACL(t, path); got != inheritedDACL {
								t.Fatalf("file DACL=%s, want inherited %s", got, inheritedDACL)
							}
						})
					}
				})
			}
		})
	}
}
