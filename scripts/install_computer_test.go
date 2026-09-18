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

type computerInstallerFixture struct {
	home       string
	tools      string
	installDir string
	binary     string
	checksums  string
	requests   string
	asset      string
	os         string
	arch       string
}

func newComputerInstallerFixture(t *testing.T, operatingSystem, architecture, asset string) computerInstallerFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the curl installer targets macOS and Linux")
	}
	directory := t.TempDir()
	fixture := computerInstallerFixture{
		home:       filepath.Join(directory, "home"),
		tools:      filepath.Join(directory, "tools"),
		installDir: filepath.Join(directory, "home", ".revyl", "bin"),
		binary:     filepath.Join(directory, "binary"),
		checksums:  filepath.Join(directory, "checksums.txt"),
		requests:   filepath.Join(directory, "requests"),
		asset:      asset,
		os:         operatingSystem,
		arch:       architecture,
	}
	for _, path := range []string{fixture.tools, fixture.installDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"awk", "chmod", "cp", "dirname", "grep", "mkdir", "mktemp", "mv", "rm", "sed"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(fixture.tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	checksumTool := "sha256sum"
	path, err := exec.LookPath(checksumTool)
	if err != nil {
		checksumTool = "shasum"
		path, err = exec.LookPath(checksumTool)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(path, filepath.Join(fixture.tools, checksumTool)); err != nil {
		t.Fatal(err)
	}
	binary := "#!/bin/sh\nprintf 'fixture revyl-computer\\n'\n"
	files := map[string]string{
		fixture.binary:    binary,
		fixture.checksums: fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(binary)), asset),
		filepath.Join(fixture.tools, "uname"): `#!/bin/sh
case "$1" in
    -s) printf '%s\n' "$FIXTURE_OS" ;;
    -m) printf '%s\n' "$FIXTURE_ARCH" ;;
    *) exit 1 ;;
esac
`,
		filepath.Join(fixture.tools, "curl"): `#!/bin/sh
set -eu
destination=
head=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output) destination=$2; shift 2 ;;
        --write-out|--proto|--proto-redir|--tlsv1.2|--connect-timeout|--max-time|--retry|--retry-max-time)
            if [ "$1" = --tlsv1.2 ]; then shift; else shift 2; fi ;;
        --head) head=1; shift ;;
        --fail|--silent|--show-error|--location) shift ;;
        https://*) url=$1; shift ;;
        *) exit 2 ;;
    esac
done
printf '%s\n' "$url" >> "$FIXTURE_REQUESTS"
if [ "$head" = 1 ]; then
    [ "$FIXTURE_FAIL" != latest ] || exit 22
    printf '%s' "$FIXTURE_RELEASE_URL"
elif [ "${url##*/}" = checksums.txt ]; then
    [ "$FIXTURE_FAIL" != checksums ] || exit 22
    cp "$FIXTURE_CHECKSUMS" "$destination"
else
    [ "$FIXTURE_FAIL" != binary ] || exit 22
    cp "$FIXTURE_BINARY" "$destination"
fi
`,
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture computerInstallerFixture) run(t *testing.T, overrides ...string) ([]byte, error) {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, shell, "install-computer.sh")
	command.Env = append(os.Environ(),
		"HOME="+fixture.home, "PATH="+fixture.tools, "SHELL=/bin/bash", "ZDOTDIR=",
		"REVYL_COMPUTER_VERSION=", "REVYL_COMPUTER_INSTALL_DIR=", "REVYL_COMPUTER_NO_MODIFY_PATH=0",
		"FIXTURE_OS="+fixture.os, "FIXTURE_ARCH="+fixture.arch,
		"FIXTURE_BINARY="+fixture.binary, "FIXTURE_CHECKSUMS="+fixture.checksums,
		"FIXTURE_REQUESTS="+fixture.requests, "FIXTURE_FAIL=",
		"FIXTURE_RELEASE_URL=https://github.com/RevylAI/revyl-cli/releases/tag/v1.2.3",
	)
	command.Env = append(command.Env, overrides...)
	return command.CombinedOutput()
}

