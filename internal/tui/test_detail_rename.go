package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/util"
)

const maxTUIResourceNameLen = 128

var reservedTUIResourceNames = map[string]bool{
	"run": true, "create": true, "delete": true, "open": true, "cancel": true,
	"list": true, "remote": true, "push": true, "pull": true, "diff": true, "rename": true,
	"validate": true, "setup": true, "help": true,
	"status": true, "history": true, "report": true, "share": true,
}

type testRenamePlan struct {
	Config                *config.ProjectConfig
	LocalTests            map[string]*config.LocalTest
	ConfigPath            string
	TestsDir              string
	AliasToRename         string
	ApplyLocalAliasRename bool
	LocalAlias            string
	ApplyLocalFileChanges bool
	DestFileAlias         string
}

func renameTestCmd(client *api.Client, testID, oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		newName = strings.TrimSpace(newName)
		if err := validateTUIResourceName(newName, "test"); err != nil {
			return TestRenamedMsg{Err: err}
		}

		remoteTest, err := client.GetTest(ctx, testID)
		if err != nil {
			return TestRenamedMsg{Err: fmt.Errorf("failed to load test: %w", err)}
		}

		plan, err := buildTestRenamePlan(testID, oldName, remoteTest.Name, newName)
		if err != nil {
			return TestRenamedMsg{Err: err}
		}

		testsResp, listErr := client.ListOrgTests(ctx, 200, 0)
		if listErr == nil && testsResp != nil {
			for _, t := range testsResp.Tests {
				if t.ID != testID && t.Name == newName {
					return TestRenamedMsg{Err: fmt.Errorf("a different test already uses name %q (id: %s)", newName, t.ID)}
				}
			}
		}

		var summary []string
		var warnings []string

		if remoteTest.Name != newName {
			_, err = client.UpdateTest(ctx, &api.UpdateTestRequest{
				TestID:          testID,
				Name:            newName,
				ExpectedVersion: remoteTest.Version,
			})
			if err != nil {
				return TestRenamedMsg{Err: fmt.Errorf("failed to rename test on remote: %w", err)}
			}
			summary = append(summary, fmt.Sprintf("Remote renamed: %s -> %s", remoteTest.Name, newName))
		} else {
			summary = append(summary, fmt.Sprintf("Remote test is already named \"%s\"", newName))
		}

		if plan.ApplyLocalAliasRename {
			plan.Config.Tests[newName] = testID
			delete(plan.Config.Tests, plan.AliasToRename)

			if err := os.MkdirAll(filepath.Dir(plan.ConfigPath), 0o755); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to prepare config directory: %v", err))
			} else if err := config.WriteProjectConfig(plan.ConfigPath, plan.Config); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to update .revyl/config.yaml: %v", err))
			} else {
				summary = append(summary, fmt.Sprintf("Updated local alias: %s -> %s", plan.AliasToRename, newName))
			}
		}

		if plan.ApplyLocalFileChanges && plan.LocalAlias != "" {
			local := plan.LocalTests[plan.LocalAlias]
			if local != nil {
				local.Test.Metadata.Name = newName

				sourcePath := filepath.Join(plan.TestsDir, plan.LocalAlias+".yaml")
				destPath := filepath.Join(plan.TestsDir, plan.DestFileAlias+".yaml")
				if err := os.MkdirAll(plan.TestsDir, 0o755); err != nil {
					warnings = append(warnings, fmt.Sprintf("failed to prepare .revyl/tests directory: %v", err))
				} else if err := config.SaveLocalTest(destPath, local); err != nil {
					warnings = append(warnings, fmt.Sprintf("failed to save local test file: %v", err))
				} else if sourcePath != destPath {
					if err := os.Remove(sourcePath); err != nil && !os.IsNotExist(err) {
						warnings = append(warnings, fmt.Sprintf("failed to remove old local file %s: %v", sourcePath, err))
					} else {
						summary = append(summary, fmt.Sprintf("Renamed local file: %s.yaml -> %s.yaml", plan.LocalAlias, plan.DestFileAlias))
					}
				} else {
					summary = append(summary, "Updated local test metadata name")
				}
			}
		}

		if !plan.ApplyLocalAliasRename && !plan.ApplyLocalFileChanges {
			summary = append(summary, "Local mappings/files unchanged")
		}

		for _, warning := range warnings {
			summary = append(summary, fmt.Sprintf("Warning: %s", warning))
		}

		return TestRenamedMsg{
			OldName: remoteTest.Name,
			NewName: newName,
			ID:      testID,
			Summary: strings.Join(summary, "\n"),
		}
	}
}

