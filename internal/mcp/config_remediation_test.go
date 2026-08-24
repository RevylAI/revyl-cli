package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/config"
)

func TestDevModeProjectRemediationCommandsRemainCustomerFacing(t *testing.T) {
	prepareServerAuthTest(t)
	projectDirectory := os.Getenv("REVYL_PROJECT_DIR")
	initializeMCPGitFixture(t, projectDirectory)
	t.Setenv("REVYL_API_KEY", "dev-remediation-key")
	server, err := NewServer("test", true, WithProfile(ProfileCore))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}

	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	canonicalProjectDirectory := canonicalMCPFixturePath(t, projectDirectory)
	if status.Remediation == nil ||
		status.Remediation.Command != revylRemediationCommand("-C", canonicalProjectDirectory, "config", "pull") ||
		status.Remediation.AlternativeCommand != revylRemediationCommand("-C", canonicalProjectDirectory, "init", "--non-interactive") {
		t.Fatalf("dev remediation = %+v", status.Remediation)
	}

	configDirectory := filepath.Join(projectDirectory, ".revyl")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	configPath := filepath.Join(configDirectory, "config.yaml")
	if err := os.WriteFile(configPath, []byte("project:\n  name: legacy\n"), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	legacy := resolveSetupProjectStateForMode(projectDirectory, true)
	if legacy.Remediation == nil ||
		legacy.Remediation.CheckCommand != revylRemediationCommand("-C", canonicalProjectDirectory, "config", "migrate", "--check", "--json") ||
		legacy.Remediation.ApplyCommand != revylRemediationCommand("-C", canonicalProjectDirectory, "config", "migrate", "--write") {
		t.Fatalf("dev legacy remediation = %+v", legacy.Remediation)
	}

	if err := os.WriteFile(configPath, []byte("project: [invalid"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	invalid := resolveSetupProjectStateForMode(projectDirectory, true)
	if invalid.Remediation == nil || invalid.Remediation.CheckCommand != revylRemediationCommand("-C", canonicalProjectDirectory, "config", "validate") {
		t.Fatalf("dev invalid remediation = %+v", invalid.Remediation)
	}
}

func TestLegacyProjectConfigRemediationProvidesCheckAndApplyCommands(t *testing.T) {
	projectDirectory := t.TempDir()
	initializeMCPGitFixture(t, projectDirectory)
	configDirectory := filepath.Join(projectDirectory, ".revyl")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	configPath := filepath.Join(configDirectory, "config.yaml")
	legacyConfig := []byte("project:\n  name: legacy\nbuild:\n  system: Xcode\n  platforms:\n    ios:\n      command: build\n      output: app.app\n")
	if err := os.WriteFile(configPath, legacyConfig, 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	status := resolveSetupProjectState(projectDirectory)
	canonicalProjectDirectory := canonicalMCPFixturePath(t, projectDirectory)
	checkCommand := revylRemediationCommand("-C", canonicalProjectDirectory, "config", "migrate", "--check", "--json")
	applyCommand := revylRemediationCommand("-C", canonicalProjectDirectory, "config", "migrate", "--write")
	if status.State != projectStateLegacy || status.ProjectDirectory != canonicalProjectDirectory {
		t.Fatalf("legacy status = %+v", status)
	}
	if status.Remediation == nil ||
		status.Remediation.ActionKind != remediationActionRepairProjectConfig ||
		status.Remediation.Command != checkCommand ||
		status.Remediation.CheckCommand != checkCommand ||
		status.Remediation.ApplyCommand != applyCommand ||
		status.Remediation.ConfigPath != filepath.Join(canonicalProjectDirectory, ".revyl", "config.yaml") ||
		!strings.Contains(status.Message, checkCommand) ||
		!strings.Contains(status.Message, applyCommand) {
		t.Fatalf("legacy remediation = %+v, message = %q", status.Remediation, status.Message)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read legacy config: %v", err)
	}
	if !slices.Equal(after, legacyConfig) {
		t.Fatal("legacy setup inspection changed config bytes")
	}
}

func TestMixedProjectConfigUsesLegacyMigrationRecovery(t *testing.T) {
	projectDirectory := t.TempDir()
	initializeMCPGitFixture(t, projectDirectory)
	configDirectory := filepath.Join(projectDirectory, ".revyl")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.yaml"),
		[]byte("project:\n  id: 11111111-1111-4111-8111-111111111111\n  name: legacy-name\n"),
		0o600,
	); err != nil {
		t.Fatalf("write mixed config: %v", err)
	}

	status := resolveSetupProjectState(projectDirectory)
	if status.State != projectStateLegacy || status.Remediation == nil ||
		status.Remediation.CheckCommand == "" || status.Remediation.ApplyCommand == "" {
		t.Fatalf("mixed config status = %+v", status)
	}
}

func TestProjectConfigRemediationDistinguishesWorkspaceStates(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		projectDirectory := t.TempDir()
		initializeMCPGitFixture(t, projectDirectory)
		status := resolveSetupProjectState(projectDirectory)
		canonicalProjectDirectory := canonicalMCPFixturePath(t, projectDirectory)
		if status.State != projectStateNotInitialized || status.Remediation == nil ||
			status.Remediation.ActionKind != remediationActionCommand ||
			status.Remediation.Command != revylRemediationCommand("-C", canonicalProjectDirectory, "config", "pull") ||
			status.Remediation.AlternativeCommand != revylRemediationCommand("-C", canonicalProjectDirectory, "init", "--non-interactive") {
			t.Fatalf("missing status = %+v", status)
		}
	})

	t.Run("outside git", func(t *testing.T) {
		workDir := t.TempDir()
		status := resolveSetupProjectState(workDir)
		if status.State != projectStateOutsideGit || status.Remediation == nil ||
			status.Remediation.ActionKind != remediationActionSelectProjectDir ||
			status.Remediation.EnvName != "REVYL_PROJECT_DIR" ||
			status.Remediation.Command != "" {
			t.Fatalf("outside-git status = %+v", status)
		}
	})

	t.Run("nested", func(t *testing.T) {
		workDir := t.TempDir()
		initializeMCPGitFixture(t, workDir)
		nestedProject := filepath.Join(workDir, "apps", "ios")
		writeDevLoopConfigAt(t, nestedProject)
		status := resolveSetupProjectState(workDir)
		if status.State != projectStateNested ||
			status.ProjectDirectory != canonicalMCPFixturePath(t, nestedProject) ||
			status.Remediation != nil {
			t.Fatalf("nested status = %+v", status)
		}
	})

	t.Run("non-directory marker does not hide nested project", func(t *testing.T) {
		workDir := t.TempDir()
		initializeMCPGitFixture(t, workDir)
		containerDirectory := filepath.Join(workDir, "apps", "ios")
		if err := os.MkdirAll(containerDirectory, 0o755); err != nil {
			t.Fatalf("create nested container: %v", err)
		}
		if err := os.WriteFile(filepath.Join(containerDirectory, ".revyl"), []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("write non-directory marker: %v", err)
		}
		nestedProject := filepath.Join(containerDirectory, "project")
		writeDevLoopConfigAt(t, nestedProject)
		status := resolveSetupProjectState(workDir)
		if status.State != projectStateNested ||
			status.ProjectDirectory != canonicalMCPFixturePath(t, nestedProject) {
			t.Fatalf("nested status = %+v", status)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		workDir := t.TempDir()
		initializeMCPGitFixture(t, workDir)
		first := filepath.Join(workDir, "apps", "android")
		second := filepath.Join(workDir, "apps", "ios")
		writeDevLoopConfigAt(t, second)
		writeDevLoopConfigAt(t, first)
		status := resolveSetupProjectState(workDir)
		wantRoots := []string{
			canonicalMCPFixturePath(t, first),
			canonicalMCPFixturePath(t, second),
		}
		if status.State != projectStateAmbiguous || status.Remediation == nil ||
			status.Remediation.ActionKind != remediationActionSelectProjectDir ||
			!slices.Equal(status.Remediation.CandidateRoots, wantRoots) {
			t.Fatalf("ambiguous status = %+v, want roots %v", status, wantRoots)
		}
	})

	t.Run("child Git repository is excluded", func(t *testing.T) {
		workDir := t.TempDir()
		initializeMCPGitFixture(t, workDir)
		childRepository := filepath.Join(workDir, "apps", "child")
		initializeMCPGitFixture(t, childRepository)
		writeDevLoopConfigAt(t, childRepository)

		status := resolveSetupProjectState(workDir)
		if status.State != projectStateNotInitialized {
			t.Fatalf("child-repository status = %+v, want not initialized", status)
		}
	})

	t.Run("linked worktree is excluded", func(t *testing.T) {
		workDir := t.TempDir()
		initializeMCPGitFixture(t, workDir)
		sourceRepository := t.TempDir()
		initializeMCPGitFixture(t, sourceRepository)
		if err := os.WriteFile(filepath.Join(sourceRepository, "README.md"), []byte("fixture\n"), 0o600); err != nil {
			t.Fatalf("write linked-worktree fixture: %v", err)
		}
		for _, command := range [][]string{
			{"git", "-C", sourceRepository, "add", "README.md"},
			{"git", "-C", sourceRepository, "-c", "user.name=Revyl Test", "-c", "user.email=test@revyl.invalid", "commit", "--quiet", "-m", "fixture"},
		} {
			if output, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("run %v: %v: %s", command, err, output)
			}
		}
		linkedWorktree := filepath.Join(workDir, "apps", "linked")
		if output, err := exec.Command("git", "-C", sourceRepository, "worktree", "add", "--quiet", "--detach", linkedWorktree, "HEAD").CombinedOutput(); err != nil {
			t.Fatalf("create linked worktree: %v: %s", err, output)
		}
		writeDevLoopConfigAt(t, linkedWorktree)

		status := resolveSetupProjectState(workDir)
		if status.State != projectStateNotInitialized {
			t.Fatalf("linked-worktree status = %+v, want not initialized", status)
		}
	})
}

