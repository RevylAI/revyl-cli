// Package orgguard provides project/auth organization mismatch detection.
package orgguard

import (
	"context"
	"fmt"
	"os"
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

// Check resolves the canonical project for the given working directory.
// Canonical project files intentionally have no client-authored organization
// binding; project ownership is enforced by the server using project.id.
// The legacy mismatch result therefore remains empty while source-compatible
// callers transition away from this pre-canonical helper.
func Check(_ context.Context, cwd string, _ bool) *CheckResult {
	result := &CheckResult{}

	if strings.TrimSpace(cwd) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return result
		}
		cwd = wd
	}

	fileContext, err := config.ResolveConfigFileContext(cwd, "")
	if err != nil {
		return result
	}
	result.ConfigPath = fileContext.ConfigPath
	result.ConfigExists = true

	if _, err := config.ParseAuthoredConfig(fileContext.OriginalBytes); err != nil {
		return result
	}
	result.ConfigParsed = true
	return result
}

const resolveCreateOrgIDHint = "run 'revyl auth login' to refresh credentials or 'revyl init' to bind this project"

// ResolveCreateOrgID determines which org_id should be sent when creating tests.
//
// Resolution order:
//   - live org_id from ValidateAPIKey using the active token/client
//   - file-backed credentials org_id from ~/.revyl/credentials.json
//
// This helper is intentionally separate from mismatch enforcement. Callers that
// already block on mismatches should keep doing so; this only resolves the org
// to include in create requests and returns an actionable error when no org can
// be determined.
func ResolveCreateOrgID(ctx context.Context, client *api.Client, _ *config.ProjectConfig) (string, error) {
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
