// Package beforesession runs the repository-local before_session setup script
// that prepares the app under test before a device session starts.
//
// It is deliberately stricter than build.Runner, which executes an arbitrary
// shell line with no timeout and no path constraint. before_session runs on
// every session start, including unattended cloud-agent runs, so it takes a
// path to a repository-local executable rather than a shell line, bounds the
// run with a timeout, and never echoes script output.
package beforesession

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/revyl/cli/internal/config"
)

// maxCapturedOutputBytes bounds how much of each output stream is retained.
// The contract is a short list of KEY=VALUE lines, so anything beyond this is a
// runaway script rather than useful output. A var so tests can lower the limit.
var maxCapturedOutputBytes = 1 << 20

// outputDrainGrace bounds how long a killed script's surviving grandchildren
// may hold the output pipes open before the runner abandons them.
const outputDrainGrace = 5 * time.Second

// launchEnvKeyPattern is the launch-variable key rule shared with the backend.
var launchEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Error is a before_session failure whose message is safe to surface in CLI
// output, MCP responses, and persisted status. Script stdout and stderr are
// never included, because the script's whole purpose is producing secrets.
type Error struct {
	// Reason is a stable, secret-free description of the failure.
	Reason string
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Reason
}

// Result carries the session-scoped values a before_session script produced.
type Result struct {
	// Values are the KEY=VALUE pairs printed on stdout. They are applied to a
	// single device session's launch environment and never written to org
	// launch variables, so concurrent sessions cannot clobber each other.
	Values map[string]string
}

// Run executes the configured before_session script to completion and returns
// the values it printed.
//
// Failure is fatal by design, the deliberate opposite of auth_bypass's
// best-effort semantics: a session that starts against an unprepared app
// produces a confident wrong result, which is worse than no result.
//
// Parameters:
//   - ctx: Cancellation scope for the run; a timeout is applied on top of it.
//   - repoRoot: Absolute path to the Revyl project root, used as the script's
//     working directory. Script containment is the git worktree root when found.
//   - cfg: The canonical before_script block. Nil or script-less config is a
//     no-op returning an empty result.
//
// Returns:
//   - Result: Parsed session-scoped values, empty when nothing was printed.
//   - error: An *Error with a secret-free reason when the script cannot be
//     resolved, times out, exits non-zero, exceeds the stdout capture budget,
//     or prints malformed output.
func Run(ctx context.Context, repoRoot string, cfg *config.AuthoredBeforeScript) (Result, error) {
	script := authoredScriptPath(cfg)
	if script == "" {
		return Result{}, nil
	}

	scriptPath, err := resolveScriptPath(repoRoot, script)
	if err != nil {
		return Result{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, authoredScriptTimeout(cfg))
	defer cancel()

	var stdout, stderr bytes.Buffer
	stdoutWriter := &limitedWriter{buf: &stdout, remaining: maxCapturedOutputBytes}
	cmd := exec.CommandContext(runCtx, scriptPath)
	cmd.Dir = repoRoot
	cmd.Stdout = stdoutWriter
	cmd.Stderr = &limitedWriter{buf: &stderr, remaining: maxCapturedOutputBytes}
	// Killing the script does not close the output pipes if it left a
	// grandchild running, so without a wait delay the timeout would not
	// actually bound the run.
	cmd.WaitDelay = outputDrainGrace

	runErr := cmd.Run()
	if runErr != nil {
		return Result{}, runFailure(runCtx, ctx, script, runErr)
	}

	// Truncation can cut mid KEY=VALUE line; accepting the partial capture
	// would apply a corrupted secret. Fail closed instead.
	if stdoutWriter.truncated {
		return Result{}, &Error{
			Reason: fmt.Sprintf(
				"session.before_script %q produced more than 1MiB of stdout",
				script,
			),
		}
	}

	values, err := parseValues(stdout.String(), script)
	if err != nil {
		return Result{}, err
	}
	return Result{Values: values}, nil
}

func authoredScriptPath(cfg *config.AuthoredBeforeScript) string {
	if cfg == nil || cfg.ScriptPath == nil {
		return ""
	}
	return strings.TrimSpace(*cfg.ScriptPath)
}

func authoredScriptTimeout(cfg *config.AuthoredBeforeScript) time.Duration {
	timeoutSeconds := config.DefaultBeforeSessionTimeoutSeconds
	if cfg != nil && cfg.TimeoutSeconds != nil && *cfg.TimeoutSeconds > 0 {
		timeoutSeconds = *cfg.TimeoutSeconds
	}
	return time.Duration(timeoutSeconds) * time.Second
}

// resolveScriptPath resolves a configured script to an executable file inside
// the git worktree.
//
// Relative scripts are joined against the Revyl project root (the directory
// that owns `.revyl/`). Containment is the git worktree root when one is
// found by walking up from that project root, otherwise the project root
// itself. That lets nested projects (e.g. `ios/`) call scripts elsewhere in
// the same monorepo via `../…` without allowing escapes outside the checkout.
//
// Parameters:
//   - projectRoot: Absolute path to the Revyl project root (cmd.Dir for the run).
//   - script: The configured path, relative to the project root (or absolute).
//
// Returns:
//   - string: The resolved absolute path to the script.
//   - error: An *Error when the path escapes the worktree, does not exist,
//     is not a regular file, or (on Unix) is not executable.
func resolveScriptPath(projectRoot, script string) (string, error) {
	trimmed := strings.TrimSpace(script)
	candidate := trimmed
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(projectRoot, candidate)
	}

	// Symlinks are resolved on both sides before comparison so a link inside
	// the worktree cannot point the runner at a file outside it.
	realProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return "", &Error{Reason: "session.before_script could not resolve the repository root"}
	}
	containmentRoot, err := gitWorktreeRoot(realProjectRoot)
	if err != nil {
		return "", &Error{Reason: "session.before_script could not resolve the repository root"}
	}
	realScript, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", &Error{
			Reason: fmt.Sprintf("session.before_script %q was not found in the repository", trimmed),
		}
	}

	if realScript != containmentRoot && !strings.HasPrefix(realScript, containmentRoot+string(filepath.Separator)) {
		return "", &Error{
			Reason: fmt.Sprintf("session.before_script %q resolves outside the repository", trimmed),
		}
	}

	info, err := os.Stat(realScript)
	if err != nil {
		return "", &Error{
			Reason: fmt.Sprintf("session.before_script %q was not found in the repository", trimmed),
		}
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return "", &Error{
			Reason: fmt.Sprintf("session.before_script %q is not a regular file", trimmed),
		}
	}
	// Windows file modes do not carry Unix execute bits, so enforcing 0o111
	// there rejects every script. Host-native executability is left to exec.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", &Error{
			Reason: fmt.Sprintf("session.before_script %q is not executable (chmod +x it)", trimmed),
		}
	}

	return realScript, nil
}

