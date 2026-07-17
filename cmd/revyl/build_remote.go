// Package main provides the remote build command for the Revyl CLI.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var remoteBuildPollInterval = 3 * time.Second

type remoteBuildOptions struct {
	Platform       string
	AppID          string
	Version        string
	Image          string
	Env            map[string]string
	Secrets        []string
	SetCurrent     bool
	Clean          bool
	JSON           bool
	Wait           bool
	IncludeDirty   bool
	CommittedOnly  bool
	LegacyUpload   bool
	TimeoutSeconds *int
}

type remoteBuildPlatformConfig struct {
	Platform    string
	PlatformKey string
	Command     string
	Commands    []string
	Setup       string
	Output      string
	Image       string
	Scheme      string
	AppID       string
	Source      config.BuildSource
	Env         map[string]string
	Secrets     []string
	Caches      []config.BuildCache
	// TimeoutSeconds is the optional build.platforms.<PlatformKey>.timeout, nil
	// when unset so the trigger request omits it and the server default applies.
	TimeoutSeconds *int
}

// runBuildRemote is retained for older internal callers. The public UX is
// `revyl build --remote --platform ios|android`.
func runBuildRemote(cmd *cobra.Command, args []string) error {
	if v, _ := cmd.Flags().GetBool("json"); v {
		remoteJSONFlag = true
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		remoteJSONFlag = true
	}
	if remoteJSONFlag {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	return runRemoteBuildWithOptions(cmd, apiKey, remoteBuildOptions{
		Platform:      remotePlatformFlag,
		AppID:         remoteAppFlag,
		Version:       remoteVersionFlag,
		SetCurrent:    remoteSetCurrFlag,
		Clean:         remoteCleanFlag,
		JSON:          remoteJSONFlag,
		Wait:          true,
		IncludeDirty:  !remoteCommittedOnly,
		CommittedOnly: remoteCommittedOnly,
	})
}

// runRemoteBuild packages source, uploads it, triggers a remote build on a
// Revyl cloud build runner, and polls until completion.
func runRemoteBuild(cmd *cobra.Command, apiKey string) error {
	includeDirty, _ := cmd.Flags().GetBool("include-dirty")
	platform := uploadPlatformFlag
	if strings.TrimSpace(platform) == "" {
		platform = "ios"
	}
	return runRemoteBuildWithOptions(cmd, apiKey, remoteBuildOptions{
		Platform:      platform,
		AppID:         uploadAppFlag,
		Version:       buildVersion,
		SetCurrent:    buildSetCurr,
		Clean:         uploadCleanFlag,
		JSON:          buildUploadJSON,
		Wait:          true,
		IncludeDirty:  includeDirty,
		CommittedOnly: !includeDirty,
		LegacyUpload:  true,
	})
}

func runRemoteBuildWithOptions(cmd *cobra.Command, apiKey string, opts remoteBuildOptions) error {
	ctx := cmd.Context()
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	debugOutput := ui.IsDebugMode()
	interactiveOutput := !opts.JSON

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	resolved, err := resolveRemoteBuildPlatform(cwd, opts.Platform, opts.AppID)
	if err != nil {
		if opts.JSON {
			printRemoteBuildJSON(remoteBuildJSONResult{
				Status:   "failed",
				Platform: opts.Platform,
				Error:    err.Error(),
				Phase:    "configuration",
			})
		}
		return err
	}
	resolved.Env = mergeRemoteBuildEnv(resolved.Env, opts.Env)
	resolved.Secrets, err = mergeBuildSecretRefs(resolved.Secrets, opts.Secrets)
	if err != nil {
		return err
	}
	if err := validateBuildEnvSecretCollisions(resolved.Env, resolved.Secrets); err != nil {
		return err
	}
	// --timeout (opts.TimeoutSeconds) wins over the resolved platform key's
	// build.platforms.<key>.timeout; both nil means the server default applies.
	if opts.TimeoutSeconds == nil {
		opts.TimeoutSeconds = resolved.TimeoutSeconds
	}
	appID, err := uuid.Parse(strings.TrimSpace(resolved.AppID))
	if err != nil {
		return fmt.Errorf("app id must be a valid UUID: %w", err)
	}
	// Remote builds run on a shared pool of Revyl sandbox build runners. There
	// is no org-scoped capacity to pre-flight; the enqueue call is authoritative
	// and surfaces a clear error if the pool is full or unavailable.
	if debugOutput {
		ui.PrintInfo("Starting remote %s build for app %s", resolved.Platform, resolved.AppID)
	} else if interactiveOutput {
		ui.PrintInfo("Creating remote %s build for app %s", resolved.Platform, resolved.AppID)
		ui.Println()
	}

	var uploadResp *api.RemoteBuildSourceUploadResponse
	var repoSource *config.BuildSource
	var sourcePatchKey string
	if remoteBuildUsesGitSource(resolved.Source) {
		normalized := normalizeRemoteGitSource(resolved.Source)
		repoSource = &normalized
		if debugOutput {
			ui.PrintInfo("Using repo-backed Git source: %s", normalized.RepoURL)
		}
		if debugOutput && normalized.Ref != "" {
			ui.PrintInfo("Git ref: %s", normalized.Ref)
		}
		if debugOutput && normalized.Subdir != "" {
			ui.PrintInfo("Git subdir: %s", normalized.Subdir)
		}
		if dirty, count := checkDirtyTree(cwd); dirty {
			if opts.CommittedOnly || !opts.IncludeDirty {
				ui.PrintWarning("%d file(s) have uncommitted changes and will NOT be included in the remote build.", count)
			} else {
				patchPath, empty, err := createRepoBackedSourcePatch(cwd)
				if err != nil {
					return fmt.Errorf("failed to create repo-backed source patch: %w", err)
				}
				defer os.Remove(patchPath)
				if empty {
					ui.PrintWarning("Working tree is dirty, but no tracked diff was found for the repo-backed source patch.")
				} else {
					patchResp, err := uploadRemoteBuildSourceFile(ctx, client, appID, "source.patch", patchPath)
					if err != nil {
						return fmt.Errorf("failed to upload repo-backed source patch: %w", err)
					}
					sourcePatchKey = patchResp.SourceKey
					if interactiveOutput {
						ui.PrintSuccess("Uploaded source patch to Revyl")
					}
				}
			}
		}
	} else {
		// ── 4. Package source via git archive ────────────────────────
		if debugOutput {
			ui.PrintInfo("Packaging source code…")
		}

		if dirty, count := checkDirtyTree(cwd); dirty {
			if opts.CommittedOnly || !opts.IncludeDirty {
				ui.PrintWarning("%d file(s) have uncommitted changes and will NOT be included in the remote build.", count)
				if opts.LegacyUpload {
					ui.PrintWarning("Commit your changes first, or use --include-dirty to proceed anyway.")
				}
			}
			if opts.LegacyUpload && !opts.IncludeDirty {
				return fmt.Errorf("uncommitted changes detected; commit or pass --include-dirty")
			}
		}

		if !debugOutput && interactiveOutput {
			ui.StartSpinner("Compressing project files")
		}

		var archivePath string
		compressStart := time.Now()
		if opts.IncludeDirty && !opts.CommittedOnly {
			archivePath, err = createSourceArchiveIncludingWorkingTree(cwd)
		} else {
			archivePath, err = createSourceArchive(cwd)
		}
		if !debugOutput && interactiveOutput {
			ui.StopSpinner()
		}
		if err != nil {
			return fmt.Errorf("failed to package source: %w", err)
		}
		defer os.Remove(archivePath)

		archiveInfo, _ := os.Stat(archivePath)
		sizeMB := float64(archiveInfo.Size()) / (1024 * 1024)
		if debugOutput {
			ui.PrintInfo("Source archive: %.1f MB", sizeMB)
		} else if interactiveOutput {
			ui.PrintSuccess("Compressed project files %s (%.1f MB)", formatBuildProgressDuration(time.Since(compressStart)), sizeMB)
		}

		if sizeMB > 500 {
			return fmt.Errorf("source archive too large (%.0f MB). Max 500 MB", sizeMB)
		}

		// ── 5. Get presigned upload URL ──────────────────────────────
		if debugOutput {
			ui.PrintInfo("Uploading source to Revyl…")
		} else if interactiveOutput {
			ui.StartSpinner("Uploading to Revyl")
		}
		uploadStart := time.Now()
		uploadResp, err = client.GetRemoteBuildUploadURL(ctx, appID, "source.tar.gz", archiveInfo.Size())
		if err != nil {
			if !debugOutput && interactiveOutput {
				ui.StopSpinner()
			}
			return fmt.Errorf("failed to get upload URL: %w", err)
		}

		// ── 6. Upload source archive to S3 via presigned POST ────────
		var uploadFields map[string]string
		if uploadResp.UploadFields != nil {
			uploadFields = *uploadResp.UploadFields
		}
		if err := client.UploadFileToPresignedPost(ctx, uploadResp.UploadUrl, uploadFields, archivePath); err != nil {
			if !debugOutput && interactiveOutput {
				ui.StopSpinner()
			}
			return fmt.Errorf("failed to upload source: %w", err)
		}

		if !debugOutput && interactiveOutput {
			ui.StopSpinner()
			ui.PrintSuccess("Uploaded to Revyl %s", formatBuildProgressDuration(time.Since(uploadStart)))
		} else if debugOutput {
			ui.PrintSuccess("Source uploaded")
		}
	}

	// ── 7. Trigger remote build ──────────────────────────────────
	if debugOutput {
		ui.PrintInfo("Triggering remote build…")
	} else if interactiveOutput {
		ui.StartSpinner("Triggering remote build job")
	}
	setCurrent := opts.SetCurrent
	source, err := remoteBuildRequestSource(repoSource, uploadedSourceKey(uploadResp), sourcePatchKey)
	if err != nil {
		return err
	}
	triggerReq := &api.RemoteBuildRequest{
		Source:       source,
		Config:       remoteBuildConfigFromResolved(appID, resolved),
		CleanBuild:   boolPtrOrNil(opts.Clean),
		Version:      stringPtrOrNil(opts.Version),
		Image:        stringPtrOrNil(opts.Image),
		SetAsCurrent: &setCurrent,
	}
	triggerResp, err := client.TriggerRemoteBuild(ctx, triggerReq, opts.TimeoutSeconds)
	if !debugOutput && interactiveOutput {
		ui.StopSpinner()
	}
	if err != nil {
		return fmt.Errorf("failed to trigger build: %w", err)
	}

	jobID := triggerResp.BuildJobId
	if interactiveOutput {
		if !debugOutput {
			ui.Println()
		}
		printRemoteBuildStarted(devMode, appID.String(), jobID)
		if !debugOutput && opts.Wait {
			ui.Println()
		}
	}

	if !opts.Wait {
		if opts.JSON {
			printRemoteBuildJSON(remoteBuildJSONResult{
				Status:     "pending",
				Platform:   resolved.Platform,
				BuildJobID: jobID,
				AppID:      resolved.AppID,
			})
		} else {
			printRemoteBuildQueuedNextSteps(jobID)
		}
		return nil
	}

	// ── 8. Poll for status ───────────────────────────────────────
	waitCtx, stopWaitSignals := interruptibleBuildWaitContext(ctx)
	defer stopWaitSignals()
	status, err := pollRemoteBuildStatusResult(waitCtx, client, jobID, opts.JSON)
	if err != nil {
		if opts.JSON {
			result := remoteBuildFailureJSON(resolved, jobID, status, err)
			result.LogEvents = fetchRemoteBuildLogEvents(ctx, client, jobID)
			printRemoteBuildJSON(result)
		}
		return completedRemoteBuildError(resolved, jobID, status, err)
	}
	if opts.JSON {
		result := remoteBuildSuccessJSON(resolved, jobID, status)
		result.LogEvents = fetchRemoteBuildLogEvents(ctx, client, jobID)
		printRemoteBuildJSON(result)
	}

	if !opts.JSON {
		cwd, _ := os.Getwd()
		testsDir := filepath.Join(cwd, ".revyl", "tests")
		var steps []ui.NextStep
		steps = append(steps, ui.NextStep{
			Label:   "Start a device with this build:",
			Command: fmt.Sprintf("revyl device start --platform %s --app-id %s", resolved.Platform, resolved.AppID),
		})
		if aliases := config.ListLocalTestAliases(testsDir); len(aliases) > 0 {
			steps = append(steps, ui.NextStep{
				Label:   "Run a test:",
				Command: fmt.Sprintf("revyl test run %s", aliases[0]),
			})
		} else {
			steps = append(steps, ui.NextStep{
				Label:   "Create a test:",
				Command: "revyl test create <name>",
			})
		}
		ui.PrintNextSteps(steps)
	}

	return nil
}

// parseRemoteBuildEnvOverrides parses repeatable --env KEY=VALUE flags into a
// map. Only the first '=' splits, so values may contain '='.
func parseRemoteBuildEnvOverrides(flags []string) (map[string]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}

	out := make(map[string]string, len(flags))
	for _, raw := range flags {
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --env %q: expected KEY=VALUE", raw)
		}
		key = strings.TrimSpace(key)
		if !isValidRemoteBuildEnvKey(key) {
			return nil, fmt.Errorf("invalid --env %q: key %q must match [A-Za-z_][A-Za-z0-9_]*", raw, key)
		}
		out[key] = value
	}
	return out, nil
}

func isValidRemoteBuildEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return false
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func mergeRemoteBuildEnv(base, overrides map[string]string) map[string]string {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

// mergeBuildSecretRefs validates, de-duplicates, and combines configured and
// command-line build secret names.
func mergeBuildSecretRefs(base, overrides []string) ([]string, error) {
	merged := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]struct{}, len(base)+len(overrides))
	for _, raw := range append(append([]string(nil), base...), overrides...) {
		name := strings.TrimSpace(raw)
		if !isValidRemoteBuildEnvKey(name) {
			return nil, fmt.Errorf(
				"invalid build secret %q: name must match [A-Za-z_][A-Za-z0-9_]*",
				raw,
			)
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, name)
	}
	return merged, nil
}

// validateBuildEnvSecretCollisions prevents a secret value from being
// accidentally duplicated in the plaintext build environment.
func validateBuildEnvSecretCollisions(env map[string]string, secrets []string) error {
	var collisions []string
	for _, name := range secrets {
		if _, exists := env[name]; exists {
			collisions = append(collisions, name)
		}
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Strings(collisions)
	return fmt.Errorf(
		"build variables cannot be both plaintext env and encrypted secrets: %s",
		strings.Join(collisions, ", "),
	)
}

// remoteBuildTimeoutFlagSeconds validates the --timeout flag value for a
// remote build trigger. Returns nil when the flag was not set so config or
// server defaults apply.
func remoteBuildTimeoutFlagSeconds(flagSeconds int, flagChanged bool) (*int, error) {
	if !flagChanged {
		return nil, nil
	}
	if flagSeconds <= 0 {
		return nil, fmt.Errorf("--timeout must be a positive number of seconds")
	}
	return &flagSeconds, nil
}

// buildPlatformTimeoutSeconds returns the optional per-platform remote build
// timeout configured at build.platforms.<key>.timeout, or nil when unset so
// the trigger request omits timeout_seconds and the server default applies.
// The key must be the resolved platform key actually used for the build (which
// may be non-canonical, e.g. ios-release), not the raw device platform.
func buildPlatformTimeoutSeconds(platCfg config.BuildPlatform, key string) (*int, error) {
	if platCfg.Timeout == 0 {
		return nil, nil
	}
	if platCfg.Timeout < 0 {
		return nil, fmt.Errorf(
			"build.platforms.%s.timeout in .revyl/config.yaml must be a positive number of seconds",
			key,
		)
	}
	seconds := platCfg.Timeout
	return &seconds, nil
}

// interruptibleBuildWaitContext wires SIGINT/SIGTERM into a remote-build wait.
// Ctrl-C detaches from the build rather than cancelling it; cancelling the
// polling context lets the interrupted path say so ("remote build continues
// in the cloud") instead of the process dying silently. A second Ctrl-C
// force-exits as usual once stop() restores the default disposition.
func interruptibleBuildWaitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

func remoteBuildPollingInterruptedError(jobID string, jsonMode bool) error {
	if !jsonMode && !ui.IsQuietMode() {
		ui.PrintWarning("Stopped waiting; remote build continues in the cloud.")
		ui.PrintNextSteps([]ui.NextStep{
			{
				Label:   "Follow build:",
				Command: fmt.Sprintf("revyl build status %s --follow", jobID),
			},
			{
				Label:   "Cancel build:",
				Command: fmt.Sprintf("revyl build cancel %s", jobID),
			},
		})
	}
	return fmt.Errorf("interrupted while waiting for remote build")
}

func printRemoteBuildQueuedNextSteps(jobID string) {
	ui.PrintNextSteps([]ui.NextStep{
		{
			Label:   "Follow build:",
			Command: fmt.Sprintf("revyl build status %s --follow", jobID),
		},
		{
			Label:   "Cancel build:",
			Command: fmt.Sprintf("revyl build cancel %s", jobID),
		},
	})
}

func printRemoteBuildStarted(devMode bool, appID, jobID string) {
	ui.PrintSuccess("Build started")
	ui.PrintLink("  View logs", remoteBuildDashboardURL(devMode, appID, jobID))
}

func remoteBuildDashboardURL(devMode bool, appID, jobID string) string {
	base := strings.TrimRight(config.GetAppURL(devMode), "/")
	return fmt.Sprintf("%s/apps/%s/builds/%s#logs", base, appID, jobID)
}

func runBuildStatus(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	if v, _ := cmd.Flags().GetBool("json"); v {
		buildStatusJSON = true
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		buildStatusJSON = true
	}
	if buildStatusJSON {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jobID := strings.TrimSpace(args[0])

	var status *api.RemoteBuildStatusResponse
	if buildStatusFollow {
		followCtx, stopFollowSignals := interruptibleBuildWaitContext(cmd.Context())
		defer stopFollowSignals()
		status, err = pollRemoteBuildStatusResult(followCtx, client, jobID, buildStatusJSON)
	} else {
		status, err = client.GetRemoteBuildStatus(cmd.Context(), jobID)
	}
	if buildStatusJSON {
		if status != nil {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(status)
		}
		if buildStatusFollow {
			return completedRemoteBuildStatusError(jobID, status, err)
		}
		return err
	}
	if status != nil && !buildStatusFollow {
		printRemoteBuildStatusSummary(cmd.Context(), client, jobID, status)
	}
	if buildStatusFollow {
		return completedRemoteBuildStatusError(jobID, status, err)
	}
	return err
}

func printRemoteBuildStatusSummary(ctx context.Context, client *api.Client, jobID string, status *api.RemoteBuildStatusResponse) {
	if events := fetchRemoteBuildLogEvents(ctx, client, jobID); len(events) > 0 {
		ui.PrintDim("Recent logs:")
		formatter := &remoteBuildLogFormatter{}
		for _, event := range events {
			formatter.Print(event)
		}
		ui.Println()
	}
	if status.Platform != nil && strings.TrimSpace(*status.Platform) != "" {
		ui.PrintKeyValue("Platform:", strings.TrimSpace(*status.Platform))
	}
	if status.StartedAt != nil && !status.StartedAt.IsZero() {
		ui.PrintKeyValue("Started:", formatAbsoluteTime(status.StartedAt.Format(time.RFC3339Nano)))
	}
	if status.CompletedAt != nil && !status.CompletedAt.IsZero() {
		ui.PrintKeyValue("Completed:", formatAbsoluteTime(status.CompletedAt.Format(time.RFC3339Nano)))
	}
	if status.DurationMs != nil {
		ui.PrintKeyValue("Duration:", (time.Duration(*status.DurationMs) * time.Millisecond).Round(time.Second).String())
	}
	printRemoteBuildPhaseTimings(status.PhaseTimings)
	if status.Error != nil && strings.TrimSpace(*status.Error) != "" {
		ui.PrintKeyValue("Error:", strings.TrimSpace(*status.Error))
	}
	if status.VersionId != nil && strings.TrimSpace(*status.VersionId) != "" {
		ui.PrintKeyValue("Version ID:", strings.TrimSpace(*status.VersionId))
	}
	ui.PrintKeyValue("Status:", status.Status)
}

func printRemoteBuildPhaseTimings(timings *[]api.RemoteBuildPhaseTiming) {
	if timings == nil || len(*timings) == 0 {
		return
	}
	ui.Println()
	ui.PrintDim("Phase timings:")
	for _, timing := range *timings {
		phase := strings.TrimSpace(timing.Phase)
		if phase == "" {
			continue
		}
		duration := "unknown"
		if timing.DurationMs != nil {
			duration = (time.Duration(*timing.DurationMs) * time.Millisecond).Round(time.Millisecond).String()
		}
		fmt.Fprintf(os.Stderr, "  %-14s %s\n", phase+":", duration)
	}
}

type remoteBuildLogFormatter struct {
	currentStep    string
	currentCommand string
	printedAny     bool
}

func (f *remoteBuildLogFormatter) Print(event api.RemoteBuildLogEvent) {
	message := strings.TrimRight(event.Message, "\r\n")
	if strings.TrimSpace(message) == "" {
		return
	}

	switch {
	case strings.HasPrefix(message, "step:start "):
		f.currentStep = strings.TrimSpace(strings.TrimPrefix(message, "step:start "))
		f.currentCommand = ""
		return
	case message == "source:download":
		f.printPlain("Downloading source")
		return
	case message == "source:extract":
		f.printPlain("Extracting source")
		return
	case strings.HasPrefix(message, "command "):
		command := strings.TrimSpace(strings.TrimPrefix(message, "command "))
		if f.currentStep == "checkout" {
			return
		}
		f.currentCommand = command
		if f.printedAny {
			fmt.Fprintln(os.Stderr)
		}
		f.printPlain(command)
		return
	case strings.HasPrefix(message, "step:end "):
		label := f.completionLabel(message)
		fmt.Fprintf(os.Stderr, "%s\n", ui.SuccessStyle.Render("✓ "+label+" completed"+durationSuffix(message)))
		f.currentCommand = ""
		f.printedAny = true
		return
	case strings.HasPrefix(message, "step:failed "):
		label := f.completionLabel(message)
		fmt.Fprintf(os.Stderr, "%s\n", ui.ErrorStyle.Render("✗ "+label+" failed"+durationSuffix(message)))
		f.currentCommand = ""
		f.printedAny = true
		return
	}

	indent := ""
	if f.currentCommand != "" {
		indent = "  "
	}
	level := "info"
	if event.Level != nil {
		level = strings.ToLower(strings.TrimSpace(*event.Level))
	}
	switch level {
	case "warning", "warn":
		fmt.Fprintf(os.Stderr, "%s%s\n", indent, ui.WarningStyle.Render(message))
	case "error":
		fmt.Fprintf(os.Stderr, "%s%s\n", indent, ui.ErrorStyle.Render(message))
	default:
		fmt.Fprintf(os.Stderr, "%s%s\n", indent, message)
	}
	f.printedAny = true
}

func (f *remoteBuildLogFormatter) printPlain(message string) {
	fmt.Fprintln(os.Stderr, message)
	f.printedAny = true
}

func (f *remoteBuildLogFormatter) completionLabel(message string) string {
	if f.currentStep == "checkout" {
		return "Checkout"
	}
	if f.currentCommand != "" {
		return commandCompletionLabel(f.currentCommand)
	}
	fields := strings.Fields(message)
	if len(fields) >= 2 {
		return strings.TrimPrefix(fields[1], "build-")
	}
	return "Step"
}

func commandCompletionLabel(command string) string {
	for _, marker := range []string{" --", " -"} {
		if idx := strings.Index(command, marker); idx > 0 {
			return strings.TrimSpace(command[:idx])
		}
	}
	return command
}

func durationSuffix(message string) string {
	for _, field := range strings.Fields(message) {
		raw, ok := strings.CutPrefix(field, "duration_ms=")
		if !ok {
			continue
		}
		ms, err := strconv.Atoi(raw)
		if err != nil {
			break
		}
		return fmt.Sprintf(" in %.1fs", float64(ms)/1000)
	}
	return ""
}

func printRemoteBuildLogTail(ctx context.Context, client *api.Client, jobID string) {
	events := fetchRemoteBuildLogEvents(ctx, client, jobID)
	if len(events) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "\n--- Build log tail ---")
	formatter := &remoteBuildLogFormatter{}
	for _, event := range events {
		formatter.Print(event)
	}
}

// fetchRemoteBuildLogEvents returns the latest structured log events for a
// build job, or nil when logs are unavailable — log display is best-effort.
func fetchRemoteBuildLogEvents(ctx context.Context, client *api.Client, jobID string) []api.RemoteBuildLogEvent {
	logs, err := client.GetRemoteBuildLogs(ctx, jobID, "")
	if err != nil || logs == nil || logs.Events == nil {
		return nil
	}
	return *logs.Events
}

// detectBuildCommand determines the xcodebuild command for the project.
//
// Parameters:
//   - cwd: Current working directory.
//   - platform: Target platform (only "ios" currently).
//
// Returns:
//   - buildCmd: Full xcodebuild shell command.
//   - scheme: Xcode scheme name (may be empty).
//   - setupCmd: Pre-build setup command (may be empty).
//   - error: If detection fails.
func detectBuildCommand(cwd, platform string) (string, string, string, error) {
	scheme := uploadSchemeFlag

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err == nil {
		platCfg := cfg.Build.Platforms[platform]
		if platCfg.Command != "" {
			return platCfg.Command, scheme, platCfg.Setup, nil
		}
	}

	detected, err := build.Detect(cwd)
	if err != nil {
		return "", "", "", fmt.Errorf("could not detect build system: %w", err)
	}
	if detected == nil {
		return "", "", "", fmt.Errorf("no build system detected in %s", cwd)
	}

	if platBuild, ok := detected.Platforms[platform]; ok && platBuild.Command != "" {
		cmd := platBuild.Command
		if scheme != "" {
			cmd += fmt.Sprintf(" -scheme %s", scheme)
		}
		return cmd, scheme, "", nil
	}

	if strings.EqualFold(detected.Platform, platform) && detected.Command != "" {
		cmd := detected.Command
		if scheme != "" {
			cmd += fmt.Sprintf(" -scheme %s", scheme)
		}
		return cmd, scheme, "", nil
	}

	return "", "", "", fmt.Errorf(
		"no %s build configuration found in %s%s. Add build.platforms.%s.command to .revyl/config.yaml or run 'revyl init'",
		platform, cwd, nestedProjectHint(cwd), platform,
	)
}

// resolveAppForRemoteBuild determines the app ID to use for the build,
// from flag, config, or interactive prompt.
//
// Parameters:
//   - ctx: Cancellation context.
//   - client: API client.
//   - platform: Target platform.
//
// Returns:
//   - appID: Resolved app UUID string.
//   - error: If resolution fails.
func resolveAppForRemoteBuild(ctx context.Context, client *api.Client, platform string) (string, error) {
	if uploadAppFlag != "" {
		return uploadAppFlag, nil
	}

	cwd, _ := os.Getwd()
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err == nil {
		platCfg := cfg.Build.Platforms[platform]
		if platCfg.AppID != "" {
			return platCfg.AppID, nil
		}
	}

	return "", fmt.Errorf("no app specified. Use --app <name-or-id> or configure in .revyl/config.yaml")
}

func resolveRemoteBuildPlatform(cwd, rawPlatform, appOverride string) (remoteBuildPlatformConfig, error) {
	platformOrKey := strings.TrimSpace(rawPlatform)
	if platformOrKey == "" {
		platformOrKey = "ios"
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, cfgErr := config.LoadProjectConfig(configPath)
	if cfgErr == nil {
		key := platformOrKey
		devicePlatform := platformFromKey(key)
		if normalized, err := normalizeMobilePlatform(platformOrKey, ""); err == nil {
			devicePlatform = normalized
		}

		platCfg, ok := cfg.Build.Platforms[key]
		if !ok && (devicePlatform == "ios" || devicePlatform == "android") {
			if picked := pickBestBuildPlatformKey(cfg, devicePlatform); picked != "" {
				key = picked
				platCfg = cfg.Build.Platforms[picked]
				ok = true
			}
		}
		buildCommands := platCfg.BuildCommands()
		if ok && len(buildCommands) > 0 {
			if devicePlatform != "ios" && devicePlatform != "android" {
				return remoteBuildPlatformConfig{}, fmt.Errorf("build.platforms.%s must include ios or android in its key", key)
			}
			appID := strings.TrimSpace(appOverride)
			if appID == "" {
				appID = strings.TrimSpace(platCfg.AppID)
			}
			if appID == "" {
				return remoteBuildPlatformConfig{}, fmt.Errorf("no app specified. Use --app <id> or configure build.platforms.%s.app_id in .revyl/config.yaml", key)
			}
			timeoutSeconds, err := buildPlatformTimeoutSeconds(platCfg, key)
			if err != nil {
				return remoteBuildPlatformConfig{}, err
			}
			caches := config.EffectiveBuildCaches(cfg.Build, platCfg)
			return remoteBuildPlatformConfig{
				Platform:       devicePlatform,
				PlatformKey:    key,
				Command:        strings.Join(buildCommands, " && "),
				Commands:       buildCommands,
				Setup:          strings.TrimSpace(platCfg.Setup),
				Output:         strings.TrimSpace(platCfg.Output),
				Image:          strings.TrimSpace(platCfg.Image),
				Scheme:         strings.TrimSpace(resolveRemoteBuildScheme(devicePlatform, platCfg.Scheme)),
				AppID:          appID,
				Source:         cfg.Build.Source,
				Env:            platCfg.Env,
				Secrets:        append([]string(nil), platCfg.Secrets...),
				Caches:         caches,
				TimeoutSeconds: timeoutSeconds,
			}, nil
		}
	}

	platform, err := normalizeMobilePlatform(platformOrKey, "")
	if err != nil {
		return remoteBuildPlatformConfig{}, fmt.Errorf("unknown platform/platform-key %q", platformOrKey)
	}

	detected, err := build.Detect(cwd)
	if err != nil {
		return remoteBuildPlatformConfig{}, fmt.Errorf("could not detect build system: %w", err)
	}
	if detected == nil {
		return remoteBuildPlatformConfig{}, fmt.Errorf("no build system detected in %s", cwd)
	}
	platBuild, ok := detected.Platforms[platform]
	if !ok || strings.TrimSpace(platBuild.Command) == "" {
		return remoteBuildPlatformConfig{}, fmt.Errorf(
			"no %s build configuration found in %s%s. Add build.platforms.%s.command to .revyl/config.yaml or run 'revyl init'",
			platform, cwd, nestedProjectHint(cwd), platform,
		)
	}
	appID := strings.TrimSpace(appOverride)
	if appID == "" {
		return remoteBuildPlatformConfig{}, fmt.Errorf("no app specified. Use --app <id> or configure build.platforms.%s.app_id in .revyl/config.yaml", platform)
	}
	// Auto-detected builds can still carry a config-only timeout entry.
	var timeoutSeconds *int
	if cfgErr == nil {
		if platCfg, hasCfg := cfg.Build.Platforms[platform]; hasCfg {
			timeoutSeconds, err = buildPlatformTimeoutSeconds(platCfg, platform)
			if err != nil {
				return remoteBuildPlatformConfig{}, err
			}
		}
	}
	return remoteBuildPlatformConfig{
		Platform:       platform,
		PlatformKey:    platform,
		Command:        strings.TrimSpace(platBuild.Command),
		Commands:       []string{strings.TrimSpace(platBuild.Command)},
		Output:         strings.TrimSpace(platBuild.Output),
		Scheme:         strings.TrimSpace(resolveRemoteBuildScheme(platform, "")),
		AppID:          appID,
		TimeoutSeconds: timeoutSeconds,
	}, nil
}

func resolveRemoteBuildScheme(platform, configured string) string {
	if platform != "ios" {
		return ""
	}
	if strings.TrimSpace(uploadSchemeFlag) != "" {
		return strings.TrimSpace(uploadSchemeFlag)
	}
	return strings.TrimSpace(configured)
}

func remoteBuildUsesGitSource(source config.BuildSource) bool {
	return strings.EqualFold(strings.TrimSpace(source.Type), "git") && strings.TrimSpace(source.RepoURL) != ""
}

func normalizeRemoteGitSource(source config.BuildSource) config.BuildSource {
	source.Type = strings.ToLower(strings.TrimSpace(source.Type))
	source.RepoURL = strings.TrimSpace(source.RepoURL)
	source.Ref = strings.TrimSpace(source.Ref)
	source.Subdir = strings.Trim(strings.TrimSpace(source.Subdir), "/")
	return source
}

func defaultRemoteArtifactType(platform string) string {
	if platform == "android" {
		return "apk"
	}
	return "app"
}

type remoteBuildJSONResult struct {
	Status             string                       `json:"status"`
	Platform           string                       `json:"platform,omitempty"`
	BuildJobID         string                       `json:"build_job_id,omitempty"`
	BuildVersionID     string                       `json:"build_version_id,omitempty"`
	Version            string                       `json:"version,omitempty"`
	ArtifactType       string                       `json:"artifact_type,omitempty"`
	PackageID          string                       `json:"package_id,omitempty"`
	AppID              string                       `json:"app_id,omitempty"`
	LogEvents          []api.RemoteBuildLogEvent    `json:"log_events,omitempty"`
	Phase              string                       `json:"phase,omitempty"`
	PhaseTimings       []api.RemoteBuildPhaseTiming `json:"phase_timings,omitempty"`
	Error              string                       `json:"error,omitempty"`
	SuggestedFix       string                       `json:"suggested_fix,omitempty"`
	CandidateArtifacts []string                     `json:"candidate_artifacts,omitempty"`
}

func remoteBuildSuccessJSON(resolved remoteBuildPlatformConfig, jobID string, status *api.RemoteBuildStatusResponse) remoteBuildJSONResult {
	result := remoteBuildJSONResult{
		Status:       "success",
		Platform:     resolved.Platform,
		BuildJobID:   jobID,
		ArtifactType: defaultRemoteArtifactType(resolved.Platform),
		AppID:        resolved.AppID,
	}
	if status == nil {
		return result
	}
	if status.VersionId != nil {
		result.BuildVersionID = strings.TrimSpace(*status.VersionId)
	}
	if status.Version != nil {
		result.Version = strings.TrimSpace(*status.Version)
	}
	if status.ArtifactType != nil && strings.TrimSpace(*status.ArtifactType) != "" {
		result.ArtifactType = strings.TrimSpace(*status.ArtifactType)
	}
	if status.PackageId != nil {
		result.PackageID = strings.TrimSpace(*status.PackageId)
	}
	if status.AppId != nil && strings.TrimSpace(*status.AppId) != "" {
		result.AppID = strings.TrimSpace(*status.AppId)
	}
	result.PhaseTimings = remoteBuildPhaseTimings(status)
	return result
}

func remoteBuildFailureJSON(resolved remoteBuildPlatformConfig, jobID string, status *api.RemoteBuildStatusResponse, err error) remoteBuildJSONResult {
	result := remoteBuildJSONResult{
		Status:     "failed",
		Platform:   resolved.Platform,
		BuildJobID: jobID,
		AppID:      resolved.AppID,
		Error:      err.Error(),
	}
	if status == nil {
		return result
	}
	if status.Status == "cancelled" {
		result.Status = "cancelled"
	}
	if status.Error != nil && strings.TrimSpace(*status.Error) != "" {
		result.Error = strings.TrimSpace(*status.Error)
	}
	if status.Phase != nil {
		result.Phase = strings.TrimSpace(*status.Phase)
	}
	if status.SuggestedFix != nil {
		result.SuggestedFix = strings.TrimSpace(*status.SuggestedFix)
	}
	if status.CandidateArtifacts != nil {
		result.CandidateArtifacts = append([]string(nil), (*status.CandidateArtifacts)...)
	}
	if status.ArtifactType != nil {
		result.ArtifactType = strings.TrimSpace(*status.ArtifactType)
	}
	if status.PackageId != nil {
		result.PackageID = strings.TrimSpace(*status.PackageId)
	}
	if status.VersionId != nil {
		result.BuildVersionID = strings.TrimSpace(*status.VersionId)
	}
	if status.Version != nil {
		result.Version = strings.TrimSpace(*status.Version)
	}
	result.PhaseTimings = remoteBuildPhaseTimings(status)
	return result
}

func completedRemoteBuildError(resolved remoteBuildPlatformConfig, jobID string, status *api.RemoteBuildStatusResponse, err error) error {
	if err == nil {
		return nil
	}
	completion, ok := remoteBuildCompletion(jobID, status)
	if !ok {
		return err
	}
	if resolved.Platform != "" {
		completion.Properties["remote_build_platform"] = resolved.Platform
	}
	if resolved.AppID != "" {
		completion.Properties["remote_build_app_id"] = resolved.AppID
	}
	return analytics.CompletedWithExitCode(err, completion)
}

func completedRemoteBuildStatusError(jobID string, status *api.RemoteBuildStatusResponse, err error) error {
	if err == nil {
		return nil
	}
	completion, ok := remoteBuildCompletion(jobID, status)
	if !ok {
		return err
	}
	return analytics.CompletedWithExitCode(err, completion)
}

func remoteBuildCompletion(jobID string, status *api.RemoteBuildStatusResponse) (analytics.CommandCompletion, bool) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" || status == nil {
		return analytics.CommandCompletion{}, false
	}
	statusText := strings.TrimSpace(status.Status)
	switch statusText {
	case "failed", "cancelled":
	default:
		return analytics.CommandCompletion{}, false
	}

	props := map[string]interface{}{
		"remote_build_job_id": jobID,
		"remote_build_status": statusText,
	}
	if status.Phase != nil && strings.TrimSpace(*status.Phase) != "" {
		props["remote_build_phase"] = strings.TrimSpace(*status.Phase)
	}
	if status.VersionId != nil && strings.TrimSpace(*status.VersionId) != "" {
		props["remote_build_version_id"] = strings.TrimSpace(*status.VersionId)
	}
	if status.AppId != nil && strings.TrimSpace(*status.AppId) != "" {
		props["remote_build_app_id"] = strings.TrimSpace(*status.AppId)
	}
	if status.Platform != nil && strings.TrimSpace(*status.Platform) != "" {
		props["remote_build_platform"] = strings.TrimSpace(*status.Platform)
	}

	return analytics.CommandCompletion{
		ExitCode:     1,
		Domain:       "remote_build",
		DomainStatus: statusText,
		Properties:   props,
	}, true
}

