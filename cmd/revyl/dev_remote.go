package main

import (
	"context"
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
	"github.com/revyl/cli/internal/buildselection"
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

// remoteDevInstaller installs an artifact onto a specific device session.
type remoteDevInstaller interface {
	InstallAppForSession(
		ctx context.Context,
		index int,
		req mcppkg.DeviceInstallRequest,
	) (*mcppkg.WorkerActionResponse, error)
}

func validateRemoteDevStartFlags() error {
	if _, err := normalizeMobilePlatform(devStartPlatform, "ios"); err != nil {
		return err
	}
	if devStartNoBuild {
		return fmt.Errorf("use either --remote or --no-build, not both")
	}
	if strings.TrimSpace(devStartTunnelURL) != "" {
		return fmt.Errorf("use either --remote or --tunnel, not both")
	}
	// --build-version-id is allowed with --remote: it names the build to seed
	// (install immediately) while the fresh source builds remotely.
	return nil
}

// seedRequested reports whether the remote dev loop should install an existing
// build immediately (before the fresh remote build lands). Seeding is opt-in:
// either --seed-latest (newest build overall for the config app-id) or an
// explicit --build-version-id (that specific build) turns it on.
func seedRequested() bool {
	return devStartSeedLatest || strings.TrimSpace(devStartBuildVerID) != ""
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

	openBrowser := effectiveDevOpenBrowser(cmd, configPath)

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

	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}

	// Kick off the remote build (package + upload + enqueue) concurrently with
	// device boot + optional seed install so time-to-app-open is not gated on
	// the source upload. The simulator is watchable within seconds; the fresh
	// build installs (hot-swap) the moment the artifact lands.
	triggerCh := make(chan remoteDevBuildTrigger, 1)
	go func() {
		job, triggerErr := triggerRemoteDevBuild(ctx, client, platCfg, buildCaches, platformKey, devicePlatform, appID, cwd)
		triggerCh <- remoteDevBuildTrigger{job: job, err: triggerErr}
	}()

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
		ui.PrintInfo("Starting cloud device session (build continues in background)...")
		startOpts := withDevStartLaunchVars(mcppkg.StartSessionOptions{
			Platform:    devicePlatform,
			IdleTimeout: time.Duration(timeout) * time.Second,
		})
		// No app is installed at boot; the post-install launch fires the auth
		// deep link instead of a boot-time app link.
		startOpts.AppLink = ""
		_, session, err = startDevSessionWithProgress(
			ctx,
			deviceMgr,
			startOpts,
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

	// Publish the session with the build still running so `revyl dev status`,
	// `--detach`, and the cockpit see it immediately. The build job id is not
	// known yet (the trigger runs concurrently), so it is filled in later.
	statusPath := devCtxStatusPath(cwd, ctxName)
	writeDevStatusRemoteBuildRunning(statusPath, session, viewerURL, devicePlatform, platformKey, "", buildCaches, "", false)
	registeredTriggerCh := make(chan remoteDevBuildTrigger, 1)
	go func() {
		trigger := <-triggerCh
		if trigger.err == nil {
			setDevStatusRemoteJobID(statusPath, trigger.job.jobID)
		}
		registeredTriggerCh <- trigger
	}()

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
	ui.PrintSuccess("Device ready — remote build in progress")
	printDevBrowserLinks(cockpitURL, viewerURL)
	ui.PrintDim("Watch the build: revyl dev logs --build --follow")
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

	// Seed an existing build immediately (opt-in) so the app is interactive and
	// authenticated within seconds while the fresh source builds remotely. The
	// bundle id from the seed is reused for the later hot-swap.
	bundleID := ""
	if seedRequested() {
		seededBundleID, seededVersion, seedErr := seedLatestDevBuild(
			ctx, client, deviceMgr, session, devicePlatform, appID,
			strings.TrimSpace(devStartBuildVerID),
		)
		if seedErr != nil {
			if stopper.IsUserCanceled(seedErr) {
				return nil
			}
			ui.PrintWarning("Seed install skipped: %v", seedErr)
		} else if seededBundleID != "" {
			bundleID = seededBundleID
			setDevStatusSeedInstalled(statusPath, seededVersion)
			ui.PrintSuccess("Seeded build on device (%s) — hot-swap when the fresh build lands", seededVersion)
		} else {
			ui.PrintInfo("No existing build to seed; device stays on the home screen until the fresh build lands.")
		}
	}

	// Join the concurrent build trigger, then wait for it to complete and
	// install + launch (hot-swap). A failed initial build keeps the simulator
	// alive so a fix + rebuild reuses this device (a seeded build stays on
	// screen in the meantime).
	trigger := <-registeredTriggerCh
	if trigger.err != nil {
		if stopper.IsUserCanceled(trigger.err) {
			return nil
		}
		failResult := devRebuildResult{
			buildMode: "remote",
			buildErr:  trigger.err,
		}
		writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, 0, false, failResult)
		ui.PrintWarning("Failed to start remote build: %v", trigger.err)
		ui.PrintInfo("Device session stays alive — fix the issue and trigger a rebuild.")
	} else {
		buildJob := trigger.job
		remoteBuild, buildErr := waitRemoteDevBuild(ctx, client, buildJob, cwd)
		if buildErr != nil {
			if stopper.IsUserCanceled(buildErr) {
				return nil
			}
			failResult := devRebuildResult{
				buildMode:   "remote",
				buildErr:    buildErr,
				elapsed:     time.Since(buildJob.started),
				remoteJobID: buildJob.jobID,
			}
			writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, 0, false, failResult)
			ui.PrintWarning("Initial remote build failed: %v", buildErr)
			ui.PrintInfo("Device session stays alive — fix the issue and trigger a rebuild.")
		} else {
			installErr := installAndLaunchRemoteDevBuild(ctx, client, deviceMgr, session, devicePlatform, remoteBuild, &bundleID, statusPath, viewerURL)
			if installErr != nil {
				if stopper.IsUserCanceled(installErr) {
					return nil
				}
				ui.PrintWarning("%v", installErr)
				ui.PrintInfo("Device session stays alive — fix the issue and trigger a rebuild.")
			}
		}
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

		// Publish "running" before any work so `revyl dev status` and
		// `rebuild --wait` never see the prior rebuild's result as current.
		writeDevStatusRebuildStarted(statusPath, session, viewerURL, "", "", "", devicePlatform, rebuildCount, false, platformKey)

		// Reload the project config so edits to build commands, caches, and
		// auth_bypass take effect without restarting the loop.
		if reloaded, reloadErr := config.LoadProjectConfig(configPath); reloadErr != nil {
			result.buildErr = fmt.Errorf("config_invalid: failed to reload %s: %w", configPath, reloadErr)
			result.elapsed = time.Since(rebuildStart)
			writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, rebuildCount, false, result)
			ui.PrintWarning("Rebuild skipped: %v", result.buildErr)
			printRebuildLoopControls(keybindsEnabled, true)
			continue
		} else {
			reloadedPlat, ok := reloaded.Build.Platforms[platformKey]
			if !ok || len(reloadedPlat.BuildCommands()) == 0 {
				result.buildErr = fmt.Errorf("config_invalid: build.platforms.%s no longer has build commands in %s", platformKey, configPath)
				result.elapsed = time.Since(rebuildStart)
				writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, rebuildCount, false, result)
				ui.PrintWarning("Rebuild skipped: %v", result.buildErr)
				printRebuildLoopControls(keybindsEnabled, true)
				continue
			}
			platCfg = reloadedPlat
			if newAppID := strings.TrimSpace(reloadedPlat.AppID); newAppID != "" {
				appID = newAppID
			}
			buildCaches = config.EffectiveBuildCaches(reloaded.Build, reloadedPlat)
			initDevAuthBypass(reloaded, client)
		}

		buildJob, buildErr := triggerRemoteDevBuild(ctx, client, platCfg, buildCaches, platformKey, devicePlatform, appID, cwd)
		if buildErr == nil {
			// Surface the live job id while the runner builds.
			setDevStatusRemoteJobID(statusPath, buildJob.jobID)
			var remoteBuild remoteDevBuildResult
			remoteBuild, buildErr = waitRemoteDevBuild(ctx, client, buildJob, cwd)
			if buildErr == nil {
				result.remoteJobID = remoteBuild.jobID
				result.remoteVersionID = remoteBuild.versionID
				result.remoteVersion = remoteBuild.version
				result.buildDuration = remoteBuild.duration
			} else {
				result.remoteJobID = buildJob.jobID
			}
		}
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
		remoteBuild := remoteDevBuildResult{
			jobID:     result.remoteJobID,
			versionID: result.remoteVersionID,
			version:   result.remoteVersion,
			duration:  result.buildDuration,
		}

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

