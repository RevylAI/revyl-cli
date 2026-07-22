package mcp

import (
	"os"
	"strings"

	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/outcome"
)

// SetupAuthState identifies the current Revyl authentication state.
type SetupAuthState string

const (
	authenticationStateAuthenticated       SetupAuthState = "authenticated"
	authenticationStateRequired            SetupAuthState = "auth_required"
	authenticationStateExpired             SetupAuthState = "auth_expired"
	authenticationStateCloudSecretRequired SetupAuthState = "cloud_secret_required"
)

// mcpAuthentication describes the current credential resolution without exposing secret material.
type mcpAuthentication struct {
	State     SetupAuthState
	Token     string
	LoadError error
}

// devAuthenticationFailure describes one structured authentication gate failure.
type devAuthenticationFailure struct {
	Code    SetupAuthState
	Message string
}

// resolveMCPAuthentication resolves the active credential and classifies unavailable authentication.
//
// Parameters:
//   - manager: Shared CLI credential manager.
//   - cloud: Whether the MCP server is running in Cursor Cloud.
//
// Returns:
//   - mcpAuthentication: Credential state and active token, when available.
func resolveMCPAuthentication(manager *auth.Manager, cloud bool) mcpAuthentication {
	credentials, err := manager.GetCredentials()
	if err != nil {
		return mcpAuthentication{
			State:     unavailableAuthenticationState(cloud),
			LoadError: err,
		}
	}
	if credentials == nil {
		return mcpAuthentication{State: unavailableAuthenticationState(cloud)}
	}

	apiKey := strings.TrimSpace(credentials.APIKey)
	accessToken := strings.TrimSpace(credentials.AccessToken)
	if accessToken != "" && !credentials.IsExpired() {
		return mcpAuthentication{State: authenticationStateAuthenticated, Token: accessToken}
	}
	if apiKey != "" {
		return mcpAuthentication{State: authenticationStateAuthenticated, Token: apiKey}
	}
	if accessToken != "" && credentials.IsExpired() {
		return mcpAuthentication{State: authenticationStateExpired}
	}
	return mcpAuthentication{State: unavailableAuthenticationState(cloud)}
}

// unavailableAuthenticationState returns the setup state for an environment without credentials.
//
// Parameters:
//   - cloud: Whether browser authentication is unavailable in the current runtime.
//
// Returns:
//   - SetupAuthState: Structured missing-authentication state.
func unavailableAuthenticationState(cloud bool) SetupAuthState {
	if cloud {
		return authenticationStateCloudSecretRequired
	}
	return authenticationStateRequired
}

// isCloudEnvironment reports whether Cursor marks this process as a Cloud agent.
//
// Returns:
//   - bool: Whether the established Cursor Cloud signal is present.
func isCloudEnvironment() bool {
	return os.Getenv("CURSOR_AGENT") == "1"
}

// refreshDevAuthentication re-resolves credentials and updates the shared API client in place.
//
// Returns:
//   - *devAuthenticationFailure: Structured failure when authentication is unavailable.
func (s *Server) refreshDevAuthentication() *devAuthenticationFailure {
	if s.profile != ProfileDev {
		return nil
	}
	authentication := s.resolveAndApplyDevAuthentication()
	if authentication.State == authenticationStateAuthenticated {
		return nil
	}

	return &devAuthenticationFailure{
		Code:    authentication.State,
		Message: authenticationFailureMessage(authentication.State),
	}
}

// resolveAndApplyDevAuthentication updates the existing API client from current credentials.
//
// Returns:
//   - mcpAuthentication: Current credential state after applying any valid token.
func (s *Server) resolveAndApplyDevAuthentication() mcpAuthentication {
	authentication := resolveMCPAuthentication(s.authManager, isCloudEnvironment())
	if authentication.State == authenticationStateAuthenticated {
		s.apiClient.SetAPIKey(authentication.Token)
		return authentication
	}

	// Clear any previously valid token before returning so an expired or removed
	// credential can never leak into a backend request.
	s.apiClient.SetAPIKey("")
	return authentication
}

// authenticationFailureMessage returns a secret-free remediation message for one auth state.
//
// Parameters:
//   - state: Structured authentication state.
//
// Returns:
//   - string: Actionable authentication failure message.
func authenticationFailureMessage(state SetupAuthState) string {
	switch state {
	case authenticationStateExpired:
		return "Revyl authentication expired; run 'revyl auth login'"
	case authenticationStateCloudSecretRequired:
		return "Revyl authentication requires the REVYL_API_KEY Runtime Secret and a new Cloud session"
	default:
		return "Revyl authentication required; run 'revyl auth login'"
	}
}

// failedAuthenticationOutcome converts a gate failure into the shared MCP outcome contract.
//
// Parameters:
//   - failure: Authentication failure returned by refreshDevAuthentication.
//
// Returns:
//   - outcome.Envelope: Structured, non-retryable authentication failure.
func failedAuthenticationOutcome(failure *devAuthenticationFailure) outcome.Envelope {
	return outcome.Failed(string(failure.Code), failure.Message, false)
}
