package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/sessionprofile"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

const sessionProfileFlagUsage = "Session profile from .revyl/config.yaml for Revyl to run before the session is ready (waits until it finishes), or none to skip the project's default_profile"

// devLoopWaitingForSessionProfile is what a detached dev loop writes to its
// claim while Revyl runs its session profile, so the detach parent starts its
// readiness wait only after the profile finishes.
const devLoopWaitingForSessionProfile = "session_profile_running"

// sessionProfileRuntime sends a project's session profile with the device
// sessions this invocation starts, reports how Revyl's hosted run of it goes,
// and remembers which profile a started or reused session has.
type sessionProfileRuntime struct {
	start        sessionprofile.Start
	projectRoot  string
	explicitPick string

	mu                 sync.Mutex
	applied            string
	firstLaunchPending bool
}

// devSessionProfile is the active runtime for the current command invocation,
// nil when its starts send no session profile.
var devSessionProfile *sessionProfileRuntime

// initDevSessionProfile resolves the session profile this invocation's starts
// send from the checkout: the explicit pick, else session.default_profile, by
// name with its definition and script. A pick the project doesn't define
// fails; analytics gets a fixed diagnostic, since profile names are
// customer-authored.
func initDevSessionProfile(ctx context.Context, projectRoot, configPath string, session config.AuthoredSession, explicitPick string) error {
	devSessionProfile = nil
	start, err := sessionprofile.ResolveStart(ctx, sessionprofile.StartOptions{
		Session:      session,
		ProjectRoot:  projectRoot,
		ConfigPath:   configPath,
		Pick:         explicitPick,
		FromCheckout: true,
		Asked:        explicitPick != "" && explicitPick != config.NoSessionProfile,
	})
	if err != nil {
		return analytics.WithSafeDiagnostic(err, "session profile is not usable")
	}
	if start.Instruction == nil && explicitPick == "" {
		return nil
	}
	devSessionProfile = &sessionProfileRuntime{start: start, projectRoot: projectRoot, explicitPick: explicitPick}
	return nil
}

// checkSessionProfilePick fails a pick the project doesn't define. The error
// lists the project's profiles for the person, while analytics gets a fixed
// diagnostic, since profile names are customer-authored.
func checkSessionProfilePick(session config.AuthoredSession, explicitPick string) error {
	_, err := sessionprofile.ResolvePick(session, explicitPick)
	return analytics.WithSafeDiagnostic(err, "session profile is not defined")
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

// prepareStart puts the session profile on a new session's start and reports
// each status of Revyl's run of it.
func (r *sessionProfileRuntime) prepareStart(ctx context.Context, opts mcppkg.StartSessionOptions) mcppkg.StartSessionOptions {
	if r == nil {
		return opts
	}
	r.mu.Lock()
	r.applied, r.firstLaunchPending = "", false
	r.mu.Unlock()
	opts.SessionProfile = r.start
	opts.OnSessionProfile = func(report api.SessionProfileReport) { r.observe(ctx, report) }
	return opts
}

func (r *sessionProfileRuntime) observe(ctx context.Context, report api.SessionProfileReport) {
	waiting := report.Status == api.SessionProfileReportStatusPending || report.Status == api.SessionProfileReportStatusRunning
	if isDetachedDevChild() {
		markDetachedDevLoopWaitingForSessionProfile(r.projectRoot, waiting)
	}
	ui.StopSpinner()
	switch report.Status {
	case api.SessionProfileReportStatusPending:
		ui.PrintInfo("Session profile %s is waiting for Revyl's hosted runner...", report.Name)
	case api.SessionProfileReportStatusRunning:
		ui.PrintInfo("Running session profile %s on Revyl...", report.Name)
	case api.SessionProfileReportStatusSucceeded:
		r.mu.Lock()
		r.applied, r.firstLaunchPending = report.Name, true
		r.mu.Unlock()
		ui.PrintDim("Applied session profile %s", report.Name)
		analytics.SetCommandCompletion(ctx, analytics.CommandCompletion{Properties: map[string]any{"session_profile_pick": string(r.start.Pick), "session_profile_script_sent": r.start.SendsScript()}})
	}
}

// rememberAppliedProfile records the profile a new session started with, so
// reuse and auth refresh know it. A failed record leaves the running session
// in place and warns that later commands won't know its profile.
func (r *sessionProfileRuntime) rememberAppliedProfile(workDir, sessionID string) {
	name := r.appliedProfileName()
	if name == "" {
		return
	}
	if err := sessionprofile.SaveAppliedProfile(workDir, sessionID, sessionprofile.AppliedProfile{Name: name}); err != nil {
		ui.PrintWarning("Couldn't record that this session started with session profile %s (%v), so reusing it and `revyl dev auth refresh` won't know its profile; stop it and start a new one to fix that", name, err)
	}
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
	r.applied, r.firstLaunchPending = "", false
	if applied != nil {
		r.applied = applied.Name
	}
	r.mu.Unlock()
	return nil
}

// takeFirstLaunch reports the profile that ran on the session this invocation
// started when the app has yet to launch there, and marks that launch taken.
// The worker opens the profile's deep link on that launch, so the launch
// failing means the profile's sign-in never reached the app.
func (r *sessionProfileRuntime) takeFirstLaunch() (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.firstLaunchPending {
		return "", false
	}
	r.firstLaunchPending = false
	return r.applied, true
}

// pendingFirstLaunch reports what takeFirstLaunch would without taking it,
// for a launch that is skipped.
func (r *sessionProfileRuntime) pendingFirstLaunch() (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applied, r.firstLaunchPending
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
	return r.applied
}

// refreshSessionProfileDeepLink answers `revyl dev auth refresh` for a project
// with session profiles. Revyl's hosted runner opens a profile's deep link
// once, when the session starts, and the CLI never holds the profile's
// outputs, so only a fresh session runs the profile again.
func refreshSessionProfileDeepLink(cmd *cobra.Command) error {
	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}
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
	return fail("no_deep_link", fmt.Sprintf("session profile %s ran on Revyl's hosted runner, which applies its outputs, deep link included, only when the session starts", applied.Name), restartAction)
}

func markDetachedDevLoopWaitingForSessionProfile(repoRoot string, waiting bool) {
	var content []byte
	if waiting {
		content = []byte(devLoopWaitingForSessionProfile)
	}
	_ = writeManagedRuntimeFile(devActiveLoopPath(repoRoot, os.Getpid()), content)
}
