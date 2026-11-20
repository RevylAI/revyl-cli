package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sessionprofile"
)

// prepareSessionProfileStart runs the project's picked session profile for a
// start_device_session call. It returns the options to start with and the
// applied profile, nil when none applies.
func (s *Server) prepareSessionProfileStart(ctx context.Context, opts StartSessionOptions, explicitPick string) (StartSessionOptions, *sessionprofile.AppliedProfile, error) {
	var session config.AuthoredSession
	if s.project != nil && s.project.Aggregate != nil {
		session = s.project.Aggregate.Session
	}
	if _, err := sessionprofile.ResolvePick(session, explicitPick); err != nil || len(session.Profiles) == 0 {
		return opts, nil, err
	}
	backend := s.sessionMgr.APIClient()
	request := sessionprofile.PrepareRequest{
		Session:      session,
		ProjectRoot:  s.project.ProjectRoot,
		ConfigPath:   s.project.ConfigPath,
		ExplicitPick: explicitPick,
		Platform:     opts.Platform,
		Backend:      backend,
		BackendURL:   backend.BaseURL(),
	}
	if opts.Platform == "web" {
		artifact, err := s.sessionMgr.PrepareWebStartArtifact(ctx, opts)
		if err != nil {
			return opts, nil, err
		}
		opts.ResolvedWebArtifact = &artifact
		request.WebOrigin, request.WebAcceptsLaunchEnv = artifact.WebOrigin, artifact.WebAcceptsLaunchEnv
	}
	preparation, err := sessionprofile.Prepare(ctx, request)
	analytics.SetMCPToolCallProperties(ctx, preparation.AnalyticsProperties())
	if err != nil || preparation.Instruction == nil {
		return opts, nil, err
	}
	opts.SessionProfile = preparation.Instruction
	opts.OnSessionProfileNotApplied = func() {
		recordSessionProfileOutcome(ctx, sessionprofile.OutcomeBackendUnsupported)
	}
	return opts, &sessionprofile.AppliedProfile{Name: preparation.Pick.Name, MobileDeepLink: preparation.MobileDeepLink}, nil
}

// recordSessionProfileOutcome replaces the session profile outcome of the
// start_device_session call in ctx once the start itself rejects the profile.
func recordSessionProfileOutcome(ctx context.Context, outcome sessionprofile.Outcome) {
	analytics.SetMCPToolCallProperties(ctx, map[string]interface{}{"session_profile_outcome": string(outcome)})
}

// LaunchConfigurationChangedCode is the backend's refusal code for a deep link
// that uses a launch configuration changed after the session started.
const LaunchConfigurationChangedCode = "launch_configuration_changed"

// SessionProfileDeepLinkRefusal is the backend refusing to fill a profile's
// deep link. Message is the backend's own guidance, which names launch
// configuration keys and never their values.
type SessionProfileDeepLinkRefusal struct {
	ProfileName string `json:"-"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

func (r *SessionProfileDeepLinkRefusal) Error() string {
	return fmt.Sprintf("session profile %q: %s", r.ProfileName, r.Message)
}

// OpenSessionProfileDeepLink opens a profile's deep link template on a mobile
// session through the worker proxy's template route, even when it has no
// ${NAME} reference, so the backend fills its references from the
// organization's stored launch configurations and treats the whole link as a
// secret. Errors never carry the link, which may hold values the profile
// script minted.
func OpenSessionProfileDeepLink(
	ctx context.Context,
	requester WorkerSessionRequester,
	session *DeviceSession,
	profileName string,
	deepLinkTemplate string,
) error {
	err := openOnLaunchedApp(ctx, requester, session, "/open_url_template", api.DeviceOpenURLTemplateRequest{URLTemplate: deepLinkTemplate})
	if err == nil {
		return nil
	}
	var workerErr *WorkerHTTPError
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("session profile %q: opening its deep link was cancelled: %w", profileName, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("session profile %q: opening its deep link timed out: %w", profileName, context.DeadlineExceeded)
	case errors.As(err, &workerErr) && workerErr.StatusCode == http.StatusConflict:
		var conflict struct {
			Detail SessionProfileDeepLinkRefusal `json:"detail"`
		}
		if json.Unmarshal([]byte(workerErr.Body), &conflict) != nil || conflict.Detail.Message == "" {
			return fmt.Errorf("session profile %q: the Revyl backend refused to fill its deep link (worker status 409)", profileName)
		}
		conflict.Detail.ProfileName = profileName
		return &conflict.Detail
	case errors.As(err, &workerErr):
		return fmt.Errorf("session profile %q: its deep link failed to open (worker status %d)", profileName, workerErr.StatusCode)
	}
	return fmt.Errorf("session profile %q: its deep link failed to open", profileName)
}