func remoteBuildPhaseTimings(status *api.RemoteBuildStatusResponse) []api.RemoteBuildPhaseTiming {
	if status == nil || status.PhaseTimings == nil {
		return nil
	}
	return append([]api.RemoteBuildPhaseTiming(nil), (*status.PhaseTimings)...)
}

func printRemoteBuildJSON(result remoteBuildJSONResult) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
}

// createSourceArchive runs git archive to create a tar.gz of the project
// directory at HEAD.  When cwd is a subdirectory of a larger repo (e.g. a
// monorepo), only the subtree rooted at cwd is archived so the build
// command finds project files at the archive root.
//
// Parameters:
//   - cwd: Directory to archive (must be inside a git repo).
//
// Returns:
//   - archivePath: Path to the created tar.gz file.
//   - error: If git archive fails.
func createSourceArchive(cwd string) (string, error) {
	tmpFile, err := os.CreateTemp("", "revyl-source-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpFile.Close()

	prefixCmd := exec.Command("git", "rev-parse", "--show-prefix")
	prefixCmd.Dir = cwd
	prefixOut, err := prefixCmd.Output()
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to determine git subdirectory: %w", err)
	}
	prefix := strings.TrimSpace(string(prefixOut))

	// HEAD:<prefix> archives just the subtree at that path with files at the
	// root.  When prefix is empty the cwd IS the repo root so plain HEAD works.
	treeish := "HEAD"
	if prefix != "" {
		treeish = "HEAD:" + prefix
	}

	// Resolve the repo root so git archive resolves tree-ish paths correctly.
	// Running from a subdirectory causes HEAD:<prefix> to double the path
	// (e.g. HEAD:sub/dir/ resolved from sub/dir/ becomes sub/dir/sub/dir/),
	// which silently produces an empty archive in monorepos.
	toplevelCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	toplevelCmd.Dir = cwd
	toplevelOut, err := toplevelCmd.Output()
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to determine git root: %w", err)
	}
	repoRoot := strings.TrimSpace(string(toplevelOut))

	cmd := exec.Command("git", "archive", "--format=tar.gz", "-o", tmpFile.Name(), treeish)
	cmd.Dir = repoRoot

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("git archive failed: %w\n%s", err, stderr.String())
	}

	info, err := os.Stat(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to stat archive: %w", err)
	}
	if info.Size() < 100 {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("git archive produced an empty or near-empty archive (%d bytes); ensure project files are committed", info.Size())
	}

	return tmpFile.Name(), nil
}