// remoteDevBuildJob identifies a triggered remote build that has not
// completed yet.
type remoteDevBuildJob struct {
	jobID   string
	started time.Time
}

// remoteDevBuildTrigger carries the result of an asynchronous
// triggerRemoteDevBuild call back to the dev loop, so the build can be
// packaged and enqueued concurrently with device boot and seed install.
type remoteDevBuildTrigger struct {
	job remoteDevBuildJob
	err error
}

// seedLatestDevBuild installs an existing build on the session's device
// immediately so the app is interactive (and, with auth bypass configured,
// authenticated) within seconds while the fresh source builds remotely.
//
// Parameters:
//   - explicitVersionID: a specific build version to seed; when empty the
//     newest build overall for appID is resolved (not branch-aware).
//
// Returns the installed bundle id and a human-readable version label, or
// ("", "", nil) when there is no existing build to seed (a clean no-op so the
// loop falls back to the blank-device experience). A non-nil error means the
// resolve or install failed and should be surfaced as a warning.
func seedLatestDevBuild(
	ctx context.Context,
	client *api.Client,
	deviceMgr *mcppkg.DeviceSessionManager,
	session *mcppkg.DeviceSession,
	devicePlatform string,
	appID string,
	explicitVersionID string,
) (string, string, error) {
	versionID := strings.TrimSpace(explicitVersionID)
	if versionID == "" {
		// Seed uses latest-overall (empty branch), not branch-aware selection.
		// TODO: For Expo/RN, skip CompatibleNo builds via ClassifyBuild so we
		// do not seed production/preview EAS artifacts.
		// TODO: Consider making latest-overall the default beyond --seed-latest
		// once revyl run / normal revyl dev callers agree.
		selected, _, warnings, err := buildselection.SelectPreferredBuildVersionForBranch(ctx, client, appID, "")
		if err != nil {
			return "", "", fmt.Errorf("could not resolve latest build to seed: %w", err)
		}
		for _, warning := range warnings {
			ui.PrintWarning("%s", warning)
		}
		if selected == nil {
			return "", "", nil
		}
		versionID = selected.ID
	}

	ui.PrintInfo("Seeding existing build on device (interactive while the fresh build runs)...")
	buildDetail, err := client.GetBuildVersionDownloadURL(ctx, versionID)
	if err != nil {
		return "", "", fmt.Errorf("could not resolve seed build download URL: %w", err)
	}

	bundleID := strings.TrimSpace(buildDetail.PackageName)
	installedBundleID, _, err := installRemoteDevBuild(ctx, deviceMgr, session, buildDetail, bundleID)
	if err != nil {
		return "", "", err
	}
	if installedBundleID != "" {
		bundleID = installedBundleID
	}

	// Launch fires the auth bypass deep link (fireAuthBypassAfterLaunch) so the
	// seeded app lands authenticated without waiting on the fresh build.
	tryLaunchInstalledApp(ctx, deviceMgr, session.Index, devicePlatform, bundleID, "", "")

	return bundleID, strings.TrimSpace(buildDetail.Version), nil
}

