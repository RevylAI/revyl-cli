package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/sessionprofile"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

const sessionProfileFlagUsage = "Session profile from .revyl/config.yaml to run before the session starts, or none to skip the project's default_profile"

// devLoopRunningSessionProfileScript is what a detached dev loop writes to its
// claim while its session profile script runs, so the detach parent starts its
// readiness wait only after the script finishes.
const devLoopRunningSessionProfileScript = "session_profile_script_running"

// sessionProfileRuntime applies a project's session profiles to the device
// sessions this invocation starts or reuses: it runs the picked profile once
// per new session and opens the profile's mobile deep link after launch.
type sessionProfileRuntime struct {
	session      config.AuthoredSession
	projectRoot  string
	configPath   string
	explicitPick string
	backend      *api.Client

	mu                   sync.Mutex
	preparation          sessionprofile.Preparation
	applied              *sessionprofile.AppliedProfile
	startDeepLinkPending bool
}

// devSessionProfile is the active runtime for the current command invocation,
// nil for projects without session.profiles.
var devSessionProfile *sessionProfileRuntime

// checkSessionProfilePick fails a pick the project doesn't define. The error
// lists the project's profiles for the person, while analytics gets a fixed
// diagnostic, since profile names are customer-authored.
func checkSessionProfilePick(session config.AuthoredSession, explicitPick string) error {
	_, err := sessionprofile.ResolvePick(session, explicitPick)
	return analytics.WithSafeDiagnostic(err, "session profile is not defined")
}

// initDevSessionProfile checks the explicit pick and activates session
// profiles for this invocation when the project defines them.
func initDevSessionProfile(backend *api.Client, projectRoot, configPath string, session config.AuthoredSession, explicitPick string) error {
	if err := checkSessionProfilePick(session, explicitPick); err != nil {
		return err
	}
	if len(session.Profiles) == 0 {
		devSessionProfile = nil
		return nil
	}
	devSessionProfile = &sessionProfileRuntime{
		session:      session,
		projectRoot:  projectRoot,
		configPath:   configPath,
		explicitPick: explicitPick,
		backend:      backend,
	}
	return nil
}

// sessionWithoutProfilesInsideComputer drops the project's profiles for a start
// inside a computer, where no session profile runs, and fails an explicit pick
// there.
func sessionWithoutProfilesInsideComputer(session config.AuthoredSession, explicitPick string) (config.AuthoredSession, error) {
	if explicitPick != "" && explicitPick != config.NoSessionProfile {
		return session, fmt.Errorf("session profile %q can't run inside a computer; pass --session-profile none or --computer none", explicitPick)
	}
	session.DefaultProfile, session.Profiles = nil, nil
	return session, nil
}

// prepareStart runs the picked profile for a new session and puts its applied
// instruction on opts. web is the resolved artifact of a web start.
func (r *sessionProfileRuntime) prepareStart(ctx context.Context, opts mcppkg.StartSessionOptions, web *startdevice.ResolvedStartArtifact) (mcppkg.StartSessionOptions, error) {
	if r == nil {
		return opts, nil
	}
	request := sessionprofile.PrepareRequest{
		Session:      r.session,
		ProjectRoot:  r.projectRoot,
		ConfigPath:   r.configPath,
		ExplicitPick: r.explicitPick,
		Platform:     opts.Platform,
		Backend:      r.backend,
		BackendURL:   r.backend.BaseURL(),
	}
	if web != nil {
		request.WebOrigin, request.WebAcceptsLaunchEnv = web.WebOrigin, web.WebAcceptsLaunchEnv
	}
	if pick, _ := sessionprofile.ResolvePick(r.session, r.explicitPick); pick.Name != "" {
		ui.PrintInfo("Running session profile %s...", pick.Name)
		if isDetachedDevChild() {
			markDetachedDevLoopRunningSessionProfileScript(r.projectRoot, true)
			defer markDetachedDevLoopRunningSessionProfileScript(r.projectRoot, false)
		}
	}
	preparation, err := sessionprofile.Prepare(ctx, request)
	r.mu.Lock()
	r.preparation = preparation
	r.applied, r.startDeepLinkPending = nil, false
	if preparation.Instruction != nil {
		r.applied = &sessionprofile.AppliedProfile{Name: preparation.Pick.Name, MobileDeepLink: preparation.MobileDeepLink}
		r.startDeepLinkPending = preparation.MobileDeepLink != nil
	}
	r.mu.Unlock()
	r.recordAnalytics(ctx)
	if err != nil {
		return opts, analytics.WithSafeDiagnostic(err, fmt.Sprintf("session profile failed (%s)", preparation.Outcome))
	}
	if preparation.Instruction != nil {
		ui.PrintDim("Applied session profile %s", preparation.Pick.Name)
		opts.OnSessionProfileNotApplied = func() {
			r.mu.Lock()
			r.preparation.Outcome = sessionprofile.OutcomeBackendUnsupported
			r.applied, r.startDeepLinkPending = nil, false
			r.mu.Unlock()
			r.recordAnalytics(ctx)
		}
	}
	opts.SessionProfile = preparation.Instruction
	return opts, nil
}

