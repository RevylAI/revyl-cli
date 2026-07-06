package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/sigutil"
	"github.com/revyl/cli/internal/ui"
)

type remoteDevBuildResult struct {
	jobID     string
	versionID string
	version   string
	duration  time.Duration
}

func validateRemoteDevStartFlags() error {
	if _, err := normalizeMobilePlatform(devStartPlatform, "ios"); err != nil {
		return err
	}
	if devStartNoBuild {
		return fmt.Errorf("use either --remote or --no-build, not both")
	}
	if strings.TrimSpace(devStartBuildVerID) != "" {
		return fmt.Errorf("use either --remote or --build-version-id, not both")
	}
	if strings.TrimSpace(devStartTunnelURL) != "" {
		return fmt.Errorf("use either --remote or --tunnel, not both")
	}
	return nil
}

// runDevRemoteRebuildOnly starts a native dev loop where all builds run on
// Revyl's remote build runner and the active device session only handles
// install, launch, streaming, and interaction.
func runDevRemoteRebuildOnly(cmd *cobra.Command, cfg *config.ProjectConfig, configPath, cwd, apiKey, ctxName string) error {
	if err := validateRemoteDevStartFlags(); err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	requestedPlatform, _ := normalizeMobilePlatform(devStartPlatform, "ios")
	platformKey, devicePlatform, err := resolveRebuildLoopPlatform(
		cfg,
		requestedPlatform,
		strings.TrimSpace(devStartPlatformKey),
		cmd.Flags().Changed("platform"),
	)
	if err != nil {
		return err
	}
	platCfg := cfg.Build.Platforms[platformKey]
	if len(platCfg.BuildCommands()) == 0 {
		return fmt.Errorf("build.platforms.%s.command or build.platforms.%s.commands is required for revyl dev --remote", platformKey, platformKey)
	}

	ctxName, err = resolveDevStartContextName(cwd, getDevContextFlag(cmd), devicePlatform)
	if err != nil {
		return err
	}
	if printIfDevStartContextAlreadyRunning(cwd, ctxName) {
		return nil
	}

	timeout := devStartTimeout
	if !cmd.Flags().Changed("timeout") {
		timeout = config.EffectiveTimeoutSeconds(cfg, timeout)
	}
	if timeout <= 0 {
		timeout = 300
	}

	openBrowser := devStartOpen
	if !cmd.Flags().Changed("open") {
		openBrowser = config.EffectiveOpenBrowser(cfg)
	}
	if devStartNoOpen {
		openBrowser = false
	}

	ui.PrintBanner(version)
	ui.Println()
	ui.PrintInfo("Remote dev loop (%s / %s)", cfg.Build.System, platformKey)
	ui.PrintDim("Builds run on a Revyl build runner; this session keeps the device session alive.")
	ui.Println()

	if strings.TrimSpace(platCfg.AppID) == "" {
		_, err := selectOrCreateAppForPlatform(cmd, client, cfg, configPath, platformKey, devicePlatform)
		if err != nil {
			return err
		}
		cfg, err = config.LoadProjectConfig(configPath)
		if err != nil {
			return fmt.Errorf("failed to reload config: %w", err)
		}
		platCfg = cfg.Build.Platforms[platformKey]
		if strings.TrimSpace(platCfg.AppID) == "" {
			return fmt.Errorf("build.platforms.%s.app_id is required", platformKey)
		}
	}
	appID := strings.TrimSpace(platCfg.AppID)
	buildCaches := config.EffectiveBuildCaches(cfg.Build, platCfg)

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	var interrupted int32
	stopper := newDevLoopStopper(cwd, ctxName, cancel, &interrupted)

	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)
	stopSigHandler := startDevLoopSignalHandler(sigChan, stopper)
	defer stopSigHandler()

	remoteBuild, err := runRemoteDevBuild(ctx, client, platCfg, buildCaches, platformKey, devicePlatform, appID, cwd)
	if err != nil {
		if stopper.IsUserCanceled(err) {
			return nil
		}
		return err
	}

	buildDetail, err := client.GetBuildVersionDownloadURL(ctx, remoteBuild.versionID)
	if err != nil {
		if stopper.IsUserCanceled(err) {
			return nil
		}
		return fmt.Errorf("could not resolve remote build download URL: %w", err)
	}
	bundleID := strings.TrimSpace(buildDetail.PackageName)

	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}

	var session *mcppkg.DeviceSession
	sessionOwned := true
	if savedCtx, _ := loadDevContext(cwd, ctxName); savedCtx != nil && savedCtx.SessionID != "" {
		reuse := tryReuseDevContextSession(ctx, deviceMgr, savedCtx, devicePlatform)
		if reuse != nil {
			session = reuse.Session
			sessionOwned = reuse.SessionOwned
			warnLaunchVarsIgnoredForReusedDevSession()
		}
	}

	if session == nil {
		ui.PrintInfo("Starting cloud device session...")
		_, session, err = startDevSessionWithProgress(
			ctx,
			deviceMgr,
			withDevStartLaunchVars(mcppkg.StartSessionOptions{
				Platform:       devicePlatform,
				AppID:          appID,
				BuildVersionID: remoteBuild.versionID,
				AppURL:         strings.TrimSpace(buildDetail.DownloadURL),
				AppPackage:     bundleID,
				IdleTimeout:    time.Duration(timeout) * time.Second,
			}),
			30*time.Second,
			nil,
		)
		if err != nil {
			if stopper.IsUserCanceled(err) {
				return nil
			}
			return err
		}
	}

	if sessionOwned {
		defer func() {
			if stopErr := deviceMgr.StopSession(context.Background(), session.Index); stopErr != nil {
				if !isNoSessionAtIndexError(stopErr, session.Index) {
					ui.PrintWarning("Failed to stop device session: %v", stopErr)
				}
			}
		}()
	}

	installedBundleID, installDuration, err := installRemoteDevBuild(ctx, deviceMgr, session, buildDetail, bundleID)
	if err != nil {
		if stopper.IsUserCanceled(err) {
			return nil
		}
		return err
	}
	if installedBundleID != "" {
		bundleID = installedBundleID
	}
	tryLaunchInstalledApp(ctx, deviceMgr, session.Index, devicePlatform, bundleID, "", "")

	deviceMgr.StopIdleTimer(session.Index)
	viewerURL := devSessionViewerURL(session, devMode)

	pidPath := devCtxPIDPath(cwd, ctxName)
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return fmt.Errorf("failed to create dev context directory: %w", err)
	}
	startNonce := time.Now().UnixNano()
	_ = writeDevCtxPIDFile(pidPath, os.Getpid(), startNonce)
	defer os.Remove(pidPath)

	devCtx := &DevContext{
		Name:          ctxName,
		Platform:      devicePlatform,
		PlatformKey:   platformKey,
		Provider:      "remote-xcode",
		SessionID:     session.SessionID,
		SessionIndex:  session.Index,
		SessionOwned:  sessionOwned,
		ViewerURL:     viewerURL,
		PID:           os.Getpid(),
		StartedAtNano: startNonce,
		State:         devContextStateRunning,
		CreatedAt:     time.Now(),
		LastActivity:  time.Now(),
	}
	_ = saveDevContext(cwd, devCtx)
	_ = setCurrentDevContext(cwd, ctxName)
	defer func() {
		devCtx.State = devContextStateStopped
		devCtx.PID = 0
		if sessionOwned {
			devCtx.SessionID = ""
			devCtx.SessionIndex = 0
			devCtx.ViewerURL = ""
		}
		_ = saveDevContext(cwd, devCtx)
	}()

	statusPath := devCtxStatusPath(cwd, ctxName)
	initialResult := devRebuildResult{
		buildMode:       "remote",
		buildDuration:   remoteBuild.duration,
		pushDuration:    installDuration,
		elapsed:         remoteBuild.duration + installDuration,
		newBundleID:     bundleID,
		remoteJobID:     remoteBuild.jobID,
		remoteVersionID: remoteBuild.versionID,
		remoteVersion:   remoteBuild.version,
	}
	writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, 0, false, initialResult)

	cockpitRebuilds := make(chan struct{}, 1)
	cockpit, cockpitErr := startDevCockpitForContext(ctx, cwd, ctxName, viewerURL, true, cockpitRebuilds, stopper.RequestStop)
	cockpitURL := ""
	if cockpitErr != nil {
		ui.PrintWarning("Local cockpit unavailable: %v", cockpitErr)
	} else {
		cockpitURL = cockpit.URL
		defer cockpit.Close(context.Background())
	}

	ui.Println()
	ui.PrintSuccess("Remote dev loop ready")
	printDevBrowserLinks(cockpitURL, viewerURL)
	ui.PrintInfo("Installed remote build: %s", strings.TrimSpace(buildDetail.Version))
	if identifier := formatInstalledAppIdentifier(devicePlatform, bundleID); identifier != "" {
		ui.PrintInfo("Installed app: %s", identifier)
	}
	ui.Println()
	printNewTerminalHints(ctxName, session.Index)
	ui.Println()

	sigusr1 := make(chan os.Signal, 1)
	if sigutil.RebuildSignal != nil {
		signal.Notify(sigusr1, sigutil.RebuildSignal)
	}
	defer signal.Stop(sigusr1)

	if openBrowser {
		_ = ui.OpenBrowser(devBrowserOpenTarget(cockpitURL, viewerURL))
	}

	stdinKeys, restoreTerminal, keybindsEnabled := readStdinKeys(ctx, stopper.RequestStop)
	defer restoreTerminal()
	ticker := time.NewTicker(defaultDevSessionPollInterval)
	defer ticker.Stop()

	printRebuildLoopControls(keybindsEnabled, false)
	ui.Println()

	rebuildCount := 0
	var lastRebuildStart time.Time
	for {
		var doRebuild bool
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
			alive, reason := deviceMgr.CheckSessionAlive(checkCtx, session)
			checkCancel()
			if !alive {
				ui.PrintWarning("Device session ended (%s).", reason)
				cancel()
				return nil
			}
		case <-sigusr1:
			doRebuild = true
		case <-cockpitRebuilds:
			doRebuild = true
		case key := <-stdinKeys:
			switch key {
			case 'r':
				doRebuild = true
			case 'q':
				stopper.RequestStop()
				return nil
			}
		}

		if !doRebuild {
			continue
		}
		if !lastRebuildStart.IsZero() && time.Since(lastRebuildStart) < rebuildCooldown {
			if drainStdinKeys(stdinKeys) {
				stopper.RequestStop()
				return nil
			}
			continue
		}
		lastRebuildStart = time.Now()

		rebuildCount++
		rebuildStart := time.Now()
		result := devRebuildResult{buildMode: "remote"}
		remoteBuild, buildErr := runRemoteDevBuild(ctx, client, platCfg, buildCaches, platformKey, devicePlatform, appID, cwd)
		if buildErr != nil {
			if stopper.IsUserCanceled(buildErr) {
				return nil
			}
			result.buildErr = buildErr
			result.elapsed = time.Since(rebuildStart)
			writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, rebuildCount, false, result)
			ui.PrintWarning("Remote rebuild failed: %v", buildErr)
			printRebuildLoopControls(keybindsEnabled, true)
			continue
		}
		result.remoteJobID = remoteBuild.jobID
		result.remoteVersionID = remoteBuild.versionID
		result.remoteVersion = remoteBuild.version
		result.buildDuration = remoteBuild.duration

		buildDetail, detailErr := client.GetBuildVersionDownloadURL(ctx, remoteBuild.versionID)
		if detailErr != nil {
			if stopper.IsUserCanceled(detailErr) {
				return nil
			}
			result.pushErr = fmt.Errorf("could not resolve remote build download URL: %w", detailErr)
			result.elapsed = time.Since(rebuildStart)
			writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, rebuildCount, false, result)
			ui.PrintWarning("Remote rebuild failed: %v", result.pushErr)
			printRebuildLoopControls(keybindsEnabled, true)
			continue
		}

		installedBundleID, installDuration, installErr := installRemoteDevBuild(ctx, deviceMgr, session, buildDetail, bundleID)
		result.pushDuration = installDuration
		if installErr != nil {
			if stopper.IsUserCanceled(installErr) {
				return nil
			}
			result.pushErr = installErr
			result.elapsed = time.Since(rebuildStart)
			writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, rebuildCount, false, result)
			ui.PrintWarning("Install failed: %v", installErr)
			printRebuildLoopControls(keybindsEnabled, true)
			continue
		}
		if installedBundleID != "" {
			bundleID = installedBundleID
			result.newBundleID = installedBundleID
		}

		tryLaunchInstalledApp(ctx, deviceMgr, session.Index, devicePlatform, bundleID, "", "")
		result.elapsed = time.Since(rebuildStart)
		writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, rebuildCount, false, result)

		if drainStdinKeys(stdinKeys) {
			stopper.RequestStop()
			return nil
		}

		ui.PrintSuccess("Remote rebuilt + reinstalled (%s) - build: %s, device update: %s",
			formatProgressDuration(result.elapsed),
			formatProgressDuration(result.buildDuration),
			formatProgressDuration(result.pushDuration),
		)
		ui.Println()
		printRebuildLoopControls(keybindsEnabled, false)
	}
}