func createRepoBackedSourcePatch(cwd string) (string, bool, error) {
	tmpFile, err := os.CreateTemp("", "revyl-source-patch-*.patch")
	if err != nil {
		return "", false, fmt.Errorf("failed to create temp patch: %w", err)
	}
	defer tmpFile.Close()

	cmd := exec.Command("git", "diff", "--binary", "HEAD")
	cmd.Dir = cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", false, fmt.Errorf("git diff failed: %w\n%s", err, stderr.String())
	}
	if _, err := tmpFile.Write(out); err != nil {
		os.Remove(tmpFile.Name())
		return "", false, fmt.Errorf("failed to write patch: %w", err)
	}
	return tmpFile.Name(), len(bytes.TrimSpace(out)) == 0, nil
}

func uploadRemoteBuildSourceFile(ctx context.Context, client *api.Client, appID uuid.UUID, filename, path string) (*api.RemoteBuildSourceUploadResponse, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat %s: %w", filename, err)
	}
	resp, err := client.GetRemoteBuildUploadURL(ctx, appID, filename, info.Size())
	if err != nil {
		return nil, err
	}
	var fields map[string]string
	if resp.UploadFields != nil {
		fields = *resp.UploadFields
	}
	if err := client.UploadFileToPresignedPost(ctx, resp.UploadUrl, fields, path); err != nil {
		return nil, err
	}
	return resp, nil
}

