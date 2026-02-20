// Package orgguard provides project/auth organization mismatch detection.
package orgguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
)

// ConfigRelPath is the project config path relative to the working directory.
const ConfigRelPath = ".revyl/config.yaml"

// CheckResult captures org mismatch check context and outcome.
type CheckResult struct {
	ConfigPath   string
	ConfigExists bool
	ConfigParsed bool
	ProjectOrgID string
	AuthOrgID    string
	Mismatch     *MismatchError
}

// MismatchError indicates that project org binding differs from the current auth org.
type MismatchError struct {
	ProjectOrgID string
	AuthOrgID    string
	ConfigPath   string
}

// Error returns a user-facing mismatch message.
func (e *MismatchError) Error() string {
	return e.UserMessage()
}

// UserMessage returns a standardized mismatch message used by CLI and MCP.
func (e *MismatchError) UserMessage() string {
	return fmt.Sprintf(
		"Project is bound to %q, current login is %q. Test/workflow-scoped operations are blocked until this is resolved.\nConfig: %s\nFix: run 'revyl auth login' with the correct account, or rebind this project with 'revyl init'.",
		e.ProjectOrgID,
		e.AuthOrgID,
		e.ConfigPath,
	)
}

// Check resolves org mismatch state for the given working directory.
//
// The check is intentionally best-effort and non-blocking by default:
//   - Missing config, parse failures, missing project org binding, missing auth token,
//     or auth-org lookup failures all return a non-mismatch result.
func Check(ctx context.Context, cwd string, devMode bool) *CheckResult {
	result := &CheckResult{}

	if strings.TrimSpace(cwd) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return result
		}
		cwd = wd
	}

	cwd = filepath.Clean(cwd)
	configPath := filepath.Join(cwd, ConfigRelPath)
	result.ConfigPath = configPath

	if _, err := os.Stat(configPath); err != nil {
		return result
	}
	result.ConfigExists = true

	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		return result
	}
	result.ConfigParsed = true

	projectOrgID := strings.TrimSpace(cfg.Project.OrgID)
	result.ProjectOrgID = projectOrgID
	if projectOrgID == "" {
		return result
	}

	mgr := auth.NewManager()
	token, err := mgr.GetActiveToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return result
	}

	client := api.NewClientWithDevMode(token, devMode)
	userInfo, err := client.ValidateAPIKey(ctx)
	if err != nil || userInfo == nil {
		return result
	}

	authOrgID := strings.TrimSpace(userInfo.OrgID)
	result.AuthOrgID = authOrgID
	if authOrgID == "" {
		return result
	}

	if authOrgID != projectOrgID {
		result.Mismatch = &MismatchError{
			ProjectOrgID: projectOrgID,
			AuthOrgID:    authOrgID,
			ConfigPath:   configPath,
		}
	}

	return result
}