func runRemoteDevBuild(
	ctx context.Context,
	client *api.Client,
	platCfg config.BuildPlatform,
	buildCaches []config.BuildCache,
	platformKey string,
	devicePlatform string,
	appID string,
	cwd string,
) (remoteDevBuildResult, error) {
	start := time.Now()
	parsedAppID, err := uuid.Parse(strings.TrimSpace(appID))
	if err != nil {
		return remoteDevBuildResult{}, fmt.Errorf("app id must be a valid UUID: %w", err)
	}
	ui.Println()
	ui.PrintInfo("Remote building %s...", platformKey)
	ui.PrintInfo("Packaging current working tree...")
	ui.PrintDim("  Run from the app subdirectory when using a large monorepo.")

	archivePath, err := createSourceArchiveIncludingWorkingTree(cwd)
	if err != nil {
		return remoteDevBuildResult{}, fmt.Errorf("failed to package current working tree: %w", err)
	}
	defer os.Remove(archivePath)

	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return remoteDevBuildResult{}, fmt.Errorf("failed to stat source archive: %w", err)
	}
	sizeMB := float64(archiveInfo.Size()) / (1024 * 1024)
	ui.PrintDim("  Source snapshot: %.1f MB", sizeMB)
	if sizeMB > 500 {
		return remoteDevBuildResult{}, fmt.Errorf("source archive too large (%.0f MB). Max 500 MB", sizeMB)
	}

	uploadResp, err := client.GetRemoteBuildUploadURL(ctx, parsedAppID, "source.tar.gz", archiveInfo.Size())
	if err != nil {
		return remoteDevBuildResult{}, fmt.Errorf("failed to get remote build upload URL: %w", err)
	}

	var uploadFields map[string]string
	if uploadResp.UploadFields != nil {
		uploadFields = *uploadResp.UploadFields
	}
	if err := client.UploadFileToPresignedPost(ctx, uploadResp.UploadUrl, uploadFields, archivePath); err != nil {
		return remoteDevBuildResult{}, fmt.Errorf("failed to upload source snapshot: %w", err)
	}

	platform := devicePlatform
	versionStr := build.GenerateVersionStringForWorkDir(cwd)
	triggerReq, err := remoteDevTriggerRequest(parsedAppID, uploadResp.SourceKey, platform, versionStr, platCfg, buildCaches)
	if err != nil {
		return remoteDevBuildResult{}, err
	}
	timeoutSeconds, err := buildPlatformTimeoutSeconds(platCfg, platformKey)
	if err != nil {
		return remoteDevBuildResult{}, err
	}
	triggerResp, err := client.TriggerRemoteBuild(ctx, triggerReq, timeoutSeconds)
	if err != nil {
		return remoteDevBuildResult{}, fmt.Errorf("failed to trigger remote build: %w", err)
	}

	status, err := pollRemoteBuildStatusResult(ctx, client, triggerResp.BuildJobId, false)
	if err != nil {
		return remoteDevBuildResult{jobID: triggerResp.BuildJobId, duration: time.Since(start)}, err
	}
	if status.VersionId == nil || strings.TrimSpace(*status.VersionId) == "" {
		return remoteDevBuildResult{jobID: triggerResp.BuildJobId, duration: time.Since(start)}, fmt.Errorf("remote build succeeded but returned no build version ID")
	}
	version := ""
	if status.Version != nil {
		version = strings.TrimSpace(*status.Version)
	}

	return remoteDevBuildResult{
		jobID:     triggerResp.BuildJobId,
		versionID: strings.TrimSpace(*status.VersionId),
		version:   version,
		duration:  time.Since(start),
	}, nil
}

