package scripts_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildAllPackagesBothCLIs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("build-all is a POSIX shell script")
	}
	for _, failBuild := range []bool{false, true} {
		t.Run(fmt.Sprintf("failBuild=%t", failBuild), func(t *testing.T) {
			projectDir := t.TempDir()
			for _, directory := range []string{"scripts", "tools"} {
				if err := os.Mkdir(filepath.Join(projectDir, directory), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			script, err := os.ReadFile("build-all.sh")
			if err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"scripts/build-all.sh": string(script),
				"VERSION":              "1.2.3\n",
				"tools/go": `#!/bin/bash
set -euo pipefail
test "$1" = build
shift
test "$1" = -ldflags
flags="$2"
shift 2
test "$1" = -o
output="$2"
command="$3"
if [ "$FAIL_BUILD" = true ] && [ "$command" = ./cmd/revyl-computer ]; then
    exit 1
fi
printf '%s\n' "$GOOS/$GOARCH cgo=$CGO_ENABLED $flags $command" > "$output"
`,
			}
			for name, contents := range files {
				if err := os.WriteFile(filepath.Join(projectDir, name), []byte(contents), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash", filepath.Join(projectDir, "scripts", "build-all.sh"))
			command.Env = append(os.Environ(),
				"PATH="+filepath.Join(projectDir, "tools")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"VERSION=", "COMMIT=test-commit", "DATE=2026-01-01T00:00:00Z",
				fmt.Sprintf("FAIL_BUILD=%t", failBuild),
			)
			output, err := command.CombinedOutput()
			checksumPath := filepath.Join(projectDir, "build", "checksums.txt")
			if failBuild {
				if err == nil {
					t.Fatalf("build-all succeeded after a failed build: %s", output)
				}
				if _, err := os.Stat(checksumPath); !os.IsNotExist(err) {
					t.Fatalf("failed build generated checksums: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("build-all: %v\n%s", err, output)
			}
			checksums, err := os.ReadFile(checksumPath)
			if err != nil {
				t.Fatal(err)
			}
			if lines := strings.Count(string(checksums), "\n"); lines != 12 {
				t.Fatalf("checksum entries = %d, want 12", lines)
			}
			for _, platform := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"} {
				for _, binary := range []string{"revyl", "revyl-computer"} {
					name := binary + "-" + strings.ReplaceAll(platform, "/", "-")
					if strings.HasPrefix(platform, "windows/") {
						name += ".exe"
					}
					contents, err := os.ReadFile(filepath.Join(projectDir, "build", name))
					if err != nil {
						t.Fatal(err)
					}
					want := platform + " cgo=0 -X main.version=1.2.3 -X main.commit=test-commit -X main.date=2026-01-01T00:00:00Z ./cmd/" + binary + "\n"
					if string(contents) != want {
						t.Errorf("%s build invocation = %q, want %q", name, contents, want)
					}
					checksumLine := fmt.Sprintf("%x  %s\n", sha256.Sum256(contents), name)
					if !strings.Contains(string(checksums), checksumLine) {
						t.Errorf("missing checksum for %s", name)
					}
				}
			}
		})
	}
}

func TestMakefilePackagesBothCLIs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local Makefile requires POSIX tooling")
	}
	for _, target := range []string{"build", "install", "check"} {
		t.Run(target, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "make", "--no-print-directory", "-n", target,
				"VERSION=1.2.3", "COMMIT=test-commit", "DATE=2026-01-01T00:00:00Z")
			command.Dir = ".."
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("make %s: %v\n%s", target, err, output)
			}
			for _, binary := range []string{"revyl", "revyl-computer"} {
				found := false
				for _, argument := range strings.Fields(string(output)) {
					if strings.TrimSuffix(argument, "/...") == "./cmd/"+binary {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("make %s does not include %s: %s", target, binary, output)
				}
			}
			if target != "check" && !strings.Contains(string(output), "-X main.version=1.2.3 -X main.commit=test-commit -X main.date=2026-01-01T00:00:00Z") {
				t.Errorf("make %s does not include build metadata: %s", target, output)
			}
		})
	}
}