func (r *sessionProfileRuntime) recordAnalytics(ctx context.Context) {
	r.mu.Lock()
	properties := r.preparation.AnalyticsProperties()
	r.mu.Unlock()
	if properties != nil {
		analytics.SetCommandCompletion(ctx, analytics.CommandCompletion{Properties: properties})
	}
}

// rememberAppliedProfile records the profile a new session started with, so
// reuse and auth refresh never rerun its script. A failed record leaves the
// running session in place and warns that later commands won't know its
// profile.
func (r *sessionProfileRuntime) rememberAppliedProfile(workDir, sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	applied := r.applied
	r.mu.Unlock()
	if applied == nil {
		return
	}
	if err := sessionprofile.SaveAppliedProfile(workDir, sessionID, *applied); err != nil {
		ui.PrintWarning("Couldn't record that this session started with session profile %s (%v), so reusing it and `revyl dev auth refresh` won't know its profile; stop it and start a new one to fix that", applied.Name, err)
	}
}

// dropDeepLink forgets the profile's deep link when the caller's own app link
// replaces it, so auth refresh never opens a link this session never opened.
func (r *sessionProfileRuntime) dropDeepLink() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.applied != nil {
		r.applied = &sessionprofile.AppliedProfile{Name: r.applied.Name}
	}
	r.startDeepLinkPending = false
}

// adoptReusedSession loads the profile a reused session started with and
// fails an explicit pick that only a fresh session could honor.
func (r *sessionProfileRuntime) adoptReusedSession(workDir, sessionID string) error {
	if r == nil {
		return nil
	}
	applied, err := r.loadSessionAppliedProfile(workDir, sessionID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.applied, r.startDeepLinkPending = applied, false
	r.mu.Unlock()
	return nil
}

// refusePickForRunningSession fails an explicit pick that a session already
// running in a dev context didn't start with, before the command shows that
// session instead of starting one.
func (r *sessionProfileRuntime) refusePickForRunningSession(workDir, sessionID string) error {
	if r == nil {
		return nil
	}
	_, err := r.loadSessionAppliedProfile(workDir, sessionID)
	return err
}

// loadSessionAppliedProfile returns the profile an existing session started
// with, failing an explicit pick that only a fresh session could honor.
func (r *sessionProfileRuntime) loadSessionAppliedProfile(workDir, sessionID string) (*sessionprofile.AppliedProfile, error) {
	applied, err := sessionprofile.LoadAppliedProfile(workDir, sessionID)
	if err != nil {
		return nil, err
	}
	if r.explicitPick == "" {
		return applied, nil
	}
	startedWith := config.NoSessionProfile
	if applied != nil {
		startedWith = applied.Name
	}
	if r.explicitPick != startedWith {
		return nil, fmt.Errorf("session profile %s requires a fresh device session, since this one started with %s; stop the existing loop (`stop_dev_loop` in MCP or `revyl dev stop` in the CLI), then retry", r.explicitPick, startedWith)
	}
	return applied, nil
}

// appliedProfileName is the profile the current session started with.
func (r *sessionProfileRuntime) appliedProfileName() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.applied == nil {
		return ""
	}
	return r.applied.Name
}