func remoteDevTriggerRequest(appID uuid.UUID, sourceKey, platform, version string, platCfg config.BuildPlatform, buildCaches []config.BuildCache) (*api.RemoteBuildRequest, error) {
	setCurrent := true
	source, err := remoteBuildRequestSource(nil, sourceKey, "")
	if err != nil {
		return nil, err
	}
	resolved := remoteBuildPlatformConfig{
		Platform: platform,
		Setup:    strings.TrimSpace(platCfg.Setup),
		Commands: platCfg.BuildCommands(),
		Output:   strings.TrimSpace(platCfg.Output),
		Image:    strings.TrimSpace(platCfg.Image),
		Scheme:   strings.TrimSpace(platCfg.Scheme),
		Env:      platCfg.Env,
		Caches:   buildCaches,
	}
	return &api.RemoteBuildRequest{
		Source:       source,
		Config:       remoteBuildConfigFromResolved(appID, resolved),
		Version:      stringPtrOrNil(version),
		SetAsCurrent: &setCurrent,
	}, nil
}

func pollRemoteBuildStatusResult(ctx context.Context, client *api.Client, jobID string, jsonMode bool) (*api.RemoteBuildStatusResponse, error) {
	ticker := time.NewTicker(remoteBuildPollInterval)
	defer ticker.Stop()

	lastStatus := ""
	logCursor := "0-0"
	logFormatter := &remoteBuildLogFormatter{}
	startTime := time.Now()
	if !ui.IsDebugMode() {
		ui.StartSpinner("Build queued")
		defer ui.StopSpinner()
	}

	for {
		select {
		case <-ctx.Done():
			if !ui.IsDebugMode() {
				ui.StopSpinner()
			}
			return nil, remoteBuildPollingInterruptedError(jobID, jsonMode)
		case <-ticker.C:
			status, err := client.GetRemoteBuildStatus(ctx, jobID)
			if err != nil {
				ui.PrintDebug("failed to poll remote build status: %v", err)
				continue
			}

			if status.Status != lastStatus {
				if ui.IsDebugMode() {
					elapsed := time.Since(startTime).Round(time.Second)
					ui.PrintInfo("[%s] Remote build status: %s", elapsed, status.Status)
				} else {
					ui.StopSpinner()
					switch status.Status {
					case "queued", "pending":
						ui.StartSpinner("Build queued")
					case "building", "running":
						ui.StartSpinner("Build in progress")
					case "success", "failed", "cancelled":
					default:
						ui.StartSpinner("Build " + status.Status)
					}
				}
				lastStatus = status.Status
			}

			if ui.IsDebugMode() {
				logs, err := client.GetRemoteBuildLogs(ctx, jobID, logCursor)
				if err != nil {
					ui.PrintDebug("failed to poll build logs: %v", err)
				} else {
					if logs.Events != nil {
						for _, event := range *logs.Events {
							logFormatter.Print(event)
						}
					}
					if logs.NextCursor != nil && *logs.NextCursor != "" {
						logCursor = *logs.NextCursor
					}
				}
			}

			switch status.Status {
			case "success":
				if !ui.IsDebugMode() {
					ui.StopSpinner()
				}
				if status.VersionId == nil || strings.TrimSpace(*status.VersionId) == "" {
					return status, fmt.Errorf("remote build succeeded but returned no build version ID")
				}
				ui.PrintSuccess("Remote build completed in %s", formatBuildProgressDuration(time.Since(startTime)))
				printRemoteBuildPhaseTimings(status.PhaseTimings)
				return status, nil
			case "failed":
				if !ui.IsDebugMode() {
					ui.StopSpinner()
				}
				printRemoteBuildLogTail(ctx, client, jobID)
				if status.Error != nil && *status.Error != "" {
					return status, fmt.Errorf("remote build failed: %s", *status.Error)
				}
				return status, fmt.Errorf("remote build failed")
			case "cancelled":
				if !ui.IsDebugMode() {
					ui.StopSpinner()
				}
				printRemoteBuildLogTail(ctx, client, jobID)
				if status.Error != nil && *status.Error != "" {
					return status, fmt.Errorf("remote build cancelled: %s", *status.Error)
				}
				return status, fmt.Errorf("remote build cancelled")
			}
		}
	}
}