func TestOptionalCanonicalProjectReResolvesUniqueNestedProject(t *testing.T) {
	workDir := t.TempDir()
	initializeMCPGitFixture(t, workDir)
	nestedProject := filepath.Join(workDir, "apps", "ios")
	writeDevLoopConfigAt(t, nestedProject)
	testsDirectory := filepath.Join(nestedProject, ".revyl", "tests")
	if err := os.MkdirAll(testsDirectory, 0o755); err != nil {
		t.Fatalf("create tests directory: %v", err)
	}
	const remoteID = "44444444-4444-4444-8444-444444444444"
	if err := os.WriteFile(
		filepath.Join(testsDirectory, "login.yaml"),
		[]byte("_meta:\n  remote_id: "+remoteID+"\ntest:\n  metadata:\n    name: login\n"),
		0o600,
	); err != nil {
		t.Fatalf("write local test: %v", err)
	}
	_, projectErr := config.ResolveProjectContext(workDir, "")
	server := &Server{workDir: canonicalMCPFixturePath(t, workDir), projectErr: projectErr}

	project, err := server.resolveOptionalCanonicalProject()
	if err != nil {
		t.Fatalf("resolve optional canonical project: %v", err)
	}
	if project == nil || project.ProjectRoot != canonicalMCPFixturePath(t, nestedProject) {
		t.Fatalf("resolved project = %+v", project)
	}
	resolvedID, err := server.localTestRemoteID("login")
	if err != nil || resolvedID != remoteID {
		t.Fatalf("resolved local test = %q, %v", resolvedID, err)
	}
}