func buildTestRenamePlan(testID, oldNameOrID, remoteName, newName string) (*testRenamePlan, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	testsDir := filepath.Join(cwd, ".revyl", "tests")

	cfg, cfgErr := config.LoadProjectConfig(configPath)
	if cfgErr != nil {
		cfg = &config.ProjectConfig{}
	}
	if cfg.Tests == nil {
		cfg.Tests = make(map[string]string)
	}

	localTests, _ := config.LoadLocalTests(testsDir)
	if localTests == nil {
		localTests = make(map[string]*config.LocalTest)
	}

	aliasToRename, aliasAmbiguous := chooseAliasForTestRenameTUI(cfg.Tests, oldNameOrID, remoteName, testID)
	if aliasAmbiguous {
		matches := aliasesForRemoteIDTUI(cfg.Tests, testID)
		return nil, fmt.Errorf("multiple local aliases map to this test (%s)", strings.Join(matches, ", "))
	}

	localAlias, localAmbiguous := chooseLocalFileForTestRenameTUI(localTests, aliasToRename, oldNameOrID, remoteName, testID)
	if localAmbiguous {
		matches := localAliasesForRemoteIDTUI(localTests, testID)
		return nil, fmt.Errorf("multiple local files map to this test (%s)", strings.Join(matches, ", "))
	}

	plan := &testRenamePlan{
		Config:        cfg,
		LocalTests:    localTests,
		ConfigPath:    configPath,
		TestsDir:      testsDir,
		AliasToRename: aliasToRename,
		LocalAlias:    localAlias,
	}

	plan.ApplyLocalAliasRename = plan.AliasToRename != "" && plan.AliasToRename != newName
	plan.ApplyLocalFileChanges = plan.LocalAlias != ""

	if plan.ApplyLocalAliasRename {
		if existingID, exists := cfg.Tests[newName]; exists && existingID != testID {
			return nil, fmt.Errorf("local alias %q already points to a different test (%s)", newName, existingID)
		}
	}

	plan.DestFileAlias = plan.LocalAlias
	if plan.ApplyLocalFileChanges && (plan.ApplyLocalAliasRename || plan.AliasToRename == "") {
		plan.DestFileAlias = newName
	}

	if plan.ApplyLocalFileChanges && plan.DestFileAlias != "" {
		if existing, exists := localTests[plan.DestFileAlias]; exists && plan.DestFileAlias != plan.LocalAlias {
			if existing.Meta.RemoteID == "" || existing.Meta.RemoteID != testID {
				return nil, fmt.Errorf("local file already exists for %q at .revyl/tests/%s.yaml", plan.DestFileAlias, plan.DestFileAlias)
			}
		}
	}

	return plan, nil
}

func validateTUIResourceName(name, kind string) error {
	if name == "" {
		return fmt.Errorf("%s name cannot be empty", kind)
	}

	if len(name) > maxTUIResourceNameLen {
		return fmt.Errorf("%s name too long (%d chars, max %d)", kind, len(name), maxTUIResourceNameLen)
	}

	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("%s name cannot contain path separators; use a plain name (e.g. 'login-flow')", kind)
	}

	lower := strings.ToLower(name)
	for _, ext := range []string{".yaml", ".yml", ".json"} {
		if strings.HasSuffix(lower, ext) {
			return fmt.Errorf("%s name should not include a file extension (got %q)", kind, name)
		}
	}

	sanitized := util.SanitizeForFilename(name)
	if reservedTUIResourceNames[sanitized] {
		return fmt.Errorf("%q is a reserved command name and cannot be used as a %s name", name, kind)
	}

	return nil
}

func aliasesForRemoteIDTUI(testAliases map[string]string, testID string) []string {
	var aliases []string
	for alias, id := range testAliases {
		if id == testID {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	return aliases
}

func localAliasesForRemoteIDTUI(localTests map[string]*config.LocalTest, testID string) []string {
	var aliases []string
	for alias, lt := range localTests {
		if lt != nil && lt.Meta.RemoteID == testID {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	return aliases
}

func chooseAliasForTestRenameTUI(testAliases map[string]string, oldNameOrID, remoteName, testID string) (string, bool) {
	if len(testAliases) == 0 {
		return "", false
	}

	if id, ok := testAliases[oldNameOrID]; ok && id == testID {
		return oldNameOrID, false
	}
	if id, ok := testAliases[remoteName]; ok && id == testID {
		return remoteName, false
	}

	aliases := aliasesForRemoteIDTUI(testAliases, testID)
	if len(aliases) == 1 {
		return aliases[0], false
	}
	if len(aliases) > 1 {
		return "", true
	}
	return "", false
}

func chooseLocalFileForTestRenameTUI(localTests map[string]*config.LocalTest, aliasToRename, oldNameOrID, remoteName, testID string) (string, bool) {
	if len(localTests) == 0 {
		return "", false
	}

	if aliasToRename != "" {
		if lt, ok := localTests[aliasToRename]; ok {
			if lt.Meta.RemoteID == "" || lt.Meta.RemoteID == testID {
				return aliasToRename, false
			}
		}
	}

	for _, candidate := range []string{oldNameOrID, remoteName} {
		if candidate == "" {
			continue
		}
		lt, ok := localTests[candidate]
		if !ok {
			continue
		}
		if lt.Meta.RemoteID == testID {
			return candidate, false
		}
	}

	matches := localAliasesForRemoteIDTUI(localTests, testID)
	if len(matches) == 0 {
		return "", false
	}
	return matches[0], len(matches) > 1
}
