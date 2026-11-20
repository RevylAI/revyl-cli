package sessionprofile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Environment names of the profile script contract.
const (
	envPlatform    = "REVYL_PLATFORM"
	envProfileName = "REVYL_SESSION_PROFILE_NAME"
	envWebOrigin   = "REVYL_SESSION_WEB_ORIGIN"
	envBackendURL  = "REVYL_BACKEND_URL"
)

// scriptRun is one profile script run's inputs.
type scriptRun struct {
	script      profileScript
	ref         string
	projectRoot string
	platform    string
	profileName string
	webOrigin   string
	backendURL  string
}

// runScript runs the script once in workDir's scratch space, writing its
// outputs under outputDir. The script's stdout and stderr are discarded.
func runScript(ctx context.Context, run scriptRun, workDir, outputDir string) error {
	scriptPath := run.script.path
	if run.script.code != "" {
		scriptPath = filepath.Join(workDir, "script"+scriptFileExtension(run.script.runtime))
		if err := os.WriteFile(scriptPath, []byte(run.script.code), 0o600); err != nil {
			return &Error{Outcome: OutcomeScriptUnavailable, Reason: fmt.Sprintf("could not stage Library Script %q to run", run.ref)}
		}
	}
	command, err := scriptCommand(run.script, scriptPath)
	if err != nil {
		return err
	}
	command.Dir = run.projectRoot
	command.Env = scriptEnvironment(os.Environ(), run, outputDir)

	runCtx, cancel := context.WithTimeout(ctx, ScriptTimeout)
	defer cancel()
	killGroup, err := startInProcessGroup(command)
	if err != nil {
		return &Error{Outcome: OutcomeScriptUnavailable, Reason: fmt.Sprintf("script %s could not start: %v", run.ref, err)}
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	var waitErr error
	select {
	case waitErr = <-exited:
		killGroup()
	case <-runCtx.Done():
		killGroup()
		<-exited
		if ctx.Err() != nil {
			return &Error{Outcome: OutcomeScriptFailed, Reason: fmt.Sprintf("script %s was cancelled", run.ref)}
		}
		return &Error{Outcome: OutcomeScriptTimedOut, Reason: fmt.Sprintf("script %s did not finish within %d seconds", run.ref, int(ScriptTimeout.Seconds()))}
	}
	if waitErr == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) && exitErr.ExitCode() >= 0 {
		return &Error{Outcome: OutcomeScriptFailed, Reason: fmt.Sprintf("script %s exited with code %d", run.ref, exitErr.ExitCode())}
	}
	return &Error{Outcome: OutcomeScriptFailed, Reason: fmt.Sprintf("script %s was killed before it finished", run.ref)}
}

func scriptCommand(script profileScript, scriptPath string) (*exec.Cmd, error) {
	if script.runtime == "" {
		return exec.Command(scriptPath), nil // #nosec G204 -- the project's own profile script
	}
	interpreter := interpreterByRuntime[script.runtime]
	interpreterPath, err := exec.LookPath(interpreter[0])
	if err != nil {
		return nil, &Error{Outcome: OutcomeInterpreterMissing, Reason: fmt.Sprintf("%s scripts need %s, which isn't installed or isn't on PATH", script.runtime, interpreter[0])}
	}
	return exec.Command(interpreterPath, append(interpreter[1:], scriptPath)...), nil // #nosec G204 -- fixed interpreter and the profile's script
}

// scriptEnvironment is the caller's environment, minus any inherited copies of
// the contract's names, plus this run's contract values.
func scriptEnvironment(inherited []string, run scriptRun, outputDir string) []string {
	values := map[string]string{envPlatform: run.platform, envProfileName: run.profileName, envBackendURL: run.backendURL}
	if run.platform == "web" {
		values[envWebOrigin] = run.webOrigin
	}
	contractNames := []string{envPlatform, envProfileName, envWebOrigin, envBackendURL}
	for _, file := range outputFiles {
		values[file.envName] = filepath.Join(outputDir, file.fileName)
		contractNames = append(contractNames, file.envName)
	}
	environment := make([]string, 0, len(inherited)+len(values))
	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.ContainsFunc(contractNames, func(contractName string) bool { return strings.EqualFold(contractName, name) }) {
			environment = append(environment, entry)
		}
	}
	for name, value := range values {
		environment = append(environment, name+"="+value)
	}
	return environment
}