// triggerRemoteDevBuild packages the working tree, uploads it, and enqueues a
// remote build. It returns as soon as the job is accepted so the caller can do
// other work (e.g. boot a device) while the runner builds.
func triggerRemoteDevBuild(
	ctx context.Context,
	client *api.Client,
	platCfg config.BuildPlatform,
	buildCaches []config.BuildCache,
	platformKey string,
	devicePlatform string,
	appID string,
	cwd string,
) (remoteDevBuildJob, error) {
	start := time.Now()
	parsedAppID, err := uuid.Parse(strings.TrimSpace(appID))
	if err != nil {
		return remoteDevBuildJob{}, fmt.Errorf("app id must be a valid UUID: %w", err)
	}
	ui.Println()
	ui.PrintInfo("Remote building %s...", platformKey)
	ui.PrintInfo("Packaging current working tree...")
	ui.PrintDim("  Run from the app subdirectory when using a large monorepo.")

	archivePath, err := createSourceArchiveIncludingWorkingTree(cwd)
	if err != nil {
		return remoteDevBuildJob{}, fmt.Errorf("failed to package current working tree: %w", err)
	}
	defer os.Remove(archivePath)

	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return remoteDevBuildJob{}, fmt.Errorf("failed to stat source archive: %w", err)
	}
	sizeMB := float64(archiveInfo.Size()) / (1024 * 1024)
	ui.PrintDim("  Source snapshot: %.1f MB", sizeMB)
	if sizeMB > 500 {
		return remoteDevBuildJob{}, fmt.Errorf("source archive too large (%.0f MB). Max 500 MB", sizeMB)
	}

	uploadResp, err := client.GetRemoteBuildUploadURL(ctx, parsedAppID, "source.tar.gz", archiveInfo.Size())
	if err != nil {
		return remoteDevBuildJob{}, fmt.Errorf("failed to get remote build upload URL: %w", err)
	}

	var uploadFields map[string]string
	if uploadResp.UploadFields != nil {
		uploadFields = *uploadResp.UploadFields
	}
	if err := client.UploadFileToPresignedPost(ctx, uploadResp.UploadUrl, uploadFields, archivePath); err != nil {
		return remoteDevBuildJob{}, fmt.Errorf("failed to upload source snapshot: %w", err)
	}

	platform := devicePlatform
	versionStr := build.GenerateVersionStringForWorkDir(cwd)
	triggerReq, err := remoteDevTriggerRequest(parsedAppID, uploadResp.SourceKey, platform, versionStr, platCfg, buildCaches)
	if err != nil {
		return remoteDevBuildJob{}, err
	}
	timeoutSeconds, err := buildPlatformTimeoutSeconds(platCfg, platformKey)
	if err != nil {
		return remoteDevBuildJob{}, err
	}
	triggerResp, err := client.TriggerRemoteBuild(ctx, triggerReq, timeoutSeconds)
	if err != nil {
		return remoteDevBuildJob{}, fmt.Errorf("failed to trigger remote build: %w", err)
	}

	return remoteDevBuildJob{jobID: triggerResp.BuildJobId, started: start}, nil
}