// createSourceArchiveIncludingWorkingTree creates a tar.gz from the current
// working tree instead of HEAD. It includes tracked files with dirty edits plus
// untracked files that are not ignored by git. Deleted tracked files are omitted
// so the archive reflects the filesystem the developer is actually editing.
func createSourceArchiveIncludingWorkingTree(cwd string) (string, error) {
	files, err := listWorkingTreeSnapshotFiles(cwd)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no source files found to archive")
	}

	tmpFile, err := os.CreateTemp("", "revyl-dev-source-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tmpFile.Close()

	gz := gzip.NewWriter(tmpFile)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, rel := range files {
		fullPath := filepath.Join(cwd, rel)
		info, statErr := os.Lstat(fullPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to stat %s: %w", rel, statErr)
		}
		if info.IsDir() {
			continue
		}

		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(fullPath)
			if readErr != nil {
				os.Remove(tmpFile.Name())
				return "", fmt.Errorf("failed to read symlink %s: %w", rel, readErr)
			}
			linkTarget = target
		}

		header, headerErr := tar.FileInfoHeader(info, linkTarget)
		if headerErr != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to create tar header for %s: %w", rel, headerErr)
		}
		header.Name = filepath.ToSlash(rel)
		if writeErr := tw.WriteHeader(header); writeErr != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to write tar header for %s: %w", rel, writeErr)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		f, openErr := os.Open(fullPath)
		if openErr != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to open %s: %w", rel, openErr)
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to archive %s: %w", rel, copyErr)
		}
		if closeErr != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to close %s: %w", rel, closeErr)
		}
	}

	if err := tw.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to close tar archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to close gzip archive: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to close source archive: %w", err)
	}

	info, err := os.Stat(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to stat archive: %w", err)
	}
	if info.Size() < 100 {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("working tree archive produced an empty or near-empty archive (%d bytes)", info.Size())
	}

	return tmpFile.Name(), nil
}

