package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRemoteDevStartFlags(t *testing.T) {
	oldPlatform := devStartPlatform
	oldNoBuild := devStartNoBuild
	oldBuildVersionID := devStartBuildVerID
	oldTunnel := devStartTunnelURL
	defer func() {
		devStartPlatform = oldPlatform
		devStartNoBuild = oldNoBuild
		devStartBuildVerID = oldBuildVersionID
		devStartTunnelURL = oldTunnel
	}()

	tests := []struct {
		name    string
		setup   func()
		wantErr string
	}{
		{
			name: "ios valid",
			setup: func() {
				devStartPlatform = "ios"
				devStartNoBuild = false
				devStartBuildVerID = ""
				devStartTunnelURL = ""
			},
		},
		{
			name: "android rejected",
			setup: func() {
				devStartPlatform = "android"
				devStartNoBuild = false
				devStartBuildVerID = ""
				devStartTunnelURL = ""
			},
			wantErr: "supports iOS only",
		},
		{
			name: "no build rejected",
			setup: func() {
				devStartPlatform = "ios"
				devStartNoBuild = true
				devStartBuildVerID = ""
				devStartTunnelURL = ""
			},
			wantErr: "--no-build",
		},
		{
			name: "build version rejected",
			setup: func() {
				devStartPlatform = "ios"
				devStartNoBuild = false
				devStartBuildVerID = "bv_123"
				devStartTunnelURL = ""
			},
			wantErr: "--build-version-id",
		},
		{
			name: "tunnel rejected",
			setup: func() {
				devStartPlatform = "ios"
				devStartNoBuild = false
				devStartBuildVerID = ""
				devStartTunnelURL = "https://example.ngrok.app"
			},
			wantErr: "--tunnel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			err := validateRemoteDevStartFlags()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRemoteDevStartFlags() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateRemoteDevStartFlags() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCreateSourceArchiveIncludingWorkingTree(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")

	if err := os.MkdirAll(filepath.Join(dir, "SwiftMinimal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, ".gitignore"), "build/\nignored.txt\n")
	writeFile(t, filepath.Join(dir, "SwiftMinimal", "ContentView.swift"), "old marker\n")
	runGit(t, dir, "add", ".gitignore", "SwiftMinimal/ContentView.swift")

	writeFile(t, filepath.Join(dir, "SwiftMinimal", "ContentView.swift"), "dirty marker\n")
	writeFile(t, filepath.Join(dir, "SwiftMinimal", "NewView.swift"), "new file\n")
	writeFile(t, filepath.Join(dir, "build", "ignored.o"), "generated\n")
	writeFile(t, filepath.Join(dir, "ignored.txt"), "ignored\n")

	archivePath, err := createSourceArchiveIncludingWorkingTree(dir)
	if err != nil {
		t.Fatalf("createSourceArchiveIncludingWorkingTree() error = %v", err)
	}
	defer os.Remove(archivePath)

	files := readTarGz(t, archivePath)
	if got := files["SwiftMinimal/ContentView.swift"]; got != "dirty marker\n" {
		t.Fatalf("ContentView.swift = %q, want dirty working-tree content", got)
	}
	if got := files["SwiftMinimal/NewView.swift"]; got != "new file\n" {
		t.Fatalf("NewView.swift = %q, want untracked unignored file", got)
	}
	if _, ok := files["build/ignored.o"]; ok {
		t.Fatal("archive included ignored build artifact")
	}
	if _, ok := files["ignored.txt"]; ok {
		t.Fatal("archive included ignored file")
	}
}

func TestCreateSourceArchiveIncludingWorkingTree_FallsBackForIgnoredSandbox(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	writeFile(t, filepath.Join(dir, ".gitignore"), "sandbox/\n")

	sandbox := filepath.Join(dir, "sandbox")
	if err := os.MkdirAll(filepath.Join(sandbox, "SwiftMinimal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sandbox, ".revyl", "dev-sessions"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sandbox, "build"), 0755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(sandbox, ".revyl", "config.yaml"), "project:\n  name: sandbox\n")
	writeFile(t, filepath.Join(sandbox, ".revyl", ".dev-status.json"), "{}\n")
	writeFile(t, filepath.Join(sandbox, ".revyl", "dev-sessions", "default.json"), "{}\n")
	writeFile(t, filepath.Join(sandbox, "SwiftMinimal", "ContentView.swift"), "standalone source\n")
	writeFile(t, filepath.Join(sandbox, "build", "generated.o"), "generated\n")

	archivePath, err := createSourceArchiveIncludingWorkingTree(sandbox)
	if err != nil {
		t.Fatalf("createSourceArchiveIncludingWorkingTree() error = %v", err)
	}
	defer os.Remove(archivePath)

	files := readTarGz(t, archivePath)
	if got := files["SwiftMinimal/ContentView.swift"]; got != "standalone source\n" {
		t.Fatalf("ContentView.swift = %q, want standalone source", got)
	}
	if got := files[".revyl/config.yaml"]; got != "project:\n  name: sandbox\n" {
		t.Fatalf(".revyl/config.yaml = %q, want config included", got)
	}
	if _, ok := files[".revyl/.dev-status.json"]; ok {
		t.Fatal("archive included dev status runtime file")
	}
	if _, ok := files[".revyl/dev-sessions/default.json"]; ok {
		t.Fatal("archive included dev session runtime file")
	}
	if _, ok := files["build/generated.o"]; ok {
		t.Fatal("archive included generated build output")
	}
}

func TestRevylRemoteDevloopTemplateShape(t *testing.T) {
	root := filepath.Clean("../../../revyl-remote-devloop")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			t.Skip("revyl-remote-devloop is a local ignored sandbox template")
		}
		t.Fatalf("failed to inspect remote devloop template: %v", err)
	}
	required := []string{
		"README.md",
		".gitignore",
		".revyl/config.yaml",
		"SwiftMinimal.xcodeproj/project.pbxproj",
		"SwiftMinimal/ContentView.swift",
		"SwiftMinimal/SwiftMinimalApp.swift",
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("required template file %s missing: %v", rel, err)
		}
	}

	configData, err := os.ReadFile(filepath.Join(root, ".revyl/config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configText := string(configData)
	for _, want := range []string{"xcodebuild", "iphonesimulator", "SwiftMinimal.app"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}

	ignoreData, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	ignoreText := string(ignoreData)
	for _, want := range []string{"build/", "DerivedData/"} {
		if !strings.Contains(ignoreText, want) {
			t.Fatalf(".gitignore missing %q:\n%s", want, ignoreText)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readTarGz(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		header, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(data)
	}
	return files
}