func TestOptionalCanonicalProjectPreservesConfiglessOperation(t *testing.T) {
	workDir := t.TempDir()
	initializeMCPGitFixture(t, workDir)
	_, projectErr := config.ResolveProjectContext(workDir, "")
	server := &Server{workDir: canonicalMCPFixturePath(t, workDir), projectErr: projectErr}

	project, err := server.resolveOptionalCanonicalProject()
	if err != nil || project != nil {
		t.Fatalf("configless project = %+v, %v", project, err)
	}
}

func TestProjectConfigRemediationQuotesWorkspacePath(t *testing.T) {
	projectDirectory := filepath.Join(t.TempDir(), "Project Files")
	initializeMCPGitFixture(t, projectDirectory)
	canonicalProjectDirectory := canonicalMCPFixturePath(t, projectDirectory)
	quotedProjectDirectory := "'" + canonicalProjectDirectory + "'"
	if runtime.GOOS == "windows" {
		quotedProjectDirectory = `"` + canonicalProjectDirectory + `"`
	}
	status := resolveSetupProjectState(projectDirectory)
	wantCommand := "revyl -C " + quotedProjectDirectory + " config pull"
	wantAlternative := "revyl -C " + quotedProjectDirectory + " init --non-interactive"
	if status.Remediation == nil || status.Remediation.Command != wantCommand || status.Remediation.AlternativeCommand != wantAlternative {
		t.Fatalf("project remediation = %+v, want commands %q / %q", status.Remediation, wantCommand, wantAlternative)
	}
}