// gitWorktreeRoot walks up from projectRoot looking for a `.git` entry and
// returns that directory. When none is found, projectRoot is returned so
// containment falls back to the Revyl project root.
//
// Parameters:
//   - projectRoot: Absolute, symlink-resolved Revyl project root.
//
// Returns:
//   - string: Absolute containment root for before_session scripts.
//   - error: Always nil today; reserved for future resolution failures.
func gitWorktreeRoot(projectRoot string) (string, error) {
	current := projectRoot
	for {
		gitPath := filepath.Join(current, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return projectRoot, nil
		}
		current = parent
	}
}

// runFailure converts an execution failure into a secret-free *Error.
//
// Parameters:
//   - runCtx: The timeout-bounded context the script ran under.
//   - parentCtx: The caller's context, used to tell cancellation from timeout.
//   - script: The configured script path, echoed back for orientation.
//   - err: The raw failure from exec.
//
// Returns:
//   - error: An *Error describing the failure without script output.
func runFailure(runCtx, parentCtx context.Context, script string, err error) error {
	switch {
	case parentCtx.Err() != nil:
		return &Error{Reason: fmt.Sprintf("session.before_script %q was cancelled", script)}
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return &Error{Reason: fmt.Sprintf("session.before_script %q timed out", script)}
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &Error{
			Reason: fmt.Sprintf("session.before_script %q failed (exit %d)", script, exitErr.ExitCode()),
		}
	}
	return &Error{Reason: fmt.Sprintf("session.before_script %q could not be run", script)}
}

// parseValues extracts KEY=VALUE pairs from script stdout.
//
// Only well-formed lines are considered, so scripts can log freely on stdout
// without their prose being mistaken for values.
//
// Parameters:
//   - stdout: The script's captured standard output.
//   - script: The configured script path, echoed back for orientation.
//
// Returns:
//   - map[string]string: The parsed values, nil when none were printed.
//   - error: An *Error when a key is printed twice or with an empty value.
//     Neither the offending value nor any other output appears in the message.
func parseValues(stdout, script string) (map[string]string, error) {
	var values map[string]string

	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		separator := strings.Index(line, "=")
		if separator < 0 {
			continue
		}
		key := line[:separator]
		if !launchEnvKeyPattern.MatchString(key) {
			continue
		}

		value := line[separator+1:]
		if value == "" {
			return nil, &Error{
				Reason: fmt.Sprintf(
					"session.before_script %q printed %s with an empty value",
					script, key,
				),
			}
		}
		if _, duplicate := values[key]; duplicate {
			return nil, &Error{
				Reason: fmt.Sprintf(
					"session.before_script %q printed %s more than once",
					script, key,
				),
			}
		}
		if values == nil {
			values = make(map[string]string)
		}
		values[key] = value
	}

	return values, nil
}

// limitedWriter buffers output up to a fixed budget and discards the rest, so a
// runaway script cannot exhaust memory.
type limitedWriter struct {
	buf       *bytes.Buffer
	remaining int
	// truncated is true once any byte has been discarded past the budget.
	truncated bool
}

// Write implements io.Writer, reporting every byte as written so the child
// process never sees a short write once the budget is exhausted.
//
// Parameters:
//   - p: Bytes from the child process.
//
// Returns:
//   - int: Always len(p), even when some or all bytes were discarded.
//   - error: Always nil.
func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	chunk := p
	if len(chunk) > w.remaining {
		chunk = chunk[:w.remaining]
		w.truncated = true
	}
	w.buf.Write(chunk)
	w.remaining -= len(chunk)
	return len(p), nil
}
