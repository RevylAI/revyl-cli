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

// ErrNoProjectID means explicitly requested profile handling could not resolve
// a valid project.id from the checkout's .revyl/config.yaml.
var ErrNoProjectID = errors.New("a session profile needs project.id in this checkout's .revyl/config.yaml; run it from your app's checkout")

type defaultProfileNeedsProjectIDError struct{ name string }

func (e *defaultProfileNeedsProjectIDError) Error() string {
	return fmt.Sprintf("session.default_profile %s needs project.id in this checkout's .revyl/config.yaml; add it, or pass --session-profile none", e.name)
}

func (e *defaultProfileNeedsProjectIDError) Is(target error) bool { return target == ErrNoProjectID }

// Start holds the session_profile instruction and its optional source and files.
// A nil Instruction omits session_profile; it does not send "none".
type Start struct {
	Pick        PickSource
	Instruction *api.SessionProfileRequest
	Source      *api.SessionProfileSource
	Files       *api.SessionProfileCheckoutFiles
}

// Runs reports whether Revyl must run the profile, excluding applied outputs.
func (s Start) Runs() bool {
	return s.Instruction != nil && (s.Instruction.Instruction == api.SessionProfileInstructionKindNamed || s.Instruction.Instruction == api.SessionProfileInstructionKindDefault)
}

func (s Start) SendsScript() bool {
	return s.Files != nil && s.Files.ScriptFile != nil
}

func (s Start) Name() string {
	if s.Instruction == nil {
		return ""
	}
	return s.Instruction.Name
}

type StartOptions struct {
	Session     config.AuthoredSession
	ProjectRoot string
	ConfigPath  string
	// Pick is the explicit pick: "" when not given, "none" for no profile.
	Pick string
	// FromCheckout sends the local definition and any local script file.
	// Otherwise Revyl uses the project's stored configuration.
	FromCheckout bool
	// GitBranchName overrides branch detection from the checkout and CI.
	GitBranchName string
	// Asked selects the explicit-request error when project.id cannot be read.
	Asked bool
}

// ResolveStart sends a checkout's explicit pick or local default by name,
// including uncommitted files, and omits the instruction when neither is set.
// An explicit "none" suppresses even a backend default. A start using stored
// configuration can delegate the default pick to Revyl.
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

// BackendCapabilityHostedSessionProfiles advertises support for running named
// or default profiles on the hosted runner.
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

// A bare script name without a matching checkout file is resolved by Revyl
// from the Script Library.
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
