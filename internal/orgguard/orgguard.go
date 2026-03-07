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

const resolveCreateOrgIDHint = "run 'revyl auth login' to refresh credentials or 'revyl init' to bind this project"

// ResolveCreateOrgID determines which org_id should be sent when creating tests.
//
// Resolution order:
//   - project.org_id from .revyl/config.yaml
//   - live org_id from ValidateAPIKey using the active token/client
//   - file-backed credentials org_id from ~/.revyl/credentials.json
//
// This helper is intentionally separate from mismatch enforcement. Callers that
// already block on mismatches should keep doing so; this only resolves the org
// to include in create requests and returns an actionable error when no org can
// be determined.
func ResolveCreateOrgID(ctx context.Context, client *api.Client, cfg *config.ProjectConfig) (string, error) {
	if cfg != nil {
		if orgID := strings.TrimSpace(cfg.Project.OrgID); orgID != "" {
			return orgID, nil
		}
	}

	if client == nil {
		mgr := auth.NewManager()
		if creds, err := mgr.GetFileCredentials(); err == nil && creds != nil {
			if orgID := strings.TrimSpace(creds.OrgID); orgID != "" {
				return orgID, nil
			}
		}
		return "", fmt.Errorf("could not resolve organization ID for test creation; %s", resolveCreateOrgIDHint)
	}

	userInfo, err := client.ValidateAPIKey(ctx)
	if err == nil && userInfo != nil {
		orgID := strings.TrimSpace(userInfo.OrgID)
		if orgID != "" {
			return orgID, nil
		}
	}

	mgr := auth.NewManager()
	if creds, credsErr := mgr.GetFileCredentials(); credsErr == nil && creds != nil {
		if orgID := strings.TrimSpace(creds.OrgID); orgID != "" {
			return orgID, nil
		}
	}

	if err != nil {
		return "", fmt.Errorf("could not resolve organization ID for test creation: %v; %s", err, resolveCreateOrgIDHint)
	}
	if userInfo == nil {
		return "", fmt.Errorf("could not resolve organization ID for test creation: empty auth response; %s", resolveCreateOrgIDHint)
	}
	return "", fmt.Errorf("could not resolve organization ID for test creation: organization ID missing from authenticated session; %s", resolveCreateOrgIDHint)
}
