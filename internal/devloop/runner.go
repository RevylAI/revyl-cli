// Package devloop defines the stable CLI-backed dev-loop boundary used by MCP.
package devloop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	// RebuildProgressEnvironment enables the private CLI-to-MCP progress protocol.
	RebuildProgressEnvironment = "REVYL_INTERNAL_REBUILD_PROGRESS"
	// RebuildProgressJSONLMode selects version one of the JSON Lines protocol.
	RebuildProgressJSONLMode = "jsonl-v1"
	// RebuildProgressPrefix identifies structured progress records on stderr.
	RebuildProgressPrefix = "REVYL_REBUILD_PROGRESS "
	// RebuildControlEnvironment selects a private asynchronous rebuild operation.
	RebuildControlEnvironment = "REVYL_INTERNAL_REBUILD_CONTROL"
	// RebuildHandleEnvironment carries a serialized handle into a wait-only child.
	RebuildHandleEnvironment = "REVYL_INTERNAL_REBUILD_HANDLE"
	// RebuildControlTriggerMode requests a structured non-blocking trigger.
	RebuildControlTriggerMode = "trigger-json-v1"
	// RebuildControlWaitMode requests a wait that never sends another rebuild signal.
	RebuildControlWaitMode = "wait-json-v1"

	maxRebuildProgressLineBytes = 1024 * 1024
	maxCommandStderrBytes       = 64 * 1024
)

// BuildState is the stable public lifecycle for a dev-loop build.
type BuildState string

const (
	BuildStatePreparing       BuildState = "preparing"
	BuildStateCapacityBlocked BuildState = "capacity_blocked"
	BuildStateQueued          BuildState = "queued"
	BuildStateBuilding        BuildState = "building"
	BuildStateInstalling      BuildState = "installing"
	BuildStateLaunching       BuildState = "launching"
	BuildStateVerifying       BuildState = "verifying"
	BuildStateSuccess         BuildState = "success"
	BuildStateFailed          BuildState = "failed"
	BuildStateCancelled       BuildState = "cancelled"
)

// StartRequest contains the focused inputs supported by MCP start_dev_loop.
type StartRequest struct {
	Context        string
	Platform       string
	PlatformKey    string
	AppID          string
	BuildVersionID string
	Port           int
	TimeoutSeconds int
	Remote         bool
	SeedLatest     bool
}

// BuildStatus describes the latest build without conflating admission and execution.
type BuildStatus struct {
	State             BuildState `json:"state,omitempty"`
	Phase             string     `json:"phase,omitempty"`
	RemoteJobID       string     `json:"remote_job_id,omitempty"`
	FreshBuildApplied bool       `json:"fresh_build_applied"`
	SeededVersion     string     `json:"seeded_version,omitempty"`
	BuiltVersion      string     `json:"built_version,omitempty"`
	Retryable         bool       `json:"retryable"`
	Reason            string     `json:"reason,omitempty"`
}

// RebuildLogEntry describes one sanitized progress message from a running rebuild.
type RebuildLogEntry struct {
	At      string `json:"at"`
	Message string `json:"message"`
	Kind    string `json:"kind,omitempty"`
}

// BuildError describes one compiler or build-system diagnostic.
type BuildError struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// RebuildStatusSnapshot describes the latest persisted rebuild lifecycle.
type RebuildStatusSnapshot struct {
	StartedAt       string            `json:"started_at,omitempty"`
	CompletedAt     string            `json:"completed_at,omitempty"`
	Sequence        int               `json:"seq"`
	Status          string            `json:"status"`
	DurationMs      int64             `json:"duration_ms"`
	BuildDurationMs int64             `json:"build_duration_ms"`
	PushMode        string            `json:"push_mode,omitempty"`
	PushDurationMs  int64             `json:"push_duration_ms"`
	FilesChanged    int               `json:"files_changed"`
	DataPreserved   bool              `json:"data_preserved"`
	RemoteJobID     string            `json:"remote_job_id,omitempty"`
	BuiltVersionID  string            `json:"remote_build_version_id,omitempty"`
	BuiltVersion    string            `json:"remote_build_version,omitempty"`
	Error           string            `json:"error,omitempty"`
	BuildErrors     []BuildError      `json:"build_errors,omitempty"`
	Logs            []RebuildLogEntry `json:"logs,omitempty"`
}