func TestCoreAndFullProfilesReturnStructuredLegacyConfigRecovery(t *testing.T) {
	for _, profile := range []Profile{ProfileCore, ProfileFull} {
		t.Run(string(profile), func(t *testing.T) {
			prepareServerAuthTest(t)
			projectDirectory := os.Getenv("REVYL_PROJECT_DIR")
			initializeMCPGitFixture(t, projectDirectory)
			configDirectory := filepath.Join(projectDirectory, ".revyl")
			if err := os.MkdirAll(configDirectory, 0o755); err != nil {
				t.Fatalf("create config directory: %v", err)
			}
			if err := os.WriteFile(
				filepath.Join(configDirectory, "config.yaml"),
				[]byte("project:\n  name: legacy\nbuild:\n  system: Xcode\n"),
				0o600,
			); err != nil {
				t.Fatalf("write legacy config: %v", err)
			}
			t.Setenv("REVYL_API_KEY", "structured-recovery-key")
			runner := &fakeDevLoopRunner{}
			server, err := NewServer(
				"test",
				false,
				WithProfile(profile),
				WithDevLoopRunner(runner),
			)
			if err != nil {
				t.Fatalf("NewServer(): %v", err)
			}

			serverToolByName(t, listServerTools(t, server), "setup_status")
			setup := decodeStructuredToolResult[SetupStatusOutput](
				t,
				callServerTool(t, server, "setup_status", nil),
			)
			if setup.ProjectState != projectStateLegacy || setup.Remediation == nil ||
				setup.Remediation.CheckCommand == "" || setup.Remediation.ApplyCommand == "" {
				t.Fatalf("setup status = %+v", setup)
			}

			startResult := callServerTool(t, server, "start_dev_loop", nil)
			if !startResult.IsError {
				t.Fatalf("start result = %+v, want setup error", startResult)
			}
			start := decodeStructuredToolResult[StartDevLoopCoreOutput](t, startResult)
			if start.Outcome.OutcomeCode != "project_legacy_config" || start.Remediation == nil ||
				start.Error == "" || strings.Contains(start.Error, "classification: legacy_config_requires_migration") {
				t.Fatalf("start output = %+v", start)
			}
			requireRemediationParity(t, start.Remediation, setup.Remediation)
			requireRunnerStartCalls(t, runner, 0)

			listResult := callServerTool(t, server, "manage_tests", map[string]any{"action": "list"})
			if !listResult.IsError {
				t.Fatalf("manage_tests result = %+v, want setup error", listResult)
			}
			composite := decodeStructuredToolResult[CompositeOutput](t, listResult)
			encoded, err := json.Marshal(composite.Result)
			if err != nil {
				t.Fatalf("marshal composite result: %v", err)
			}
			resultText := string(encoded)
			for _, required := range []string{"project_legacy_config", "check_command", "apply_command"} {
				if !strings.Contains(resultText, required) {
					t.Fatalf("manage_tests result missing %q: %s", required, resultText)
				}
			}
			if strings.Contains(resultText, "classification: legacy_config_requires_migration") {
				t.Fatalf("manage_tests returned raw ConfigError: %s", resultText)
			}

			assertCompositeRecovery := func(toolResult *mcpsdk.CallToolResult, composite CompositeOutput, callErr error, tool, action string) {
				t.Helper()
				if callErr != nil {
					t.Fatalf("%s/%s call error: %v", tool, action, callErr)
				}
				if toolResult == nil || !toolResult.IsError {
					t.Fatalf("%s/%s result = %+v, want setup error", tool, action, toolResult)
				}
				encoded, err := json.Marshal(composite.Result)
				if err != nil {
					t.Fatalf("marshal %s/%s result: %v", tool, action, err)
				}
				if !strings.Contains(string(encoded), "project_legacy_config") || !strings.Contains(string(encoded), "remediation") {
					t.Fatalf("%s/%s recovery = %s", tool, action, encoded)
				}
			}

			for _, action := range []struct {
				tool   string
				action string
				params map[string]any
			}{
				{tool: "manage_tests", action: "delete", params: map[string]any{"test_name_or_id": "login"}},
				{tool: "manage_tests", action: "update", params: map[string]any{"test_name_or_id": "login", "yaml_content": "test: {}"}},
			} {
				encodedParams, err := json.Marshal(action.params)
				if err != nil {
					t.Fatalf("marshal %s/%s params: %v", action.tool, action.action, err)
				}
				toolResult, composite, callErr := server.handleManageTests(
					context.Background(),
					nil,
					CompositeInput{Action: action.action, ActionParams: encodedParams},
				)
				assertCompositeRecovery(toolResult, composite, callErr, action.tool, action.action)
			}

			if profile == ProfileFull {
				for _, action := range []struct {
					tool   string
					action string
					params map[string]any
				}{
					{tool: "manage_tags", action: "get_test_tags", params: map[string]any{"test_name_or_id": "login"}},
					{tool: "manage_variables", action: "list_vars", params: map[string]any{"test_name_or_id": "login"}},
				} {
					encodedParams, err := json.Marshal(action.params)
					if err != nil {
						t.Fatalf("marshal %s/%s params: %v", action.tool, action.action, err)
					}
					input := CompositeInput{Action: action.action, ActionParams: encodedParams}
					if action.tool == "manage_tags" {
						toolResult, composite, callErr := server.handleManageTags(context.Background(), nil, input)
						assertCompositeRecovery(toolResult, composite, callErr, action.tool, action.action)
					} else {
						toolResult, composite, callErr := server.handleManageVariables(context.Background(), nil, input)
						assertCompositeRecovery(toolResult, composite, callErr, action.tool, action.action)
					}
				}
			}
		})
	}
}

func TestLegacyFlatProjectHandlersPreserveOutputCompatibility(t *testing.T) {
	projectDirectory := t.TempDir()
	initializeMCPGitFixture(t, projectDirectory)
	configDirectory := filepath.Join(projectDirectory, ".revyl")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config.yaml"), []byte("project:\n  name: legacy\n"), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	_, projectErr := config.ResolveProjectContext(projectDirectory, "")
	server := &Server{
		workDir:    canonicalMCPFixturePath(t, projectDirectory),
		projectErr: projectErr,
	}

	_, output, err := server.handleDeleteTest(context.Background(), nil, DeleteTestInput{TestNameOrID: "login"})
	if err != nil {
		var setupErr *projectSetupError
		if errors.As(err, &setupErr) {
			t.Fatalf("legacy flat handler propagated setup error: %v", err)
		}
		t.Fatalf("legacy flat handler error: %v", err)
	}
	if output.Success || !strings.Contains(output.Error, "config migrate") {
		t.Fatalf("legacy flat output = %+v", output)
	}
}