func TestComputerInstallerSelectsPlatformAndInstalls(t *testing.T) {
	for _, platform := range []struct{ os, arch, asset string }{
		{"Darwin", "arm64", "revyl-computer-darwin-arm64"},
		{"Darwin", "x86_64", "revyl-computer-darwin-amd64"},
		{"Linux", "aarch64", "revyl-computer-linux-arm64"},
		{"Linux", "x86_64", "revyl-computer-linux-amd64"},
	} {
		t.Run(platform.asset, func(t *testing.T) {
			fixture := newComputerInstallerFixture(t, platform.os, platform.arch, platform.asset)
			existingRevyl := filepath.Join(fixture.installDir, "revyl")
			if err := os.WriteFile(existingRevyl, []byte("existing revyl"), 0o700); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if output, err := fixture.run(t); err != nil {
					t.Fatalf("install: %v\n%s", err, output)
				}
			}
			installed, err := os.ReadFile(filepath.Join(fixture.installDir, "revyl-computer"))
			if err != nil || !strings.Contains(string(installed), "fixture revyl-computer") {
				t.Fatalf("installed binary = %q, %v", installed, err)
			}
			info, err := os.Stat(filepath.Join(fixture.installDir, "revyl-computer"))
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("installed executable permissions: %v, %v", info, err)
			}
			profile, err := os.ReadFile(filepath.Join(fixture.home, ".bashrc"))
			if err != nil || strings.Count(string(profile), "export PATH=") != 1 {
				t.Fatalf("PATH update is not idempotent: %q, %v", profile, err)
			}
			requests, err := os.ReadFile(fixture.requests)
			if err != nil || !strings.Contains(string(requests), "/download/v1.2.3/"+platform.asset+"\n") {
				t.Fatalf("incorrect download: %s, %v", requests, err)
			}
			preserved, err := os.ReadFile(existingRevyl)
			if err != nil || string(preserved) != "existing revyl" {
				t.Fatalf("existing revyl was changed: %q, %v", preserved, err)
			}
			staging, _ := filepath.Glob(filepath.Join(fixture.installDir, ".revyl-computer-install.*"))
			if len(staging) != 0 {
				t.Fatalf("staging directories were not cleaned: %v", staging)
			}
		})
	}
}

func TestComputerInstallerFailsClosed(t *testing.T) {
	for _, testCase := range []string{"binary", "checksums", "latest", "mismatch", "missing", "duplicate", "invalid", "no-checksum-tool", "invalid-version", "multiline-version", "unexpected-release-url", "unsupported-os", "unsupported-arch", "symlink"} {
		t.Run(testCase, func(t *testing.T) {
			fixture := newComputerInstallerFixture(t, "Linux", "x86_64", "revyl-computer-linux-amd64")
			destination := filepath.Join(fixture.installDir, "revyl-computer")
			if err := os.WriteFile(destination, []byte("original"), 0o700); err != nil {
				t.Fatal(err)
			}
			overrides := []string{}
			checksums, err := os.ReadFile(fixture.checksums)
			if err != nil {
				t.Fatal(err)
			}
			switch testCase {
			case "binary", "checksums", "latest":
				overrides = append(overrides, "FIXTURE_FAIL="+testCase)
			case "mismatch":
				checksums = []byte(strings.Repeat("0", 64) + "  " + fixture.asset + "\n")
			case "missing":
				checksums = []byte(strings.ReplaceAll(string(checksums), fixture.asset, fixture.asset+".sig"))
			case "duplicate":
				checksums = append(checksums, checksums...)
			case "invalid":
				checksums = []byte("not-a-sha256  " + fixture.asset + "\n")
			case "no-checksum-tool":
				_ = os.Remove(filepath.Join(fixture.tools, "sha256sum"))
				_ = os.Remove(filepath.Join(fixture.tools, "shasum"))
			case "invalid-version":
				overrides = append(overrides, "REVYL_COMPUTER_VERSION=../../unsafe")
			case "multiline-version":
				overrides = append(overrides, "REVYL_COMPUTER_VERSION=v1.2.3\nunsafe")
			case "unexpected-release-url":
				overrides = append(overrides, "FIXTURE_RELEASE_URL=https://example.com/tag/v1.2.3")
			case "unsupported-os":
				overrides = append(overrides, "FIXTURE_OS=Windows")
			case "unsupported-arch":
				overrides = append(overrides, "FIXTURE_ARCH=i386")
			case "symlink":
				if err := os.Rename(destination, destination+"-target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination+"-target", destination); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(fixture.checksums, checksums, 0o600); err != nil {
				t.Fatal(err)
			}
			if output, err := fixture.run(t, overrides...); err == nil {
				t.Fatalf("unsafe installation succeeded: %s", output)
			}
			preserved, err := os.ReadFile(destination)
			if err != nil || string(preserved) != "original" {
				t.Fatalf("failed install changed existing binary: %q, %v", preserved, err)
			}
			if _, err := os.Stat(filepath.Join(fixture.home, ".bashrc")); !os.IsNotExist(err) {
				t.Fatalf("failed install changed the shell profile: %v", err)
			}
			staging, _ := filepath.Glob(filepath.Join(fixture.installDir, ".revyl-computer-install.*"))
			if len(staging) != 0 {
				t.Fatalf("failed install left staging directories: %v", staging)
			}
		})
	}
}

func TestComputerInstallerPinnedVersionAndPathOverrides(t *testing.T) {
	fixture := newComputerInstallerFixture(t, "Linux", "x86_64", "revyl-computer-linux-amd64")
	installDir := filepath.Join(fixture.home, "custom's $(echo untrusted) bin")
	output, err := fixture.run(t, "REVYL_COMPUTER_VERSION=1.2.3", "REVYL_COMPUTER_INSTALL_DIR="+installDir)
	if err != nil {
		t.Fatalf("install with overrides: %v\n%s", err, output)
	}
	requests, err := os.ReadFile(fixture.requests)
	if err != nil || strings.Contains(string(requests), "/latest") {
		t.Fatalf("pinned install resolved latest: %q, %v", requests, err)
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, shell, "-c", `. "$HOME/.bashrc"; command -v revyl-computer`)
	command.Env = append(os.Environ(), "HOME="+fixture.home, "PATH="+fixture.tools)
	resolved, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(resolved)) != filepath.Join(installDir, "revyl-computer") {
		t.Fatalf("profile did not quote the install directory safely: %q, %v", resolved, err)
	}
}