// RebuildProgressEvent describes one ordered update from a running rebuild.
type RebuildProgressEvent struct {
	Sequence    int        `json:"sequence"`
	Status      string     `json:"status,omitempty"`
	State       BuildState `json:"state,omitempty"`
	Phase       string     `json:"phase,omitempty"`
	Message     string     `json:"message,omitempty"`
	Kind        string     `json:"kind,omitempty"`
	RemoteJobID string     `json:"remote_job_id,omitempty"`
}

// RebuildHandle identifies one detached rebuild request across MCP calls.
type RebuildHandle struct {
	ProjectDir           string `json:"project_dir"`
	Context              string `json:"context"`
	BaselineSequence     int    `json:"baseline_sequence"`
	ExpectedSequence     int    `json:"expected_sequence"`
	ProcessID            int    `json:"process_id"`
	ProcessStartedAtNano int64  `json:"process_started_at_nano,omitempty"` // Legacy numeric generation for older clients.
	ProcessGeneration    string `json:"process_generation,omitempty"`      // Exact JSON-safe process generation.
	RequestedAt          string `json:"requested_at"`
}

// UnmarshalJSON accepts both the shared build contract and legacy dev status snapshots.
func (s *BuildStatus) UnmarshalJSON(data []byte) error {
	type buildAlias BuildStatus
	var shared buildAlias
	if err := json.Unmarshal(data, &shared); err != nil {
		return err
	}
	var legacy struct {
		Status          string `json:"status"`
		Error           string `json:"error"`
		RemoteJobID     string `json:"remote_job_id"`
		RemoteVersionID string `json:"remote_build_version_id"`
		RemoteVersion   string `json:"remote_build_version"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	*s = BuildStatus(shared)
	if s.State == "" {
		s.State = classifyBuildState(legacy.Status, legacy.RemoteJobID, legacy.Error)
	}
	if s.RemoteJobID == "" {
		s.RemoteJobID = legacy.RemoteJobID
	}
	if s.BuiltVersion == "" {
		s.BuiltVersion = firstNonEmpty(legacy.RemoteVersion, legacy.RemoteVersionID)
	}
	if s.Reason == "" {
		s.Reason = legacy.Error
	}
	s.Retryable = s.Retryable || s.State == BuildStateCapacityBlocked
	s.FreshBuildApplied = s.FreshBuildApplied || (s.State == BuildStateSuccess && s.BuiltVersion != "")
	return nil
}

// StartResult is the structured detach handshake returned by the canonical CLI.
type StartResult struct {
	Context       string      `json:"context"`
	State         string      `json:"state"`
	PID           int         `json:"pid"`
	Platform      string      `json:"platform"`
	SessionID     string      `json:"session_id"`
	SessionIndex  int         `json:"session_index"`
	ViewerURL     string      `json:"viewer_url"`
	LogPath       string      `json:"log_path,omitempty"`
	SeededVersion string      `json:"seeded_version,omitempty"`
	InstalledSeed bool        `json:"installed_seed,omitempty"`
	Build         BuildStatus `json:"build"`
}

// StatusResult is the stable subset of revyl dev status consumed by MCP.
type StatusResult struct {
	Running           bool                   `json:"running"`
	Context           string                 `json:"context"`
	State             string                 `json:"state"`
	Platform          string                 `json:"platform"`
	BuildMode         string                 `json:"build_mode,omitempty"`
	SessionID         string                 `json:"session_id"`
	SessionOwned      bool                   `json:"session_owned"`
	ViewerURL         string                 `json:"viewer_url"`
	LastRebuildStatus string                 `json:"last_rebuild_status"`
	LastRebuildError  string                 `json:"last_rebuild_error"`
	LastRebuild       *RebuildStatusSnapshot `json:"last_rebuild,omitempty"`
	RemoteJobID       string                 `json:"remote_job_id"`
	SeededVersion     string                 `json:"seeded_version"`
	InstalledSeed     bool                   `json:"installed_seed"`
	Build             BuildStatus            `json:"build"`
}

// RebuildRequest configures one bounded canonical rebuild.
type RebuildRequest struct {
	Context        string
	TimeoutSeconds int
	OnProgress     func(RebuildProgressEvent)
}

// TriggerRebuildRequest selects the dev context for one asynchronous trigger.
type TriggerRebuildRequest struct {
	Context string
}

// WaitForRebuildRequest configures a bounded wait for one previously triggered rebuild.
type WaitForRebuildRequest struct {
	Handle         RebuildHandle
	TimeoutSeconds int
	OnProgress     func(RebuildProgressEvent)
}

// RebuildResult describes the terminal rebuild snapshot returned by the CLI.
type RebuildResult struct {
	Status           string       `json:"status"`
	Error            string       `json:"error,omitempty"`
	DurationMs       int64        `json:"duration_ms"`
	BuildDurationMs  int64        `json:"build_duration_ms"`
	PushMode         string       `json:"push_mode,omitempty"`
	PushDurationMs   int64        `json:"push_duration_ms"`
	FilesChanged     int          `json:"files_changed"`
	DataPreserved    bool         `json:"data_preserved"`
	BackgroundUpload string       `json:"background_upload_status,omitempty"`
	RemoteJobID      string       `json:"remote_job_id,omitempty"`
	BuiltVersionID   string       `json:"remote_build_version_id,omitempty"`
	BuiltVersion     string       `json:"remote_build_version,omitempty"`
	BuildErrors      []BuildError `json:"build_errors,omitempty"`
	Build            BuildStatus  `json:"build"`
}

// StopResult confirms the delegated context stop request.
type StopResult struct {
	Stopped bool   `json:"stopped"`
	Context string `json:"context,omitempty"`
}

// Runner executes the canonical dev-loop behavior behind a narrow testable interface.
type Runner interface {
	Start(ctx context.Context, workDir string, request StartRequest) (StartResult, error)
	Status(ctx context.Context, workDir, contextName string) (StatusResult, error)
	Rebuild(ctx context.Context, workDir string, request RebuildRequest) (RebuildResult, error)
	TriggerRebuild(ctx context.Context, workDir string, request TriggerRebuildRequest) (RebuildHandle, error)
	WaitForRebuild(ctx context.Context, workDir string, request WaitForRebuildRequest) (RebuildResult, error)
	Stop(ctx context.Context, workDir, contextName string) (StopResult, error)
}

// CommandRunner delegates dev-loop behavior to the current Revyl executable.
type CommandRunner struct {
	BinaryPath string
	DevMode    bool
}

// Start runs the detached CLI handshake and returns as soon as the viewer is ready.
func (r *CommandRunner) Start(ctx context.Context, workDir string, request StartRequest) (StartResult, error) {
	args := []string{"dev", "--detach", "--json", "--no-open"}
	args = appendStringFlag(args, "--context", request.Context)
	args = appendStringFlag(args, "--platform", request.Platform)
	args = appendStringFlag(args, "--platform-key", request.PlatformKey)
	args = appendStringFlag(args, "--app-id", request.AppID)
	args = appendStringFlag(args, "--build-version-id", request.BuildVersionID)
	args = appendIntFlag(args, "--port", request.Port)
	args = appendIntFlag(args, "--timeout", request.TimeoutSeconds)
	if request.Remote {
		args = append(args, "--remote")
	}
	if request.SeedLatest {
		args = append(args, "--seed-latest")
	}
	return runJSON[StartResult](ctx, r, workDir, args)
}

// Status reads the canonical dev status snapshot.
func (r *CommandRunner) Status(ctx context.Context, workDir, contextName string) (StatusResult, error) {
	args := []string{"dev", "status", "--json"}
	args = appendStringFlag(args, "--context", contextName)
	result, err := runJSON[StatusResult](ctx, r, workDir, args)
	if result.Build.State == "" {
		result.Build = BuildStatus{
			State:         classifyBuildState(result.LastRebuildStatus, result.RemoteJobID, result.LastRebuildError),
			RemoteJobID:   result.RemoteJobID,
			SeededVersion: result.SeededVersion,
			Reason:        result.LastRebuildError,
			Retryable:     strings.Contains(strings.ToLower(result.LastRebuildError), "capacity"),
		}
	}
	return result, err
}

// Rebuild triggers one bounded canonical rebuild and waits for its terminal result.
func (r *CommandRunner) Rebuild(ctx context.Context, workDir string, request RebuildRequest) (RebuildResult, error) {
	timeoutSeconds := request.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300
	}
	args := []string{"dev", "rebuild", "--wait", "--json", "--timeout", strconv.Itoa(timeoutSeconds)}
	args = appendStringFlag(args, "--context", request.Context)
	var result RebuildResult
	var err error
	if request.OnProgress == nil {
		result, err = runJSON[RebuildResult](ctx, r, workDir, args)
	} else {
		result, err = runRebuildJSONWithProgress(ctx, r, workDir, args, request.OnProgress, nil)
	}
	result.Build = BuildStatus{
		State:             classifyBuildState(result.Status, result.RemoteJobID, result.Error),
		RemoteJobID:       result.RemoteJobID,
		BuiltVersion:      firstNonEmpty(result.BuiltVersion, result.BuiltVersionID),
		FreshBuildApplied: result.Status == "success" && result.BuiltVersionID != "",
		Retryable:         strings.Contains(strings.ToLower(result.Error), "capacity"),
		Reason:            result.Error,
	}
	return result, err
}

// TriggerRebuild sends one rebuild request and returns without waiting for completion.
//
// Parameters:
//   - ctx: Command cancellation context.
//   - workDir: Revyl project directory.
//   - request: Dev context selection.
//
// Returns:
//   - RebuildHandle: Correlation handle for a later wait.
//   - error: Trigger or handle decoding failure.
func (r *CommandRunner) TriggerRebuild(
	ctx context.Context,
	workDir string,
	request TriggerRebuildRequest,
) (RebuildHandle, error) {
	args := []string{"dev", "rebuild"}
	args = appendStringFlag(args, "--context", request.Context)
	return runJSONWithEnvironment[RebuildHandle](
		ctx,
		r,
		workDir,
		args,
		[]commandEnvironmentValue{{
			Key:   RebuildControlEnvironment,
			Value: RebuildControlTriggerMode,
		}},
	)
}

// WaitForRebuild waits for one handle without triggering another rebuild.
//
// Parameters:
//   - ctx: Command cancellation context.
//   - workDir: Revyl project directory.
//   - request: Handle, timeout, and optional progress callback.
//
// Returns:
//   - RebuildResult: Terminal rebuild result.
//   - error: Handle validation, timeout, cancellation, or terminal rebuild failure.
func (r *CommandRunner) WaitForRebuild(
	ctx context.Context,
	workDir string,
	request WaitForRebuildRequest,
) (RebuildResult, error) {
	timeoutSeconds := request.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300
	}
	encodedHandle, err := json.Marshal(request.Handle)
	if err != nil {
		return RebuildResult{}, fmt.Errorf("encode rebuild handle: %w", err)
	}
	args := []string{"dev", "rebuild", "--wait", "--json", "--timeout", strconv.Itoa(timeoutSeconds)}
	args = appendStringFlag(args, "--context", request.Handle.Context)
	environment := []commandEnvironmentValue{
		{Key: RebuildControlEnvironment, Value: RebuildControlWaitMode},
		{Key: RebuildHandleEnvironment, Value: string(encodedHandle)},
	}
	var result RebuildResult
	if request.OnProgress == nil {
		result, err = runJSONWithEnvironment[RebuildResult](ctx, r, workDir, args, environment)
	} else {
		result, err = runRebuildJSONWithProgress(ctx, r, workDir, args, request.OnProgress, environment)
	}
	result.Build = BuildStatus{
		State:             classifyBuildState(result.Status, result.RemoteJobID, result.Error),
		RemoteJobID:       result.RemoteJobID,
		BuiltVersion:      firstNonEmpty(result.BuiltVersion, result.BuiltVersionID),
		FreshBuildApplied: result.Status == "success" && result.BuiltVersionID != "",
		Retryable:         strings.Contains(strings.ToLower(result.Error), "capacity"),
		Reason:            result.Error,
	}
	return result, err
}

// commandEnvironmentValue defines one child-process environment override.
type commandEnvironmentValue struct {
	Key   string
	Value string
}

// runRebuildJSONWithProgress executes one rebuild and consumes its private stderr event stream.
//
// Parameters:
//   - ctx: Command cancellation context.
//   - runner: Command configuration and binary resolver.
//   - workDir: Revyl project directory.
//   - args: Canonical rebuild command arguments.
//   - onProgress: Ordered progress callback.
//
// Returns:
//   - RebuildResult: Parsed terminal stdout contract.
//   - error: Command, stderr stream, or stdout decoding failure.
func runRebuildJSONWithProgress(
	ctx context.Context,
	runner *CommandRunner,
	workDir string,
	args []string,
	onProgress func(RebuildProgressEvent),
	environment []commandEnvironmentValue,
) (RebuildResult, error) {
	var result RebuildResult
	commandArgs := append([]string(nil), args...)
	if runner.DevMode {
		commandArgs = append([]string{"--dev"}, commandArgs...)
	}
	binaryPath, err := runner.resolveBinaryPath()
	if err != nil {
		return result, err
	}
	command := exec.CommandContext(ctx, binaryPath, commandArgs...)
	command.Dir = workDir
	environment = append(environment, commandEnvironmentValue{
		Key:   RebuildProgressEnvironment,
		Value: RebuildProgressJSONLMode,
	})
	command.Env = withEnvironmentValues(os.Environ(), environment)
	var stdout bytes.Buffer
	command.Stdout = &stdout
	stderr, err := command.StderrPipe()
	if err != nil {
		return result, fmt.Errorf("open revyl rebuild stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		return result, fmt.Errorf("start revyl %s: %w", strings.Join(args, " "), err)
	}

	stderrTail := newBoundedTailBuffer(maxCommandStderrBytes)
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 64*1024), maxRebuildProgressLineBytes)
	for scanner.Scan() {
		line := scanner.Text()
		event, ok := parseRebuildProgressLine(line)
		if ok {
			onProgress(event)
			continue
		}
		_, _ = stderrTail.Write([]byte(line + "\n"))
	}
	scanErr := scanner.Err()
	runErr := command.Wait()
	if scanErr != nil {
		_, _ = stderrTail.Write([]byte(fmt.Sprintf("read rebuild progress: %v\n", scanErr)))
	}
	if stdout.Len() > 0 {
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			return result, fmt.Errorf("decode revyl %s output: %w", strings.Join(args, " "), err)
		}
	}
	if runErr != nil {
		return result, &CommandError{Args: args, Stderr: stderrTail.String(), Err: runErr}
	}
	return result, nil
}

// parseRebuildProgressLine decodes one fixed-prefix progress line.
//
// Parameters:
//   - line: One stderr line from the rebuild child.
//
// Returns:
//   - RebuildProgressEvent: Parsed event when valid.
//   - bool: Whether the line was a valid progress record.
func parseRebuildProgressLine(line string) (RebuildProgressEvent, bool) {
	var event RebuildProgressEvent
	if !strings.HasPrefix(line, RebuildProgressPrefix) {
		return event, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, RebuildProgressPrefix))
	if payload == "" || json.Unmarshal([]byte(payload), &event) != nil {
		return RebuildProgressEvent{}, false
	}
	return event, true
}

// withEnvironmentValues returns an environment with deterministic key values.
//
// Parameters:
//   - environment: Existing process environment.
//   - values: Environment variable overrides.
//
// Returns:
//   - []string: Copy of the environment with prior key entries replaced.
func withEnvironmentValues(environment []string, values []commandEnvironmentValue) []string {
	result := append([]string(nil), environment...)
	for _, value := range values {
		result = replaceEnvironmentValue(result, value.Key, value.Value)
	}
	return result
}

// replaceEnvironmentValue replaces one key in an environment copy.
//
// Parameters:
//   - environment: Existing process environment.
//   - key: Environment variable name.
//   - value: Environment variable value.
//
// Returns:
//   - []string: Environment with one deterministic key value.
func replaceEnvironmentValue(environment []string, key string, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

// boundedTailBuffer retains only the newest bytes written up to a fixed limit.
type boundedTailBuffer struct {
	maxBytes  int
	data      []byte
	truncated bool
}

// newBoundedTailBuffer creates one bounded stderr buffer.
//
// Parameters:
//   - maxBytes: Maximum retained byte count.
//
// Returns:
//   - *boundedTailBuffer: Empty bounded buffer.
func newBoundedTailBuffer(maxBytes int) *boundedTailBuffer {
	return &boundedTailBuffer{maxBytes: maxBytes}
}

// Write appends bytes while retaining only the configured tail.
//
// Parameters:
//   - data: Bytes to append.
//
// Returns:
//   - int: Original input length.
//   - error: Always nil.
func (b *boundedTailBuffer) Write(data []byte) (int, error) {
	written := len(data)
	if b.maxBytes <= 0 {
		b.truncated = b.truncated || written > 0
		return written, nil
	}
	if len(data) >= b.maxBytes {
		b.data = append(b.data[:0], data[len(data)-b.maxBytes:]...)
		b.truncated = true
		return written, nil
	}
	b.data = append(b.data, data...)
	if overflow := len(b.data) - b.maxBytes; overflow > 0 {
		b.data = append(b.data[:0], b.data[overflow:]...)
		b.truncated = true
	}
	return written, nil
}

// String returns retained stderr with an explicit truncation marker.
//
// Returns:
//   - string: Retained stderr tail.
func (b *boundedTailBuffer) String() string {
	if b == nil {
		return ""
	}
	if b.truncated {
		return "[earlier stderr truncated]\n" + string(b.data)
	}
	return string(b.data)
}

// Stop stops the canonical dev context and its owned session.
func (r *CommandRunner) Stop(ctx context.Context, workDir, contextName string) (StopResult, error) {
	args := []string{"dev", "stop", "--json"}
	args = appendStringFlag(args, "--context", contextName)
	result, err := runJSON[StopResult](ctx, r, workDir, args)
	if err == nil {
		result.Stopped = true
		result.Context = contextName
	}
	return result, err
}

// CommandError preserves bounded stderr while keeping parsed JSON available to callers.
type CommandError struct {
	Args   []string
	Stderr string
	Err    error
}

// Error returns a concise command failure without exposing stdout payloads.
func (e *CommandError) Error() string {
	message := strings.TrimSpace(e.Stderr)
	if message == "" {
		message = e.Err.Error()
	}
	return fmt.Sprintf("revyl %s failed: %s", strings.Join(e.Args, " "), message)
}

// Unwrap exposes the underlying process error for cancellation and exit inspection.
func (e *CommandError) Unwrap() error {
	return e.Err
}

// runJSON executes one bounded CLI command and decodes its stdout contract.
func runJSON[T any](ctx context.Context, runner *CommandRunner, workDir string, args []string) (T, error) {
	return runJSONWithEnvironment[T](ctx, runner, workDir, args, nil)
}

// runJSONWithEnvironment executes one CLI command with private environment overrides.
//
// Parameters:
//   - ctx: Command cancellation context.
//   - runner: Command configuration and binary resolver.
//   - workDir: Command working directory.
//   - args: CLI arguments.
//   - environment: Private child-process environment overrides.
//
// Returns:
//   - T: Parsed stdout contract.
//   - error: Command or decoding failure.
func runJSONWithEnvironment[T any](
	ctx context.Context,
	runner *CommandRunner,
	workDir string,
	args []string,
	environment []commandEnvironmentValue,
) (T, error) {
	var result T
	commandArgs := append([]string(nil), args...)
	if runner.DevMode {
		commandArgs = append([]string{"--dev"}, commandArgs...)
	}
	binaryPath, err := runner.resolveBinaryPath()
	if err != nil {
		return result, err
	}
	command := exec.CommandContext(ctx, binaryPath, commandArgs...)
	command.Dir = workDir
	if len(environment) > 0 {
		command.Env = withEnvironmentValues(os.Environ(), environment)
	}
	var stdout bytes.Buffer
	stderr := newBoundedTailBuffer(maxCommandStderrBytes)
	command.Stdout = &stdout
	command.Stderr = stderr
	runErr := command.Run()
	if stdout.Len() > 0 {
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			return result, fmt.Errorf("decode revyl %s output: %w", strings.Join(args, " "), err)
		}
	}
	if runErr != nil {
		return result, &CommandError{Args: args, Stderr: stderr.String(), Err: runErr}
	}
	return result, nil
}

// resolveBinaryPath resolves the delegated Revyl executable for one invocation.
//
// Returns:
//   - string: Executable path selected from REVYL_BINARY or the configured fallback.
//   - error: If the explicit override or fallback is not executable.
func (r *CommandRunner) resolveBinaryPath() (string, error) {
	if requested := strings.TrimSpace(os.Getenv("REVYL_BINARY")); requested != "" {
		resolved, err := exec.LookPath(requested)
		if err != nil {
			return "", fmt.Errorf("resolve REVYL_BINARY %q: %w", requested, err)
		}
		return resolved, nil
	}

	fallback := strings.TrimSpace(r.BinaryPath)
	if fallback == "" {
		return "", fmt.Errorf("resolve Revyl executable: no binary path configured")
	}
	resolved, err := exec.LookPath(fallback)
	if err != nil {
		return "", fmt.Errorf("resolve Revyl executable %q: %w", fallback, err)
	}
	return resolved, nil
}

// appendStringFlag appends a non-empty string flag.
func appendStringFlag(args []string, name, value string) []string {
	if strings.TrimSpace(value) == "" {
		return args
	}
	return append(args, name, strings.TrimSpace(value))
}

// appendIntFlag appends a positive integer flag.
func appendIntFlag(args []string, name string, value int) []string {
	if value <= 0 {
		return args
	}
	return append(args, name, strconv.Itoa(value))
}

// classifyBuildState maps existing CLI statuses into the shared lifecycle.
func classifyBuildState(status, remoteJobID, reason string) BuildState {
	normalized := strings.ToLower(strings.TrimSpace(status))
	if strings.Contains(strings.ToLower(reason), "capacity") {
		return BuildStateCapacityBlocked
	}
	switch normalized {
	case "", "idle":
		return ""
	case "running":
		if strings.TrimSpace(remoteJobID) == "" {
			return BuildStatePreparing
		}
		return BuildStateBuilding
	case "pending", "queued":
		return BuildStateQueued
	case "building":
		return BuildStateBuilding
	case "installing":
		return BuildStateInstalling
	case "launching":
		return BuildStateLaunching
	case "verifying":
		return BuildStateVerifying
	case "success", "completed":
		return BuildStateSuccess
	case "cancelled", "canceled":
		return BuildStateCancelled
	default:
		if strings.Contains(normalized, "fail") || strings.Contains(normalized, "error") {
			return BuildStateFailed
		}
		return BuildState(normalized)
	}
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
