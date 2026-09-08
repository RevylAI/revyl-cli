package skills

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

var shippedContents = map[string]string{
	RevylCLIName:            RevylCLIContent,
	RevylCLICreateName:      RevylCLICreateContent,
	RevylCLIAnalyzeName:     RevylCLIAnalyzeContent,
	RevylCLIOptimizeName:    RevylCLIOptimizeContent,
	RevylCLIDevLoopName:     RevylCLIDevLoopContent,
	RevylCLIAtlasName:       RevylCLIAtlasContent,
	RevylCLIAtlasReviewName: RevylCLIAtlasReviewContent,
	RevylCLIAuthBypassName:  RevylCLIAuthBypassContent,
	RevylMCPName:            RevylMCPContent,
	RevylMCPCreateName:      RevylMCPCreateContent,
	RevylMCPAnalyzeName:     RevylMCPAnalyzeContent,
	RevylMCPDevLoopName:     RevylMCPDevLoopContent,
}

func TestFilesMatchCompleteSourcePackages(t *testing.T) {
	entries, err := packages.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(shippedContents) {
		t.Fatalf("embedded %d packages, want %d", len(entries), len(shippedContents))
	}
	for name, content := range shippedContents {
		t.Run(name, func(t *testing.T) {
			files, err := Files(name)
			if err != nil {
				t.Fatal(err)
			}
			sourcePaths := make([]string, 0)
			err = fs.WalkDir(os.DirFS(name), ".", func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() {
					sourcePaths = append(sourcePaths, path)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(sourcePaths)
			paths := make([]string, 0, len(files))
			for _, file := range files {
				paths = append(paths, file.Path)
				if !fs.ValidPath(file.Path) || strings.Contains(file.Path, `\`) {
					t.Errorf("invalid relative path %q", file.Path)
				}
				sourcePath := filepath.Join(name, filepath.FromSlash(file.Path))
				data, err := os.ReadFile(sourcePath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(file.Content, data) {
					t.Errorf("%s: embedded content differs from source", file.Path)
				}
				info, err := os.Stat(sourcePath)
				if err != nil {
					t.Fatal(err)
				}
				if runtime.GOOS != "windows" && file.Mode != info.Mode().Perm() {
					t.Errorf("%s: install mode %o differs from source %o", file.Path, file.Mode, info.Mode().Perm())
				}
				if file.Path == SkillFileName && string(file.Content) != content {
					t.Error("legacy Content export differs from package SKILL.md")
				}
			}
			if !reflect.DeepEqual(paths, sourcePaths) {
				t.Errorf("package paths = %v, want complete source paths %v", paths, sourcePaths)
			}
			if !slices.IsSorted(paths) || !slices.Contains(paths, SkillFileName) {
				t.Errorf("package paths must be sorted and include SKILL.md: %v", paths)
			}
			again, err := Files(name)
			if err != nil || !reflect.DeepEqual(files, again) {
				t.Fatalf("repeated Files() is not deterministic: %v", err)
			}
			files[0].Content[0] ^= 0xff
			fresh, err := Files(name)
			if err != nil || !reflect.DeepEqual(again, fresh) {
				t.Fatalf("caller mutated embedded package contents: %v", err)
			}
		})
	}
}

func TestFilesRejectUnknownNamesAndTraversal(t *testing.T) {
	for _, name := range []string{
		"", ".", "..", "unknown", "../revyl-cli", "/revyl-cli", "revyl-cli/../revyl-mcp",
		"revyl-cli/SKILL.md", "revyl-cli/", "revyl-cli-auth-bypass/references", " revyl-cli ",
		`..\revyl-cli`, "revyl-cli\x00", "revyl-cli-auth-bypass-expo",
	} {
		t.Run(name, func(t *testing.T) {
			files, err := Files(name)
			if err == nil || files != nil {
				t.Fatalf("Files(%q) = %v, %v; want nil files and an error", name, files, err)
			}
		})
	}
}

func TestPackageFilesIncludeOptionalDirectoriesAndInstallModes(t *testing.T) {
	source := fstest.MapFS{
		"example/SKILL.md":                   {Data: []byte("skill"), Mode: 0o444},
		"example/agents/openai.yaml":         {Data: []byte("metadata"), Mode: 0o444},
		"example/references/nested.md":       {Data: []byte("overview"), Mode: 0o444},
		"example/references/nested/guide.md": {Data: []byte("guide"), Mode: 0o444},
		"example/scripts/check.sh":           {Data: []byte("#!/bin/sh\nexit 0\n"), Mode: 0o444},
		"example/assets/icon.png":            {Data: []byte{0x89, 0x50, 0x4e, 0x47}, Mode: 0o444},
		"other/SKILL.md":                     {Data: []byte("unrelated"), Mode: 0o444},
	}
	files, err := packageFiles(source, "example")
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"SKILL.md", "agents/openai.yaml", "assets/icon.png", "references/nested.md", "references/nested/guide.md", "scripts/check.sh"}
	if len(files) != len(wantPaths) {
		t.Fatalf("got %d files, want %d", len(files), len(wantPaths))
	}
	for i, file := range files {
		if file.Path != wantPaths[i] {
			t.Errorf("path = %q, want %q", file.Path, wantPaths[i])
		}
		wantMode := fs.FileMode(0o644)
		if file.Path == "scripts/check.sh" {
			wantMode = 0o755
		}
		if file.Mode != wantMode || !bytes.Equal(file.Content, source["example/"+file.Path].Data) {
			t.Errorf("file %s did not preserve bytes and install mode %o: %o", file.Path, wantMode, file.Mode)
		}
	}
}

func TestPackageFilesReportMissingPackage(t *testing.T) {
	files, err := packageFiles(fstest.MapFS{}, "missing")
	if files != nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("packageFiles = %v, %v; want nil files and missing-file error", files, err)
	}
}