func listWorkingTreeSnapshotFiles(cwd string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		files, fallbackErr := listStandaloneSourceFiles(cwd)
		if fallbackErr != nil {
			return nil, fmt.Errorf("failed to list git-tracked source files: %w", err)
		}
		return files, nil
	}

	seen := map[string]bool{}
	files := []string{}
	for _, raw := range bytes.Split(out, []byte{0}) {
		rel := strings.TrimSpace(string(raw))
		if rel == "" || seen[rel] {
			continue
		}
		if filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			return nil, fmt.Errorf("unsafe source path from git: %s", rel)
		}
		fullPath := filepath.Join(cwd, rel)
		info, statErr := os.Lstat(fullPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, fmt.Errorf("failed to inspect %s: %w", rel, statErr)
		}
		if info.IsDir() {
			continue
		}
		seen[rel] = true
		files = append(files, rel)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return listStandaloneSourceFiles(cwd)
	}
	return files, nil
}

func listStandaloneSourceFiles(cwd string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(cwd, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(cwd, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if shouldSkipStandaloneSourcePath(rel, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list standalone source files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func shouldSkipStandaloneSourcePath(rel string, entry os.DirEntry) bool {
	base := pathBase(rel)
	if base == ".DS_Store" || base == "MODULE.bazel.lock" {
		return true
	}
	if strings.HasSuffix(rel, ".xcuserstate") {
		return true
	}

	if entry.IsDir() {
		switch base {
		case ".git", ".gradle", ".kotlin", ".dart_tool", ".expo", ".next", "build", "DerivedData", "dist", "node_modules", "Pods":
			return true
		}
		if strings.HasSuffix(rel, ".xcuserdata") {
			return true
		}
	}

	switch rel {
	case ".revyl/.dev-push-manifest.json",
		".revyl/.dev-status.json",
		".revyl/device-sessions.json":
		return true
	}
	return strings.HasPrefix(rel, ".revyl/dev-sessions/")
}

func pathBase(rel string) string {
	if idx := strings.LastIndex(rel, "/"); idx >= 0 {
		return rel[idx+1:]
	}
	return rel
}

// stringPtrOrNil returns a pointer to s if non-empty, or nil.
func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// boolPtrOrNil returns a pointer to b if true, or nil (omit from JSON).
func boolPtrOrNil(b bool) *bool {
	if !b {
		return nil
	}
	return &b
}

func remoteBuildRequestSource(repoSource *config.BuildSource, sourceKey string, patchKey string) (api.RemoteBuildRequest_Source, error) {
	var source api.RemoteBuildRequest_Source
	if repoSource != nil {
		lfs := repoSource.LFS
		if err := source.FromRemoteBuildGitSource(api.RemoteBuildGitSource{
			RepoUrl:  repoSource.RepoURL,
			Ref:      stringPtrOrNil(repoSource.Ref),
			Lfs:      &lfs,
			PatchKey: stringPtrOrNil(patchKey),
		}); err != nil {
			return source, err
		}
		return source, nil
	}

	sourceKey = strings.TrimSpace(sourceKey)
	if sourceKey == "" {
		return source, fmt.Errorf("remote build source is required")
	}
	if err := source.FromRemoteBuildArchiveSource(api.RemoteBuildArchiveSource{
		Key: sourceKey,
	}); err != nil {
		return source, err
	}
	return source, nil
}

func uploadedSourceKey(resp *api.RemoteBuildSourceUploadResponse) string {
	if resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.SourceKey)
}

// checkDirtyTree reports whether the git working tree has uncommitted
// (modified, staged, or untracked) files.  Returns false on any git
// error so the build can proceed optimistically.
//
// Parameters:
//   - cwd: Directory inside the git repo.
//
// Returns:
//   - dirty: true if uncommitted changes exist.
//   - count: number of dirty files detected.
func checkDirtyTree(cwd string) (bool, int) {
	cmd := exec.Command("git", "status", "--porcelain", ".")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return false, 0
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return false, 0
	}
	lines := strings.Split(trimmed, "\n")
	return true, len(lines)
}
