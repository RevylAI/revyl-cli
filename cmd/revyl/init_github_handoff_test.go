package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/spf13/cobra"
)

func TestRunInitHandsWrittenCanonicalConfigToGithubSetup(t *testing.T) {
	resetInitGlobals(t)

	originalAuthenticationWizard := initAuthenticationWizard
	originalGithubContinuation := continueInitWithGithub
	t.Cleanup(func() {
		initAuthenticationWizard = originalAuthenticationWizard
		continueInitWithGithub = originalGithubContinuation
	})

	client := &api.Client{}
	initAuthenticationWizard = func(context.Context, bool, string) (*api.Client, *api.ValidateAPIKeyResponse, bool) {
		return client, &api.ValidateAPIKeyResponse{Email: "developer@example.com"}, true
	}

	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)
	configPath := filepath.Join(workDir, ".revyl", "config.yaml")
	handoffErr := errors.New("github setup stopped")
	handoffCalled := false
	continueInitWithGithub = func(cmd *cobra.Command, gotClient *api.Client) error {
		handoffCalled = true
		if cmd == nil {
			t.Fatal("GitHub setup received a nil command")
		}
		if gotClient != client {
			t.Fatalf("GitHub setup client = %p, want authenticated client %p", gotClient, client)
		}
		written, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("ReadFile(config.yaml) before GitHub setup error = %v", err)
		}
		authored, err := config.ParseAuthoredConfig(written)
		if err != nil {
			t.Fatalf("config handed to GitHub setup is not canonical: %v\n%s", err, written)
		}
		if authored.Project.ID == "" {
			t.Fatal("config handed to GitHub setup has no project ID")
		}
		return handoffErr
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("dev", false, "")
	output := captureStdoutAndStderr(t, func() {
		withStdin(t, "1\n4\n2\n", func() {
			err := runInit(cmd, nil)
			if !errors.Is(err, handoffErr) {
				t.Fatalf("runInit() error = %v, want GitHub setup error", err)
			}
		})
	})

	if !handoffCalled {
		t.Fatal("runInit() did not continue into GitHub setup")
	}
	if !strings.Contains(output, "Configure GitHub pull request automation") {
		t.Fatalf("init output did not offer GitHub setup:\n%s", output)
	}
	if !strings.Contains(output, "GitHub Pull Request Automation") {
		t.Fatalf("init output did not announce the GitHub setup handoff:\n%s", output)
	}
}

func TestRunInitKeepsGithubSetupForGitlabOrigin(t *testing.T) {
	resetInitGlobals(t)

	originalAuthenticationWizard := initAuthenticationWizard
	originalGithubContinuation := continueInitWithGithub
	t.Cleanup(func() {
		initAuthenticationWizard = originalAuthenticationWizard
		continueInitWithGithub = originalGithubContinuation
	})

	client := &api.Client{}
	initAuthenticationWizard = func(context.Context, bool, string) (*api.Client, *api.ValidateAPIKeyResponse, bool) {
		return client, &api.ValidateAPIKeyResponse{Email: "developer@example.com"}, true
	}

	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	gitContext, cancelGit := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelGit()
	if err := exec.CommandContext(gitContext, "git", "-C", workDir, "remote", "add", "origin", "git@gitlab.com:group/mobile.git").Run(); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, workDir)
	configPath := filepath.Join(workDir, ".revyl", "config.yaml")
	handoffErr := errors.New("github setup stopped")
	handoffCalled := false
	continueInitWithGithub = func(cmd *cobra.Command, gotClient *api.Client) error {
		handoffCalled = true
		if cmd == nil {
			t.Fatal("GitHub setup received a nil command")
		}
		if gotClient != client {
			t.Fatalf("GitHub setup client = %p, want authenticated client %p", gotClient, client)
		}
		written, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("ReadFile(config.yaml) before GitHub setup error = %v", err)
		}
		authored, err := config.ParseAuthoredConfig(written)
		if err != nil {
			t.Fatalf("config handed to GitHub setup is not canonical: %v\n%s", err, written)
		}
		if authored.Project.ID == "" {
			t.Fatal("config handed to GitHub setup has no project ID")
		}
		return handoffErr
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("dev", false, "")
	output := captureStdoutAndStderr(t, func() {
		withStdin(t, "1\n4\n2\n", func() {
			err := runInit(cmd, nil)
			if !errors.Is(err, handoffErr) {
				t.Fatalf("runInit() error = %v, want GitHub setup error", err)
			}
		})
	})

	if strings.Contains(output, "Configure GitLab") || strings.Contains(output, "GitLab Merge Request Automation") {
		t.Fatalf("init offered GitLab setup:\n%s", output)
	}
	if !handoffCalled {
		t.Fatal("runInit() did not continue into GitHub setup")
	}
	if !strings.Contains(output, "Configure GitHub pull request automation") {
		t.Fatalf("init output did not offer GitHub setup:\n%s", output)
	}
	if !strings.Contains(output, "GitHub Pull Request Automation") {
		t.Fatalf("init output did not announce the GitHub setup handoff:\n%s", output)
	}
}
