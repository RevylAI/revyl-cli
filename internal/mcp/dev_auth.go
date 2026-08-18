package mcp

import (
	"context"
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
	authenticationStateInvalid             SetupAuthState = "auth_invalid"
	authenticationStateCloudContextInvalid SetupAuthState = "cloud_context_invalid"
)

// mcpAuthentication describes the current credential resolution without exposing secret material.
type mcpAuthentication struct {
	State         SetupAuthState
	Token         string
	HeadlessCloud bool
	Signals       SetupEnvironmentSignals
	LoadError     error
}

// devAuthenticationFailure describes one structured authentication gate failure.
type devAuthenticationFailure struct {
	Code    SetupAuthState
	Message string

	// Authorization is a live browser approval the user can complete to fix
	// this failure. Nil when none could be registered, which leaves the failure
	// reportable but not directly actionable from the card.
	Authorization *auth.DeviceAuthorization
}

// resolveMCPAuthentication resolves the active credential and classifies unavailable authentication.
//
// Parameters:
//   - manager: Shared CLI credential manager.
//
// Returns:
//   - mcpAuthentication: Credential state and active token, when available.
func resolveMCPAuthentication(manager *auth.Manager) mcpAuthentication {
	resolution, err := manager.ResolveCredentials()
	signals := SetupEnvironmentSignals{
		CloudContextPresent: resolution.HeadlessCloud && !resolution.CloudContextInvalid,
		CloudContextInvalid: resolution.CloudContextInvalid,
		APIKeyEnvironment:   resolution.APIKeyEnvironment,
	}
	if err != nil {
		return mcpAuthentication{
			State:         invalidAuthenticationState(resolution.CloudContextInvalid),
			HeadlessCloud: resolution.HeadlessCloud,
			Signals:       signals,
			LoadError:     err,
		}
	}
	credentials := resolution.Credentials
	if credentials == nil {
		return mcpAuthentication{
			State:         unavailableAuthenticationState(resolution.HeadlessCloud),
			HeadlessCloud: resolution.HeadlessCloud,
			Signals:       signals,
		}
	}

	apiKey := strings.TrimSpace(credentials.APIKey)
	accessToken := strings.TrimSpace(credentials.AccessToken)
	if accessToken != "" && !credentials.IsExpired() {
		return mcpAuthentication{
			State:         authenticationStateAuthenticated,
			Token:         accessToken,
			HeadlessCloud: resolution.HeadlessCloud,
			Signals:       signals,
		}
	}
	if apiKey != "" {
		return mcpAuthentication{
			State:         authenticationStateAuthenticated,
			Token:         apiKey,
			HeadlessCloud: resolution.HeadlessCloud,
			Signals:       signals,
		}
	}
	if accessToken != "" && credentials.IsExpired() {
		return mcpAuthentication{
			State:         authenticationStateExpired,
			HeadlessCloud: resolution.HeadlessCloud,
			Signals:       signals,
		}
	}
	return mcpAuthentication{
		State:         unavailableAuthenticationState(resolution.HeadlessCloud),
		HeadlessCloud: resolution.HeadlessCloud,
		Signals:       signals,
	}
}

// invalidAuthenticationState classifies unreadable authentication state by runtime context.
//
// Parameters:
//   - cloudContextInvalid: Whether the Cloud runtime context itself failed to load.
//
// Returns:
//   - SetupAuthState: Structured local-storage or Cloud-context failure.
func invalidAuthenticationState(cloudContextInvalid bool) SetupAuthState {
	if cloudContextInvalid {
		return authenticationStateCloudContextInvalid
	}
	return authenticationStateInvalid
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

// refreshDevAuthentication re-resolves credentials and updates the shared API client in place.
//
// When the credential is missing rather than structurally broken, it also
// registers a browser approval so the returned failure is something the user
// can act on immediately instead of a bare instruction.
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

	var authorization *auth.DeviceAuthorization
	if approvalResolves(authentication.State) {
		// Deliberately not the tool call's context. The approval outlives the
		// call that surfaced it, so a caller who gives up must not cancel a
		// registration the next call would have reused.
		authorization = s.authorizationForGate(context.Background())
	}
	return &devAuthenticationFailure{
		Code:          authentication.State,
		Message:       authenticationFailureMessage(authentication.State, authorization),
		Authorization: authorization,
	}
}

// approvalResolves reports whether a browser approval would fix this state.
//
// A broken Cloud runtime context is not an authorization problem: minting a new
// key would leave the same unreadable context behind, so offering approval there
// would send the user down a path that cannot succeed.
//
// Parameters:
//   - state: Structured authentication state.
//
// Returns:
//   - bool: Whether to offer the user an approval.
func approvalResolves(state SetupAuthState) bool {
	return state != authenticationStateCloudContextInvalid
}

// resolveAndApplyDevAuthentication updates the existing API client from current credentials.
//
// Returns:
//   - mcpAuthentication: Current credential state after applying any valid token.
func (s *Server) resolveAndApplyDevAuthentication() mcpAuthentication {
	authentication := resolveMCPAuthentication(s.authManager)
	if authentication.State == authenticationStateAuthenticated {
		s.apiClient.SetAPIKey(authentication.Token)
		return authentication
	}

	// Clear any previously valid token before returning so an expired or removed
	// credential can never leak into a backend request.
	s.apiClient.SetAPIKey("")
	return authentication
}

// authenticationFailureMessage returns a secret-free message naming every
// recovery available for one auth state.
//
// Both recoveries are named rather than one being chosen from an environment
// guess. Misreading the runtime withdraws a recovery that would have worked,
// and the two are not interchangeable: a browser login needs a human, while the
// bridge needs REVYL_API_KEY to be visible to the shell that runs it, which can
// be true even when the server process cannot see it.
//
// Parameters:
//   - state: Structured authentication state.
//   - authorization: Live browser approval to name, or nil when none exists.
//
// Returns:
//   - string: Actionable authentication failure message.
func authenticationFailureMessage(
	state SetupAuthState,
	authorization *auth.DeviceAuthorization,
) string {
	if state == authenticationStateCloudContextInvalid {
		return "Revyl Cloud authentication context is invalid; start a fresh session"
	}

	problem := "Revyl authentication required"
	switch state {
	case authenticationStateExpired:
		problem = "Revyl authentication expired"
	case authenticationStateInvalid:
		problem = "Revyl authentication state is invalid"
	}

	recovery := "run 'revyl auth login', " + bridgeRecoverySuffix
	if instruction := authorizationInstruction(authorization); instruction != "" {
		recovery = instruction +
			", run 'revyl auth login' to do the same from a terminal, " +
			bridgeRecoverySuffix
	}
	return problem + "; " + recovery
}

// bridgeRecoverySuffix names the unattended recovery for an agent that has a
// Runtime Secret but no human to complete a browser login.
const bridgeRecoverySuffix = "or 'revyl auth persist-cloud-env' when REVYL_API_KEY is set for this agent"

// failedAuthenticationOutcome converts a gate failure into the shared MCP outcome contract.
//
// Parameters:
//   - failure: Authentication failure returned by refreshDevAuthentication.
//
// Returns:
//   - outcome.Envelope: Structured, non-retryable authentication failure.
func failedAuthenticationOutcome(failure *devAuthenticationFailure) outcome.Envelope {
	envelope := outcome.Failed(string(failure.Code), failure.Message, false)
	if failure.Authorization != nil {
		envelope.AuthorizationURL = failure.Authorization.VerificationUriComplete
		envelope.AuthorizationCode = failure.Authorization.UserCode
	}
	return envelope
}