// waitRemoteDevBuild blocks until a triggered remote build completes and
// resolves its build version.
func waitRemoteDevBuild(
	ctx context.Context,
	client *api.Client,
	job remoteDevBuildJob,
	cwd string,
) (remoteDevBuildResult, error) {
	status, err := pollRemoteBuildStatusResult(ctx, client, job.jobID, false)
	if err != nil {
		return remoteDevBuildResult{jobID: job.jobID, duration: time.Since(job.started)}, err
	}
	if status.VersionId == nil || strings.TrimSpace(*status.VersionId) == "" {
		return remoteDevBuildResult{jobID: job.jobID, duration: time.Since(job.started)}, fmt.Errorf("remote build succeeded but returned no build version ID")
	}
	version := ""
	if status.Version != nil {
		version = strings.TrimSpace(*status.Version)
	}

	return remoteDevBuildResult{
		jobID:     job.jobID,
		versionID: strings.TrimSpace(*status.VersionId),
		version:   version,
		duration:  time.Since(job.started),
	}, nil
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
	job, err := triggerRemoteDevBuild(ctx, client, platCfg, buildCaches, platformKey, devicePlatform, appID, cwd)
	if err != nil {
		return remoteDevBuildResult{}, err
	}
	return waitRemoteDevBuild(ctx, client, job, cwd)
}

// writeDevStatusRemoteBuildRunning publishes a live session whose initial
// remote build is still running, so status consumers (agents, cockpit,
// --detach handshakes) can see the device and the build job immediately.
func writeDevStatusRemoteBuildRunning(
	statusPath string,
	session *mcppkg.DeviceSession,
	viewerURL string,
	platform string,
	platformKey string,
	buildJobID string,
	caches []config.BuildCache,
	seededVersion string,
	installedSeed bool,
) {
	buildJobID = strings.TrimSpace(buildJobID)
	runningMsg := fmt.Sprintf("Remote build running for %s", platformKey)
	if buildJobID != "" {
		runningMsg = fmt.Sprintf("%s (job %s)", runningMsg, buildJobID)
	}
	logs := []devRebuildLogEntry{
		newDevRebuildLog("info", runningMsg),
	}
	if installedSeed {
		logs = append(logs, newDevRebuildLog("info", fmt.Sprintf("Seeded existing build %s on device; hot-swap pending", strings.TrimSpace(seededVersion))))
	}
	for _, cache := range caches {
		logs = append(logs, newDevRebuildLog("info", fmt.Sprintf("Cache configured: %s (%s)", cache.Key, strings.Join(cache.Paths, ", "))))
	}
	ds := devStatus{
		State:         "building",
		PID:           os.Getpid(),
		Platform:      strings.TrimSpace(platform),
		BuildMode:     "remote",
		ViewerURL:     strings.TrimSpace(viewerURL),
		RebuildCount:  0,
		SeededVersion: strings.TrimSpace(seededVersion),
		InstalledSeed: installedSeed,
		LastRebuild: &devRebuildInfo{
			StartedAt:   time.Now().UTC().Format(time.RFC3339Nano),
			Seq:         0,
			Status:      "running",
			PushMode:    "pending",
			RemoteJobID: buildJobID,
			BuildErrors: []build.BuildError{},
			Logs:        logs,
		},
	}
	if session != nil {
		ds.SessionID = session.SessionID
	}
	writeDevStatusSnapshot(statusPath, ds)
}