func installRemoteDevBuild(
	ctx context.Context,
	deviceMgr *mcppkg.DeviceSessionManager,
	session *mcppkg.DeviceSession,
	buildDetail *api.BuildVersionDetail,
	bundleID string,
) (string, time.Duration, error) {
	start := time.Now()
	body := map[string]string{
		"app_url":      strings.TrimSpace(buildDetail.DownloadURL),
		"install_mode": "fast",
	}
	if bundleID != "" {
		body["bundle_id"] = bundleID
	}

	ui.PrintInfo("Installing remote build on device...")
	var resp []byte
	var err error
	const maxInstallRetries = 3
	for attempt := 0; attempt <= maxInstallRetries; attempt++ {
		resp, err = deviceMgr.WorkerRequestForSession(ctx, session.Index, "/install", body)
		if err == nil {
			break
		}
		var workerErr *mcppkg.WorkerHTTPError
		isDeviceNotReady := errorsAsWorkerHTTPError(err, &workerErr) && workerErr.StatusCode == 503
		if !isDeviceNotReady || attempt == maxInstallRetries {
			return "", time.Since(start), fmt.Errorf("install failed: %w", err)
		}
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		ui.PrintDebug("device not ready, retrying install in %s (attempt %d/%d)", backoff, attempt+1, maxInstallRetries)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return "", time.Since(start), ctx.Err()
		}
	}

	if err := ensureWorkerActionSucceeded(resp, "install"); err != nil {
		return "", time.Since(start), fmt.Errorf("install failed: %w", err)
	}
	if extracted := extractInstallBundleID(resp); extracted != "" {
		bundleID = extracted
	}
	return bundleID, time.Since(start), nil
}

func errorsAsWorkerHTTPError(err error, target **mcppkg.WorkerHTTPError) bool {
	var workerErr *mcppkg.WorkerHTTPError
	if !errors.As(err, &workerErr) {
		return false
	}
	*target = workerErr
	return true
}
