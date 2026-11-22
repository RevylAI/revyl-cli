package sessionprofile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/checkoutdefinitions"
	"github.com/revyl/cli/internal/config"
)

// CheckoutScriptMaxBytes is the largest profile script a start sends from the
// checkout, matching the backend's limit.
const CheckoutScriptMaxBytes = 64 * 1024

// ErrNoProjectID is a start that asked for a profile from a checkout whose
// .revyl/config.yaml has no project.id to read it from.
var ErrNoProjectID = errors.New("a session profile needs project.id in this checkout's .revyl/config.yaml; run it from your app's checkout")

// defaultProfileNeedsProjectIDError is a start that would run the checkout's
// session.default_profile from a .revyl/config.yaml without project.id.
type defaultProfileNeedsProjectIDError struct{ name string }

func (e *defaultProfileNeedsProjectIDError) Error() string {
	return fmt.Sprintf("session.default_profile %s needs project.id in this checkout's .revyl/config.yaml; add it, or pass --session-profile none", e.name)
}

func (e *defaultProfileNeedsProjectIDError) Is(target error) bool { return target == ErrNoProjectID }

// Start is the session profile part of one start request. Instruction is nil
// when the start sends no session_profile.
type Start struct {
	Pick        PickSource
	Instruction *api.SessionProfileRequest
	Source      *api.SessionProfileSource
	Files       *api.SessionProfileCheckoutFiles
}

// Runs reports whether the start asks Revyl to run a profile.
func (s Start) Runs() bool {
	return s.Instruction != nil && (s.Instruction.Instruction == api.SessionProfileInstructionKindNamed || s.Instruction.Instruction == api.SessionProfileInstructionKindDefault)
}

// SendsScript reports whether the start sends a script file from the
// checkout, rather than only a definition whose script Revyl reads.
func (s Start) SendsScript() bool {
	return s.Files != nil && s.Files.ScriptFile != nil
}

// Name is the profile a named or applied start sends, or "".
func (s Start) Name() string {
	if s.Instruction == nil {
		return ""
	}
	return s.Instruction.Name
}

// StartOptions describes how one start picks its session profile.
type StartOptions struct {
	Session     config.AuthoredSession
	ProjectRoot string
	ConfigPath  string
	// Pick is the explicit pick: "" when not given, "none" for no profile.
	Pick string
	// FromCheckout sends the profile's definition and script as this checkout
	// has them; false lets Revyl read them, and the project's default, from Git.
	FromCheckout bool
	// GitBranchName is the branch the profile reads from Git; "" uses the
	// checkout's.
	GitBranchName string
	// Asked is true when the caller asked for a profile itself rather than
	// running the checkout's session.default_profile.
	Asked bool
}

// ResolveStart resolves what a start sends for its session profile, the way a
// test run takes its checkout files. From the checkout it is the explicit
// pick, else the checkout's session.default_profile, sent as a named profile
// with its definition and script, uncommitted changes included. "none" sends
// a none instruction, and no pick and no default sends nothing. Only a start
// that reads profiles from Git sends a default instruction. A profile names
// the project in .revyl/config.yaml and the Git branch it reads.
func ResolveStart(ctx context.Context, options StartOptions) (Start, error) {
	pick := strings.TrimSpace(options.Pick)
	if pick == config.NoSessionProfile {
		return Start{Pick: PickNone, Instruction: &api.SessionProfileRequest{Instruction: api.SessionProfileInstructionKindNone}}, nil
	}
	if pick != "" && !config.ValidSessionProfileName(pick) {
		return Start{}, fmt.Errorf("session profile %q isn't a session profile name: use 1-64 lowercase letters, digits or hyphens, or none", pick)
	}
	var start Start
	switch {
	case options.FromCheckout:
		resolved, err := ResolvePick(options.Session, pick)
		if err != nil {
			return Start{}, err
		}
		if resolved.Name == "" {
			return Start{Pick: resolved.Source}, nil
		}
		files, err := checkoutFiles(options.Session, options.ProjectRoot, options.ConfigPath, resolved.Name)
		if err != nil {
			return Start{}, err
		}
		start.Pick, start.Files = resolved.Source, &files
		start.Instruction = &api.SessionProfileRequest{Instruction: api.SessionProfileInstructionKindNamed, Name: resolved.Name}
	case pick != "":
		start.Pick = PickExplicit
		start.Instruction = &api.SessionProfileRequest{Instruction: api.SessionProfileInstructionKindNamed, Name: pick}
	default:
		start.Pick = PickDefault
		start.Instruction = &api.SessionProfileRequest{Instruction: api.SessionProfileInstructionKindDefault}
	}
	projectID, err := checkoutProjectID(options.ConfigPath)
	if err != nil {
		if options.Asked {
			return Start{}, ErrNoProjectID
		}
		return Start{}, &defaultProfileNeedsProjectIDError{name: start.Name()}
	}
	start.Source = &api.SessionProfileSource{ProjectId: projectID}
	branch := strings.TrimSpace(options.GitBranchName)
	if branch == "" {
		if detected, err := buildselection.DetectGitBranch(ctx, buildselection.GitBranchOptions{WorkDir: options.ProjectRoot}); err == nil {
			branch = detected.Name
		}
	}
	if branch != "" {
		start.Source.GitBranchName = &branch
	}
	return start, nil
}