// openStartDeepLink opens the profile's mobile deep link after the first
// launch of a session this invocation started. Its failure fails the start.
func (r *sessionProfileRuntime) openStartDeepLink(ctx context.Context, requester workerSessionRequester, session *mcppkg.DeviceSession) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	pending, applied := r.startDeepLinkPending, r.applied
	r.startDeepLinkPending = false
	r.mu.Unlock()
	if !pending {
		return nil
	}
	if err := mcppkg.OpenSessionProfileDeepLink(ctx, requester, session, applied.Name, *applied.MobileDeepLink); err != nil {
		r.mu.Lock()
		r.preparation.Outcome = sessionprofile.OutcomeDeepLinkFailed
		r.mu.Unlock()
		r.recordAnalytics(ctx)
		return analytics.WithSafeDiagnostic(err, "session profile deep link failed")
	}
	ui.PrintDim("Opened session profile %s deep link", applied.Name)
	return nil
}

// reopenDeepLinkAfterRelaunch reopens the profile's mobile deep link after the
// app relaunches. It only warns on failure and waits while the start's own
// open is pending.
func (r *sessionProfileRuntime) reopenDeepLinkAfterRelaunch(ctx context.Context, requester workerSessionRequester, session *mcppkg.DeviceSession) {
	if r == nil {
		return
	}
	r.mu.Lock()
	applied, pending := r.applied, r.startDeepLinkPending
	r.mu.Unlock()
	if pending || applied == nil || applied.MobileDeepLink == nil {
		return
	}
	if err := mcppkg.OpenSessionProfileDeepLink(ctx, requester, session, applied.Name, *applied.MobileDeepLink); err != nil {
		ui.PrintWarning("%v", err)
	}
}

// refreshSessionProfileDeepLink reopens the deep link the target session's
// profile wrote at start, without rerunning the profile script.
func refreshSessionProfileDeepLink(cmd *cobra.Command) error {
	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}
	// Messages name the profile, so analytics gets the code alone.
	fail := func(code, message, action string) error {
		return analytics.WithSafeDiagnostic(devAuthRefreshError(cmd, code, message, action), fmt.Sprintf("session profile deep link refresh failed (%s)", code))
	}
	session, err := resolveSessionTarget(cmd, deviceMgr)
	if err != nil {
		return fail("no_session",
			fmt.Sprintf("no active device session: %v", err),
			"start one with `revyl dev` or `revyl device start`")
	}
	restartAction := "restart_session: run `revyl dev stop` then `revyl dev` so the session profile runs again at boot"
	applied, err := sessionprofile.LoadAppliedProfile(deviceMgr.WorkDir(), session.SessionID)
	if err != nil {
		return fail("session_profile_load_failed", err.Error(), restartAction)
	}
	if applied == nil {
		return fail("no_session_profile", "this session started without a session profile", restartAction)
	}
	if applied.MobileDeepLink == nil {
		reason := fmt.Sprintf("session profile %s wrote no deep link — its other outputs apply only at session boot", applied.Name)
		if session.Platform == "web" {
			reason = fmt.Sprintf("session profile %s applies to a web session only at session boot", applied.Name)
		}
		return fail("no_deep_link", reason, restartAction)
	}
	if err := mcppkg.OpenSessionProfileDeepLink(cmd.Context(), deviceMgr, session, applied.Name, *applied.MobileDeepLink); err != nil {
		var refusal *mcppkg.SessionProfileDeepLinkRefusal
		switch {
		case errors.As(err, &refusal) && refusal.Code == mcppkg.LaunchConfigurationChangedCode:
			return fail(refusal.Code, err.Error(), restartAction)
		case errors.As(err, &refusal):
			return fail("deep_link_refused", err.Error(), "fix the launch configuration the message names in the Library, then run `revyl dev auth refresh` again")
		}
		return fail("deep_link_failed", err.Error(), "check the device session is alive (`revyl dev status`)")
	}
	result := map[string]interface{}{
		"ok":              true,
		"deep_link_fired": true,
		"session_id":      session.SessionID,
		"session_index":   session.Index,
		"session_profile": applied.Name,
	}
	jsonOrPrint(cmd, result, fmt.Sprintf("Session profile %s deep link reopened", applied.Name))
	return nil
}

func markDetachedDevLoopRunningSessionProfileScript(repoRoot string, running bool) {
	var content []byte
	if running {
		content = []byte(devLoopRunningSessionProfileScript)
	}
	_ = writeManagedRuntimeFile(devActiveLoopPath(repoRoot, os.Getpid()), content)
}
