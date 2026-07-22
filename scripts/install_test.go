package scripts_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type environmentOverride struct {
	name  string
	value string
}

// TestInstallerScriptContract protects the public overrides and fail-closed primitives.
func TestInstallerScriptContract(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(installerPath(t))
	if err != nil {
		t.Fatalf("read install.ps1: %v", err)
	}

	requiredFragments := []string{
		"$env:REVYL_VERSION",
		"$env:REVYL_INSTALL_DIR",
		"$env:REVYL_NO_MODIFY_PATH",
		`"revyl-windows-$Architecture.exe"`,
		"checksums.txt",
		"Confirm-RevylChecksum",
		"[IO.File]::Replace",
		"[Net.SecurityProtocolType]::Tls12",
		`SetEnvironmentVariable("Path", $Value, "User")`,
	}
	for _, fragment := range requiredFragments {
		if !strings.Contains(string(contents), fragment) {
			t.Errorf("install.ps1 is missing required fragment %q", fragment)
		}
	}
}

// TestInstallerSelectsWindowsAssets verifies amd64 and arm64 release selection.
func TestInstallerSelectsWindowsAssets(t *testing.T) {
	powerShell := requirePowerShell(t)

	testCases := []struct {
		name                  string
		nativeArchitecture    string
		processArchitecture   string
		expectedArchitecture  string
		expectedAssetFilename string
	}{
		{
			name:                  "amd64",
			processArchitecture:   "AMD64",
			expectedArchitecture:  "amd64",
			expectedAssetFilename: "revyl-windows-amd64.exe",
		},
		{
			name:                  "arm64 native process",
			processArchitecture:   "ARM64",
			expectedArchitecture:  "arm64",
			expectedAssetFilename: "revyl-windows-arm64.exe",
		},
		{
			name:                  "arm64 compatibility layer",
			nativeArchitecture:    "ARM64",
			processArchitecture:   "AMD64",
			expectedArchitecture:  "arm64",
			expectedAssetFilename: "revyl-windows-arm64.exe",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			installDirectory := t.TempDir()
			command := fmt.Sprintf(`
$architecture = Get-RevylArchitecture -NativeArchitecture %s -ProcessArchitecture %s
$plan = New-RevylInstallPlan -Version 'v1.2.3' -InstallDirectory %s -Architecture $architecture
Write-Output "$architecture|$($plan.AssetName)"
`,
				quotePowerShell(testCase.nativeArchitecture),
				quotePowerShell(testCase.processArchitecture),
				quotePowerShell(installDirectory),
			)

			output, err := runLoadedPowerShell(t, powerShell, command)
			if err != nil {
				t.Fatalf("select asset: %v\n%s", err, output)
			}

			expected := testCase.expectedArchitecture + "|" + testCase.expectedAssetFilename
			if actual := strings.TrimSpace(output); actual != expected {
				t.Fatalf("asset selection = %q, want %q", actual, expected)
			}
		})
	}

	output, err := runLoadedPowerShell(
		t,
		powerShell,
		"Get-RevylArchitecture -NativeArchitecture '' -ProcessArchitecture 'x86'",
	)
	if err == nil {
		t.Fatalf("unsupported x86 architecture unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "Unsupported Windows architecture") {
		t.Fatalf("unsupported architecture error was not actionable: %s", output)
	}
}

