package mcp

import (
	"context"
	"strings"

	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/outcome"
)

type SetupAuthState string

const (
	authenticationStateAuthenticated       SetupAuthState = "authenticated"
	authenticationStateRequired            SetupAuthState = "auth_required"
	authenticationStateExpired             SetupAuthState = "auth_expired"
	authenticationStateCloudSecretRequired SetupAuthState = "cloud_secret_required"
	authenticationStateInvalid             SetupAuthState = "auth_invalid"
	authenticationStateCloudContextInvalid SetupAuthState = "cloud_context_invalid"
)

type mcpAuthentication struct {
	State         SetupAuthState
	Token         string
	HeadlessCloud bool
	Signals       SetupEnvironmentSignals
	LoadError     error
}

type devAuthenticationFailure struct {
	Code    SetupAuthState
	Message string

	Authorization *auth.DeviceAuthorization
}

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

func invalidAuthenticationState(cloudContextInvalid bool) SetupAuthState {
	if cloudContextInvalid {
		return authenticationStateCloudContextInvalid
	}
	return authenticationStateInvalid
}

func unavailableAuthenticationState(cloud bool) SetupAuthState {
	if cloud {
		return authenticationStateCloudSecretRequired
	}
	return authenticationStateRequired
}

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
func approvalResolves(state SetupAuthState) bool {
	return state != authenticationStateCloudContextInvalid
}

func (s *Server) resolveAndApplyDevAuthentication() mcpAuthentication {
	authentication := resolveMCPAuthentication(s.authManager)
	if authentication.State == authenticationStateAuthenticated {
		s.apiClient.SetAPIKey(authentication.Token)
		return authentication
	}

	// Clear any previously valid token so later requests do not reuse an expired or removed credential.
	s.apiClient.SetAPIKey("")
	return authentication
}

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

const bridgeRecoverySuffix = "or 'revyl auth persist-cloud-env' when REVYL_API_KEY is set for this agent"

func failedAuthenticationOutcome(failure *devAuthenticationFailure) outcome.Envelope {
	envelope := outcome.Failed(string(failure.Code), failure.Message, false)
	if failure.Authorization != nil {
		envelope.AuthorizationURL = failure.Authorization.VerificationUriComplete
		envelope.AuthorizationCode = failure.Authorization.UserCode
	}
	return envelope
}
