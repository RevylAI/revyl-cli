// Package beforesession runs session.before_script in the local checkout
// and returns launch values for the CLI start paths that opt into it.
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

var maxCapturedOutputBytes = 1 << 20
var outputDrainGrace = 5 * time.Second

var launchEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Error describes a setup failure for CLI output and persisted status.
// Run's errors omit script output and values; they may name paths and keys.
type Error struct {
	Reason string
}

func (e *Error) Error() string {
	return e.Reason
}

// Result carries launch values for one session, not organization variables.
type Result struct {
	Values map[string]string
}

// Run executes the configured script in the Revyl project directory under the
// caller's context and a script timeout. A nil or blank script is a no-op.
// Callers must abort preparation on *Error rather than boot without the values.
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
	// A surviving descendant can hold the output pipes open after cancellation.
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

// runFailure deliberately omits the original process error and captured output.
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

// parseValues ignores unrecognized lines; errors for recognized assignments
// name the key but omit its value.
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

type limitedWriter struct {
	buf       *bytes.Buffer
	remaining int
	truncated bool
}

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