// BackendCapabilityHostedSessionProfiles is the backend capability that runs a
// named or default session profile on Revyl's hosted runner for device, web
// and computer starts, from Git or from the checkout's files.
const BackendCapabilityHostedSessionProfiles = "hosted_session_profiles"

// RequireHostedRun fails a start that runs a session profile when the backend
// can't run it on its hosted runner yet. An older backend ignores the profile's
// source and files, so the start never reaches one.
func (s Start) RequireHostedRun(ctx context.Context, backend CapabilityLister) error {
	if !s.Runs() {
		return nil
	}
	name := "the project's default session profile"
	if s.Name() != "" {
		name = "session profile " + s.Name()
	}
	supported, err := backendSupports(ctx, backend, BackendCapabilityHostedSessionProfiles)
	if err != nil {
		return fmt.Errorf("%s: could not check whether Revyl can run it: %w", name, err)
	}
	if !supported {
		return fmt.Errorf("%s can't run yet: Revyl's update that runs session profiles on its hosted runner is pending on this backend; start with --session-profile none until it lands", name)
	}
	return nil
}

func checkoutProjectID(configPath string) (uuid.UUID, error) {
	data, err := config.ReadConfigFile(configPath)
	if err != nil {
		return uuid.UUID{}, err
	}
	authored, err := config.ParseAuthoredConfig(data)
	if err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(authored.Project.ID)
}

// checkoutFiles is profileName's definition and script file as this checkout
// has them, uncommitted changes included. A script name with no .revyl/scripts
// file is a Library Script, which Revyl reads itself.
func checkoutFiles(session config.AuthoredSession, projectRoot, configPath, profileName string) (api.SessionProfileCheckoutFiles, error) {
	profile := session.Profiles[profileName]
	files := api.SessionProfileCheckoutFiles{Script: profile.Script}
	if profile.RequiresRepositoryCheckout {
		files.RequiresRepositoryCheckout = &profile.RequiresRepositoryCheckout
	}
	fail := func(err error) (api.SessionProfileCheckoutFiles, error) {
		return api.SessionProfileCheckoutFiles{}, fmt.Errorf("session profile %q: %w", profileName, err)
	}
	var filePath, projectRelativePath string
	if strings.Contains(profile.Script, "/") {
		resolved, err := resolveRepositoryPath(projectRoot, profile.Script)
		if err != nil {
			return fail(err)
		}
		filePath, projectRelativePath = resolved, path.Clean(profile.Script)
	} else {
		if checkoutdefinitions.ValidateDefinitionFileStem(profile.Script) != nil {
			return files, nil
		}
		script, found, err := findRepositoryScript(projectRoot, filepath.Join(filepath.Dir(configPath), "scripts"), profile.Script)
		if err != nil {
			return fail(err)
		}
		if !found {
			return files, nil
		}
		filePath, projectRelativePath = script, ".revyl/scripts/"+filepath.Base(script)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return fail(fmt.Errorf("could not read script %s: %w", projectRelativePath, err))
	}
	if info.Size() > CheckoutScriptMaxBytes {
		return fail(fmt.Errorf("script %s is %d bytes, over the %d a start takes", projectRelativePath, info.Size(), CheckoutScriptMaxBytes))
	}
	content, err := os.ReadFile(filePath) // #nosec G304 -- a script inside this checkout's repository, resolved by resolveRepositoryPath or findRepositoryScript
	if err != nil {
		return fail(fmt.Errorf("could not read script %s: %w", projectRelativePath, err))
	}
	if len(content) > CheckoutScriptMaxBytes {
		return fail(fmt.Errorf("script %s is %d bytes, over the %d a start takes", projectRelativePath, len(content), CheckoutScriptMaxBytes))
	}
	if !utf8.Valid(content) {
		return fail(fmt.Errorf("script %s isn't UTF-8 text", projectRelativePath))
	}
	files.ScriptFile = &api.SessionProfileCheckoutScriptFile{ProjectRelativePath: projectRelativePath, Content: string(content)}
	return files, nil
}
