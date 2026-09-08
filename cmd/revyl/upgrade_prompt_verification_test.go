package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestVerifyInstalledUpgradeRejectsInvalidAndPrereleaseVersions(t *testing.T) {
	for _, test := range []struct {
		name      string
		installed string
		expected  string
		wantError bool
	}{
		{name: "prerelease is not stable", installed: "v1.2.3-rc.1", expected: "v1.2.3", wantError: true},
		{name: "older prerelease", installed: "1.2.3-rc.1", expected: "1.2.3-rc.2", wantError: true},
		{name: "stable after prerelease", installed: "1.2.3", expected: "1.2.3-rc.1"},
		{name: "build metadata", installed: "1.2.3+build.4", expected: "1.2.3"},
		{name: "invalid installed version", installed: "1.2.3.invalid", expected: "1.2.0", wantError: true},
		{name: "invalid target", installed: "1.2.3", expected: "invalid", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("REVYL_UPGRADE_VERSION_HELPER", "1")
			t.Setenv("REVYL_UPGRADE_VERSION_OUTPUT", fmt.Sprintf(`{"version":%q}`, test.installed))
			previous := upgradeVerificationCommand
			t.Cleanup(func() { upgradeVerificationCommand = previous })
			upgradeVerificationCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUpgradeVersionHelper$")
			}
			actual, err := verifyInstalledUpgrade(context.Background(), "installed-revyl", test.expected)
			if (err != nil) != test.wantError {
				t.Fatalf("verification = %q, %v", actual, err)
			}
		})
	}
}

type blockedUpgradeOutput struct {
	started chan struct{}
	release chan struct{}
}

func (output *blockedUpgradeOutput) Write(data []byte) (int, error) {
	close(output.started)
	<-output.release
	return len(data), nil
}

func TestUpgradeProcessCancellationBoundsOutputWait(t *testing.T) {
	output := &blockedUpgradeOutput{started: make(chan struct{}), release: make(chan struct{})}
	defer close(output.release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBlockedUpgradeOutputHelper$")
	cmd.Env = append(os.Environ(), "REVYL_UPGRADE_BLOCKED_OUTPUT_HELPER=1")
	cmd.Stdout = output
	result := make(chan error, 1)
	go func() { result <- runUpgradeProcess(ctx, cmd) }()
	select {
	case <-output.started:
	case <-time.After(10 * time.Second):
		t.Fatal("updater did not write output")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("updater cancellation waited indefinitely for output")
	}
}

func TestBlockedUpgradeOutputHelper(t *testing.T) {
	if os.Getenv("REVYL_UPGRADE_BLOCKED_OUTPUT_HELPER") != "1" {
		return
	}
	fmt.Print("upgrading")
	time.Sleep(time.Minute)
	os.Exit(0)
}