func TestComputerInstallerCanSkipProfileChanges(t *testing.T) {
	fixture := newComputerInstallerFixture(t, "Linux", "x86_64", "revyl-computer-linux-amd64")
	output, err := fixture.run(t, "REVYL_COMPUTER_NO_MODIFY_PATH=1")
	if err != nil || !strings.Contains(string(output), "export PATH=") {
		t.Fatalf("manual PATH setup: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(fixture.home, ".bashrc")); !os.IsNotExist(err) {
		t.Fatalf("installer changed the shell profile with the opt-out set: %v", err)
	}
}

func TestComputerInstallerPersistsInheritedPath(t *testing.T) {
	for _, skipProfile := range []bool{false, true} {
		t.Run(fmt.Sprintf("skip_profile=%t", skipProfile), func(t *testing.T) {
			fixture := newComputerInstallerFixture(t, "Linux", "x86_64", "revyl-computer-linux-amd64")
			overrides := []string{"PATH=" + fixture.installDir + string(os.PathListSeparator) + fixture.tools}
			if skipProfile {
				overrides = append(overrides, "REVYL_COMPUTER_NO_MODIFY_PATH=1")
			}
			for attempt := 0; attempt < 2; attempt++ {
				output, err := fixture.run(t, overrides...)
				if err != nil || !strings.Contains(string(output), "export PATH=") {
					t.Fatalf("install with inherited PATH: %v\n%s", err, output)
				}
			}
			profile, err := os.ReadFile(filepath.Join(fixture.home, ".bashrc"))
			if skipProfile {
				if !os.IsNotExist(err) {
					t.Fatalf("installer changed the shell profile with the opt-out set: %q, %v", profile, err)
				}
				return
			}
			if err != nil || strings.Count(string(profile), "export PATH=") != 1 {
				t.Fatalf("inherited PATH was not persisted exactly once: %q, %v", profile, err)
			}
			shell, err := exec.LookPath("sh")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, shell, "-c", `. "$HOME/.bashrc"; command -v revyl-computer`)
			command.Env = append(os.Environ(), "HOME="+fixture.home, "PATH="+fixture.tools)
			resolved, err := command.CombinedOutput()
			if err != nil || strings.TrimSpace(string(resolved)) != filepath.Join(fixture.installDir, "revyl-computer") {
				t.Fatalf("new shell did not resolve the installed binary: %q, %v", resolved, err)
			}
		})
	}
}

func TestComputerInstallerShellProfiles(t *testing.T) {
	for _, testCase := range []struct {
		shell   string
		profile string
		prefix  string
	}{
		{"/bin/bash", ".bash_profile", "export PATH="},
		{"/bin/zsh", "zsh/.zshrc", "export PATH="},
		{"/bin/fish", ".config/fish/config.fish", "set -gx PATH "},
		{"/bin/sh", ".profile", "export PATH="},
	} {
		t.Run(testCase.shell, func(t *testing.T) {
			fixture := newComputerInstallerFixture(t, "Linux", "x86_64", "revyl-computer-linux-amd64")
			profile := filepath.Join(fixture.home, testCase.profile)
			if err := os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(profile, []byte("existing-profile\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				output, err := fixture.run(t, "SHELL="+testCase.shell, "ZDOTDIR="+filepath.Join(fixture.home, "zsh"))
				if err != nil {
					t.Fatalf("install: %v\n%s", err, output)
				}
			}
			contents, err := os.ReadFile(profile)
			if err != nil || !strings.HasPrefix(string(contents), "existing-profile\n") || strings.Count(string(contents), testCase.prefix) != 1 {
				t.Fatalf("profile was overwritten or duplicated: %q, %v", contents, err)
			}
		})
	}
}

func TestComputerInstallerUsesShasumFallback(t *testing.T) {
	fixture := newComputerInstallerFixture(t, "Darwin", "arm64", "revyl-computer-darwin-arm64")
	shasum, err := exec.LookPath("shasum")
	if err != nil {
		t.Skip("shasum is not installed")
	}
	_ = os.Remove(filepath.Join(fixture.tools, "sha256sum"))
	_ = os.Remove(filepath.Join(fixture.tools, "shasum"))
	if err := os.Symlink(shasum, filepath.Join(fixture.tools, "shasum")); err != nil {
		t.Fatal(err)
	}
	if output, err := fixture.run(t); err != nil {
		t.Fatalf("shasum fallback: %v\n%s", err, output)
	}
}
