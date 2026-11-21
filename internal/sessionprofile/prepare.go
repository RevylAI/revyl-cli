package sessionprofile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// BackendCapabilitySessionProfiles is the backend capability that accepts an
// applied session_profile on a start request.
const BackendCapabilitySessionProfiles = "session_profiles"

// Backend is what preparing a session profile needs from the Revyl API.
type Backend interface {
	ScriptLibrary
	GetBackendCapabilities(ctx context.Context) ([]string, error)
}

// PrepareRequest describes one session start that may apply a profile.
type PrepareRequest struct {
	Session     config.AuthoredSession
	ProjectRoot string
	ConfigPath  string
	// ExplicitPick is the --session-profile or session_profile value: "" when
	// not given, "none" to start without the default.
	ExplicitPick string
	// Platform is ios, android or web.
	Platform string
	// WebOrigin is the web app's scheme://host[:port], for web sessions.
	WebOrigin string
	// WebAcceptsLaunchEnv is true when Revyl starts the web app with a start
	// command, so profile launch env can reach it.
	WebAcceptsLaunchEnv bool
	Backend             Backend
	BackendURL          string
}

// Preparation is the outcome of preparing one start's session profile.
type Preparation struct {
	Pick Pick
	// Instruction is the start request's session_profile, nil when no profile
	// applies.
	Instruction *api.AppliedSessionProfile
	// MobileDeepLink is the profile's deep link template for a mobile session,
	// which the CLI opens after launch; mobile instructions never carry it.
	MobileDeepLink *string
	ScriptSource   ScriptSource
	OutputKinds    []string
	Outcome        Outcome

	backendSupportsProfiles bool
}

// Prepare picks the start's profile and, when one applies, runs its script
// and returns the applied instruction. It returns a Preparation filled as far
// as it got, so callers can record analytics for failures too.
func Prepare(ctx context.Context, request PrepareRequest) (Preparation, error) {
	if len(request.Session.Profiles) == 0 && (request.ExplicitPick == "" || request.ExplicitPick == config.NoSessionProfile) {
		return Preparation{}, nil
	}
	pick, err := ResolvePick(request.Session, request.ExplicitPick)
	if err != nil {
		return Preparation{}, err
	}
	preparation := Preparation{Pick: pick}

	capabilities, capabilitiesErr := request.Backend.GetBackendCapabilities(ctx)
	preparation.backendSupportsProfiles = capabilitiesErr == nil && slices.Contains(capabilities, BackendCapabilitySessionProfiles)
	if pick.Name == "" {
		return preparation, nil
	}
	if !preparation.backendSupportsProfiles {
		var apiErr *api.APIError
		if capabilitiesErr != nil && !(errors.As(capabilitiesErr, &apiErr) && apiErr.StatusCode == 404) {
			return preparation, fmt.Errorf("session profile %q: could not check whether the Revyl backend supports session profiles: %w", pick.Name, capabilitiesErr)
		}
		preparation.Outcome = OutcomeBackendUnsupported
		return preparation, &Error{Outcome: OutcomeBackendUnsupported, Reason: "This project's session profiles need a newer Revyl backend"}
	}

	fail := func(err error) (Preparation, error) {
		var profileErr *Error
		if !errors.As(err, &profileErr) {
			return preparation, fmt.Errorf("session profile %q: %w", pick.Name, err)
		}
		preparation.Outcome = profileErr.Outcome
		return preparation, &Error{Outcome: profileErr.Outcome, Reason: fmt.Sprintf("session profile %q: %s", pick.Name, profileErr.Reason)}
	}
	scriptRef := request.Session.Profiles[pick.Name].Script
	script, err := resolveScript(ctx, request.Backend, request.ProjectRoot, request.ConfigPath, scriptRef)
	if err != nil {
		return fail(err)
	}
	preparation.ScriptSource = script.source

	workDir, err := os.MkdirTemp("", "revyl-session-profile-")
	if err != nil {
		return fail(fmt.Errorf("could not create the script's private directory: %w", err))
	}
	defer os.RemoveAll(workDir)
	outputDir := filepath.Join(workDir, "outputs")
	if err := os.Mkdir(outputDir, 0o700); err != nil {
		return fail(fmt.Errorf("could not create the script's output directory: %w", err))
	}
	run := scriptRun{
		script:      script,
		ref:         scriptRef,
		projectRoot: request.ProjectRoot,
		platform:    request.Platform,
		profileName: pick.Name,
		webOrigin:   request.WebOrigin,
		backendURL:  request.BackendURL,
	}
	if err := runScript(ctx, run, workDir, outputDir); err != nil {
		return fail(err)
	}
	outputs, err := parseOutputs(outputDir, outputTarget{platform: request.Platform, webAcceptsLaunchEnv: request.WebAcceptsLaunchEnv})
	if err != nil {
		return fail(err)
	}

	preparation.OutputKinds = outputKinds(outputs)
	if request.Platform != "web" {
		preparation.MobileDeepLink, outputs.DeepLink = outputs.DeepLink, nil
	}
	preparation.Instruction = &api.AppliedSessionProfile{
		Instruction: api.SessionProfileInstructionKindApplied,
		Name:        pick.Name,
		Outputs:     outputs,
	}
	preparation.Outcome = OutcomeApplied
	return preparation, nil
}

// AnalyticsProperties returns the CLI analytics properties for this start, or
// nil when the backend has not confirmed it accepts them.
func (p Preparation) AnalyticsProperties() map[string]any {
	if !p.backendSupportsProfiles {
		return nil
	}
	properties := map[string]any{"session_profile_pick": string(p.Pick.Source)}
	if p.ScriptSource != "" {
		properties["session_profile_script_source"] = string(p.ScriptSource)
	}
	if p.Outcome != "" {
		properties["session_profile_outcome"] = string(p.Outcome)
	}
	if p.OutputKinds != nil {
		properties["session_profile_output_kinds"] = p.OutputKinds
	}
	return properties
}
