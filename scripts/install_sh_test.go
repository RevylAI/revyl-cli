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

type cliInstallerFixture struct {
	home     string
	tools    string
	binary   string
	sums     string
	requests string
}

func newCLIInstallerFixture(t *testing.T, asset string) cliInstallerFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh runs under a POSIX shell; Windows hosts are covered by faking uname")
	}
	directory := t.TempDir()
	fixture := cliInstallerFixture{
		home:     filepath.Join(directory, "home"),
		tools:    filepath.Join(directory, "tools"),
		binary:   filepath.Join(directory, "binary"),
		sums:     filepath.Join(directory, "checksums.txt"),
		requests: filepath.Join(directory, "requests"),
	}
	for _, path := range []string{fixture.home, fixture.tools} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"awk", "basename", "chmod", "cp", "dirname", "grep", "mkdir", "mktemp", "mv", "rm", "sed", "tr"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(fixture.tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"sha256sum", "shasum"} {
		if path, err := exec.LookPath(name); err == nil {
			if err := os.Symlink(path, filepath.Join(fixture.tools, name)); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	binary := "#!/bin/sh\nprintf 'fixture revyl\\n'\n"
	files := map[string]string{
		fixture.binary: binary,
		fixture.sums:   fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(binary)), asset),
		filepath.Join(fixture.tools, "uname"): `#!/bin/sh
case "$1" in
    -s) printf '%s\n' "$FIXTURE_OS" ;;
    -m) printf '%s\n' "$FIXTURE_ARCH" ;;
    *) exit 1 ;;
esac
`,
		filepath.Join(fixture.tools, "curl"): `#!/bin/sh
destination=
url=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) destination=$2; shift 2 ;;
        -*) shift ;;
        *) url=$1; shift ;;
    esac
done
printf '%s\n' "$url" >> "$FIXTURE_REQUESTS"
case "$url" in
    */checksums.txt) cp "$FIXTURE_CHECKSUMS" "$destination" ;;
    *) cp "$FIXTURE_BINARY" "$destination" ;;
esac
`,
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture cliInstallerFixture) run(t *testing.T, operatingSystem, architecture string, overrides ...string) ([]byte, error) {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, shell, "install.sh")
	command.Env = append(os.Environ(),
		"HOME="+fixture.home, "PATH="+fixture.tools, "SHELL=/bin/bash",
		"REVYL_VERSION=v1.2.3", "REVYL_INSTALL_DIR=", "REVYL_NO_MODIFY_PATH=0",
		"FIXTURE_OS="+operatingSystem, "FIXTURE_ARCH="+architecture,
		"FIXTURE_BINARY="+fixture.binary, "FIXTURE_CHECKSUMS="+fixture.sums,
		"FIXTURE_REQUESTS="+fixture.requests,
	)
	command.Env = append(command.Env, overrides...)
	return command.CombinedOutput()
}

func TestCLIInstallerInstallsWindowsExecutableUnderPOSIXLayers(t *testing.T) {
	for _, testCase := range []struct{ uname, arch, asset string }{
		{"MINGW64_NT-10.0-22631", "x86_64", "revyl-windows-amd64.exe"},
		{"MSYS_NT-10.0-22631", "x86_64", "revyl-windows-amd64.exe"},
		{"CYGWIN_NT-10.0-22631", "aarch64", "revyl-windows-arm64.exe"},
	} {
		t.Run(testCase.uname, func(t *testing.T) {
			fixture := newCLIInstallerFixture(t, testCase.asset)

			output, err := fixture.run(t, testCase.uname, testCase.arch)

			if err != nil {
				t.Fatalf("install: %v\n%s", err, output)
			}
			installed := filepath.Join(fixture.home, ".revyl", "bin", "revyl.exe")
			if contents, err := os.ReadFile(installed); err != nil || !strings.Contains(string(contents), "fixture revyl") {
				t.Fatalf("installed executable = %q, %v", contents, err)
			}
			requests, err := os.ReadFile(fixture.requests)
			if err != nil || !strings.Contains(string(requests), "/download/v1.2.3/"+testCase.asset+"\n") {
				t.Fatalf("incorrect download: %s, %v", requests, err)
			}
			for _, want := range []string{"Checksum verified", "Git Bash: open a new Git Bash window", "PowerShell and cmd: add"} {
				if !strings.Contains(string(output), want) {
					t.Fatalf("output missing %q:\n%s", want, output)
				}
			}
		})
	}
}

func TestCLIInstallerRejectsUnknownOperatingSystemWithPackageAlternatives(t *testing.T) {
	fixture := newCLIInstallerFixture(t, "revyl-plan9-amd64")

	output, err := fixture.run(t, "Plan9", "x86_64")

	if err == nil {
		t.Fatalf("unsupported OS unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "uv tool install revyl") {
		t.Fatalf("unsupported OS error lacks alternatives:\n%s", output)
	}
}

func TestCLIInstallerSucceedsWhenShellProfileIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file write permissions")
	}
	fixture := newCLIInstallerFixture(t, "revyl-linux-amd64")
	profile := filepath.Join(fixture.home, ".bashrc")
	if err := os.WriteFile(profile, []byte("managed-by-nix\n"), 0o400); err != nil {
		t.Fatal(err)
	}

	output, err := fixture.run(t, "Linux", "x86_64")

	if err != nil {
		t.Fatalf("install with read-only profile failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(fixture.home, ".revyl", "bin", "revyl")); err != nil {
		t.Fatalf("binary was not installed: %v", err)
	}
	installDir := filepath.Join(fixture.home, ".revyl", "bin")
	for _, want := range []string{"Could not update " + profile, "export PATH=\"" + installDir + ":$PATH\"", "REVYL_NO_MODIFY_PATH=1", "installed successfully"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if contents, err := os.ReadFile(profile); err != nil || string(contents) != "managed-by-nix\n" {
		t.Fatalf("read-only profile changed: %q, %v", contents, err)
	}
}