// installAndLaunchRemoteDevBuild resolves a completed remote build, installs
// it on the session's device, launches it (which fires the auth bypass deep
// link), and records the successful initial build in the status file.
func installAndLaunchRemoteDevBuild(
	ctx context.Context,
	client *api.Client,
	deviceMgr *mcppkg.DeviceSessionManager,
	session *mcppkg.DeviceSession,
	devicePlatform string,
	remoteBuild remoteDevBuildResult,
	bundleID *string,
	statusPath string,
	viewerURL string,
) error {
	buildDetail, err := client.GetBuildVersionDownloadURL(ctx, remoteBuild.versionID)
	if err != nil {
		writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, 0, false, devRebuildResult{
			buildMode:   "remote",
			pushErr:     err,
			elapsed:     remoteBuild.duration,
			remoteJobID: remoteBuild.jobID,
		})
		return fmt.Errorf("could not resolve remote build download URL: %w", err)
	}
	if *bundleID == "" {
		*bundleID = strings.TrimSpace(buildDetail.PackageName)
	}

	installedBundleID, installDuration, err := installRemoteDevBuild(ctx, deviceMgr, session, buildDetail, *bundleID)
	if err != nil {
		writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, 0, false, devRebuildResult{
			buildMode:   "remote",
			pushErr:     err,
			elapsed:     remoteBuild.duration + installDuration,
			remoteJobID: remoteBuild.jobID,
		})
		return err
	}
	if installedBundleID != "" {
		*bundleID = installedBundleID
	}
	tryLaunchInstalledApp(ctx, deviceMgr, session.Index, devicePlatform, *bundleID, "", "")

	writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, 0, false, devRebuildResult{
		buildMode:       "remote",
		buildDuration:   remoteBuild.duration,
		pushDuration:    installDuration,
		elapsed:         remoteBuild.duration + installDuration,
		newBundleID:     *bundleID,
		remoteJobID:     remoteBuild.jobID,
		remoteVersionID: remoteBuild.versionID,
		remoteVersion:   remoteBuild.version,
	})

	ui.Println()
	ui.PrintSuccess("Remote dev loop ready")
	ui.PrintInfo("Installed remote build: %s", strings.TrimSpace(buildDetail.Version))
	if identifier := formatInstalledAppIdentifier(devicePlatform, *bundleID); identifier != "" {
		ui.PrintInfo("Installed app: %s", identifier)
	}
	ui.Println()
	return nil
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
	deviceMgr remoteDevInstaller,
	session *mcppkg.DeviceSession,
	buildDetail *api.BuildVersionDetail,
	bundleID string,
) (string, time.Duration, error) {
	start := time.Now()
	request := mcppkg.DeviceInstallRequest{
		AppURL:      strings.TrimSpace(buildDetail.DownloadURL),
		BundleID:    strings.TrimSpace(bundleID),
		InstallMode: mcppkg.DeviceInstallModeFast,
	}

	ui.PrintInfo("Installing remote build on device...")
	result, err := deviceMgr.InstallAppForSession(ctx, session.Index, request)
	if err != nil {
		return "", time.Since(start), fmt.Errorf("install failed: %w", err)
	}
	if result == nil {
		return "", time.Since(start), fmt.Errorf("install failed: worker returned no result")
	}
	if !result.Success {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = "worker reported an unsuccessful install"
		}
		return "", time.Since(start), fmt.Errorf("install failed: %s", message)
	}
	if installedBundleID := strings.TrimSpace(result.BundleID); installedBundleID != "" {
		bundleID = installedBundleID
	}
	return bundleID, time.Since(start), nil
}