// TestInstallerResolvesPinnedAndLatestVersions verifies override and latest seams.
func TestInstallerResolvesPinnedAndLatestVersions(t *testing.T) {
	powerShell := requirePowerShell(t)

	output, err := runLoadedPowerShell(t, powerShell, `
$resolved = Resolve-RevylVersion -ConfiguredVersion 'v1.2.3' -LatestReleaseFetcher { throw 'must not run' }
Write-Output $resolved
`)
	if err != nil {
		t.Fatalf("resolve pinned version: %v\n%s", err, output)
	}
	if actual := strings.TrimSpace(output); actual != "v1.2.3" {
		t.Fatalf("pinned version = %q, want v1.2.3", actual)
	}

	output, err = runLoadedPowerShell(t, powerShell, `
$resolved = Resolve-RevylVersion -ConfiguredVersion '' -LatestReleaseFetcher { return 'v9.8.7' }
Write-Output $resolved
`)
	if err != nil {
		t.Fatalf("resolve latest version: %v\n%s", err, output)
	}
	if actual := strings.TrimSpace(output); actual != "v9.8.7" {
		t.Fatalf("latest version = %q, want v9.8.7", actual)
	}

	output, err = runLoadedPowerShell(
		t,
		powerShell,
		"Resolve-RevylVersion -ConfiguredVersion '../unsafe' -LatestReleaseFetcher { throw 'must not run' }",
	)
	if err == nil {
		t.Fatalf("unsafe version unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "Invalid Revyl version") {
		t.Fatalf("unsafe version error was not actionable: %s", output)
	}
}

// TestInstallerChecksumVerification verifies exact-entry and digest enforcement.
func TestInstallerChecksumVerification(t *testing.T) {
	powerShell := requirePowerShell(t)
	temporaryDirectory := t.TempDir()
	binaryPath := filepath.Join(temporaryDirectory, "revyl-windows-amd64.exe")
	checksumsPath := filepath.Join(temporaryDirectory, "checksums.txt")
	binaryContents := []byte("verified-revyl-binary")
	if err := os.WriteFile(binaryPath, binaryContents, 0o600); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}

	expectedHash := fmt.Sprintf("%x", sha256.Sum256(binaryContents))
	validChecksums := fmt.Sprintf(
		"%s  unrelated-file\n%s  revyl-windows-amd64.exe\n",
		strings.Repeat("0", 64),
		expectedHash,
	)
	if err := os.WriteFile(checksumsPath, []byte(validChecksums), 0o600); err != nil {
		t.Fatalf("write checksum fixture: %v", err)
	}

	command := fmt.Sprintf(`
Confirm-RevylChecksum -BinaryPath %s -ChecksumsPath %s -AssetName 'revyl-windows-amd64.exe'
Write-Output 'verified'
`, quotePowerShell(binaryPath), quotePowerShell(checksumsPath))
	output, err := runLoadedPowerShell(t, powerShell, command)
	if err != nil {
		t.Fatalf("verify checksum: %v\n%s", err, output)
	}
	if actual := strings.TrimSpace(output); actual != "verified" {
		t.Fatalf("checksum result = %q, want verified", actual)
	}

	if err := os.WriteFile(
		checksumsPath,
		[]byte(strings.Repeat("0", 64)+"  revyl-windows-amd64.exe\n"),
		0o600,
	); err != nil {
		t.Fatalf("write mismatch fixture: %v", err)
	}
	output, err = runLoadedPowerShell(t, powerShell, command)
	if err == nil {
		t.Fatalf("checksum mismatch unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "Checksum mismatch") {
		t.Fatalf("checksum mismatch error was not actionable: %s", output)
	}

	if err := os.WriteFile(
		checksumsPath,
		[]byte(expectedHash+"  another-file.exe\n"),
		0o600,
	); err != nil {
		t.Fatalf("write missing-entry fixture: %v", err)
	}
	output, err = runLoadedPowerShell(t, powerShell, command)
	if err == nil {
		t.Fatalf("missing checksum entry unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "does not contain an entry") {
		t.Fatalf("missing checksum error was not actionable: %s", output)
	}
}

// TestInstallerFailsWhenChecksumsUnavailable verifies fail-closed orchestration.
func TestInstallerFailsWhenChecksumsUnavailable(t *testing.T) {
	powerShell := requirePowerShell(t)
	installDirectory := filepath.Join(t.TempDir(), "install")

	command := fmt.Sprintf(`
$script:downloadCount = 0
$downloadFile = {
    param([uri] $Uri, [string] $Destination)
    $script:downloadCount++
    if ($script:downloadCount -eq 1) {
        [IO.File]::WriteAllText($Destination, 'downloaded-binary')
        return
    }
    throw 'checksums unavailable'
}
$updateUserPath = {
    throw 'PATH updater must not run'
}
Invoke-RevylInstaller -RequestedVersion 'v1.2.3' -RequestedInstallDirectory %s -SkipPathModification -DownloadFile $downloadFile -UpdateUserPath $updateUserPath
`, quotePowerShell(installDirectory))

	output, err := runLoadedPowerShell(t, powerShell, command)
	if err == nil {
		t.Fatalf("missing checksums unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "Could not download checksums.txt") {
		t.Fatalf("missing checksums error was not actionable: %s", output)
	}
	if _, statErr := os.Stat(filepath.Join(installDirectory, "revyl.exe")); !os.IsNotExist(statErr) {
		t.Fatalf("unverified binary was installed; stat error: %v", statErr)
	}
}

// TestInstallerReplacesBinaryAtomically verifies replacement and staging cleanup.
func TestInstallerReplacesBinaryAtomically(t *testing.T) {
	powerShell := requirePowerShell(t)
	temporaryDirectory := t.TempDir()
	sourcePath := filepath.Join(temporaryDirectory, "download.exe")
	installDirectory := filepath.Join(temporaryDirectory, "install")
	destinationPath := filepath.Join(installDirectory, "revyl.exe")

	if err := os.MkdirAll(installDirectory, 0o700); err != nil {
		t.Fatalf("create install fixture: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("new-binary"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	if err := os.WriteFile(destinationPath, []byte("old-binary"), 0o600); err != nil {
		t.Fatalf("write destination fixture: %v", err)
	}

	command := fmt.Sprintf(
		"Install-RevylBinaryAtomically -SourcePath %s -InstallDirectory %s | Out-Null",
		quotePowerShell(sourcePath),
		quotePowerShell(installDirectory),
	)
	output, err := runLoadedPowerShell(t, powerShell, command)
	if err != nil {
		t.Fatalf("replace binary: %v\n%s", err, output)
	}

	installedContents, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if actual := string(installedContents); actual != "new-binary" {
		t.Fatalf("installed contents = %q, want new-binary", actual)
	}

	stagedFiles, err := filepath.Glob(filepath.Join(installDirectory, ".revyl.exe.*.tmp"))
	if err != nil {
		t.Fatalf("find staged files: %v", err)
	}
	if len(stagedFiles) != 0 {
		t.Fatalf("staged files were not cleaned: %v", stagedFiles)
	}
}

// TestInstallerUserPathUpdateIsIdempotent verifies duplicate and no-modify behavior.
func TestInstallerUserPathUpdateIsIdempotent(t *testing.T) {
	powerShell := requirePowerShell(t)

	output, err := runLoadedPowerShell(t, powerShell, `
$update = Set-RevylUserPath -InstallDirectory 'C:\Users\Example\.revyl\bin' -ReadUserPath { 'C:\Windows;C:\Users\EXAMPLE\.revyl\bin\' } -WriteUserPath { throw 'writer must not run' }
Write-Output "$($update.AlreadyPresent)|$($update.ShouldModify)|$($update.Reason)"
`)
	if err != nil {
		t.Fatalf("calculate idempotent PATH update: %v\n%s", err, output)
	}
	if actual := strings.TrimSpace(output); actual != "True|False|already_present" {
		t.Fatalf("existing PATH result = %q", actual)
	}

	output, err = runLoadedPowerShell(t, powerShell, `
$update = Set-RevylUserPath -InstallDirectory 'C:\Users\Example\.revyl\bin' -NoModifyPath -ReadUserPath { 'C:\Windows' } -WriteUserPath { throw 'writer must not run' }
Write-Output "$($update.AlreadyPresent)|$($update.ShouldModify)|$($update.Reason)"
`)
	if err != nil {
		t.Fatalf("calculate no-modify PATH update: %v\n%s", err, output)
	}
	if actual := strings.TrimSpace(output); actual != "False|False|modification_disabled" {
		t.Fatalf("no-modify PATH result = %q", actual)
	}

	output, err = runLoadedPowerShell(t, powerShell, `
$update = Get-RevylUserPathUpdate -InstallDirectory 'C:\Users\Example\.revyl\bin' -CurrentUserPath 'C:\Windows'
Write-Output "$($update.ShouldModify)|$($update.Reason)|$($update.UpdatedPath)"
`)
	if err != nil {
		t.Fatalf("calculate PATH addition: %v\n%s", err, output)
	}
	expected := `True|added|C:\Windows;C:\Users\Example\.revyl\bin`
	if actual := strings.TrimSpace(output); actual != expected {
		t.Fatalf("PATH addition = %q, want %q", actual, expected)
	}
}

// TestInstallerDryRunUsesEnvironmentOverrides verifies the public environment contract.
func TestInstallerDryRunUsesEnvironmentOverrides(t *testing.T) {
	powerShell := requirePowerShell(t)
	installDirectory := filepath.Join(t.TempDir(), "dry-run-install")
	// PowerShell GetFullPath expands Windows 8.3 names; match that form in assertions.
	expectedInstallDirectory := expandPowerShellPath(t, powerShell, installDirectory)

	command := exec.Command(
		powerShell,
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		installerPath(t),
		"-DryRun",
	)
	command.Env = environmentWithOverrides(
		environmentOverride{name: "PROCESSOR_ARCHITEW6432", value: "AMD64"},
		environmentOverride{name: "PROCESSOR_ARCHITECTURE", value: "AMD64"},
		environmentOverride{name: "REVYL_VERSION", value: "v7.8.9"},
		environmentOverride{name: "REVYL_INSTALL_DIR", value: installDirectory},
		environmentOverride{name: "REVYL_NO_MODIFY_PATH", value: "1"},
		environmentOverride{name: "REVYL_INSTALLER_NO_RUN", value: ""},
	)

	outputBytes, err := command.CombinedOutput()
	output := string(outputBytes)
	if err != nil {
		t.Fatalf("run installer dry-run: %v\n%s", err, output)
	}

	for _, expected := range []string{
		"Version:  v7.8.9",
		"Asset:    revyl-windows-amd64.exe",
		"Install:  " + expectedInstallDirectory,
		"PATH:     unchanged",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("dry-run output is missing %q:\n%s", expected, output)
		}
	}

	if _, statErr := os.Stat(installDirectory); !os.IsNotExist(statErr) {
		t.Fatalf("dry-run created the install directory; stat error: %v", statErr)
	}
}

// installerPath returns the absolute path to install.ps1.
func installerPath(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine install_test.go path")
	}
	return filepath.Join(filepath.Dir(currentFile), "install.ps1")
}

// requirePowerShell finds a test-capable PowerShell executable.
func requirePowerShell(t *testing.T) string {
	t.Helper()

	candidates := []string{"pwsh", "powershell"}
	if runtime.GOOS == "windows" {
		candidates = []string{"pwsh.exe", "powershell.exe"}
	}
	for _, candidate := range candidates {
		if executablePath, err := exec.LookPath(candidate); err == nil {
			return executablePath
		}
	}

	if runtime.GOOS == "windows" {
		t.Fatal("PowerShell is required for Windows installer tests")
	}
	t.Skip("PowerShell is unavailable on this host; behavioral tests run in Windows CI")
	return ""
}

// runLoadedPowerShell loads install.ps1 without executing main, then runs a test command.
func runLoadedPowerShell(
	t *testing.T,
	powerShell string,
	testCommand string,
) (string, error) {
	t.Helper()

	commandText := fmt.Sprintf(
		"$env:REVYL_INSTALLER_NO_RUN='1'; . %s; %s",
		quotePowerShell(installerPath(t)),
		testCommand,
	)
	command := exec.Command(
		powerShell,
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-Command",
		commandText,
	)
	command.Env = environmentWithOverrides(
		environmentOverride{name: "PROCESSOR_ARCHITEW6432", value: "AMD64"},
		environmentOverride{name: "PROCESSOR_ARCHITECTURE", value: "AMD64"},
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

// quotePowerShell returns a single-quoted PowerShell string literal.
func quotePowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// expandPowerShellPath returns the path form produced by [IO.Path]::GetFullPath.
func expandPowerShellPath(t *testing.T, powerShell, path string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return path
	}
	command := exec.Command(
		powerShell,
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-Command",
		"[IO.Path]::GetFullPath("+quotePowerShell(path)+")",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("expand path via PowerShell: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

// environmentWithOverrides returns the process environment with exact replacements.
func environmentWithOverrides(overrides ...environmentOverride) []string {
	environment := os.Environ()
	for _, override := range overrides {
		replaced := false
		for index, entry := range environment {
			name, _, found := strings.Cut(entry, "=")
			if found && strings.EqualFold(name, override.name) {
				environment[index] = override.name + "=" + override.value
				replaced = true
				break
			}
		}
		if !replaced {
			environment = append(environment, override.name+"="+override.value)
		}
	}
	return environment
}
