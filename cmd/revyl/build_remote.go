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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var remoteBuildPollInterval = 3 * time.Second

// runRemoteBuild packages the local source via git archive, uploads it,
// triggers a remote iOS build on a dedicated cloud runner, and polls
// until the build completes or fails.
//
// Parameters:
//   - cmd: The cobra command (used for context).
//   - apiKey: Authenticated API key.
//
// Returns:
//   - error: Any error encountered during the process.
func runRemoteBuild(cmd *cobra.Command, apiKey string) error {
	ctx := cmd.Context()
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// ── 1. Pre-flight: verify a build runner is online for this org ──
	ui.PrintInfo("Checking build runner availability…")
	runnerStatus, runnerErr := client.CheckBuildRunnersAvailable(ctx)
	if runnerErr != nil {
		ui.PrintWarning("Could not verify runner availability: %v (proceeding anyway)", runnerErr)
	} else if !runnerStatus.Available {
		ui.PrintError("No iOS build runners are available for your organisation.")
		ui.PrintError("")
		ui.PrintError("Remote builds require a dedicated build runner assigned to your org.")
		ui.PrintError("This can happen if:")
		ui.PrintError("  - Your org doesn't have a build runner provisioned yet")
		ui.PrintError("  - The build runner for your org is offline or being updated")
		ui.PrintError("  - All runners are currently occupied (try again shortly)")
		ui.PrintError("")
		ui.PrintError("What to do:")
		ui.PrintError("  - Try again in a few minutes (runners may be restarting)")
		ui.PrintError("  - Contact support to provision a build runner for your org")
		ui.PrintError("  - Build locally with: revyl build upload --platform ios")
		return fmt.Errorf("no build runners available for your organisation")
	} else if runnerStatus.RunnerCount < 0 {
		ui.PrintWarning("Could not confirm runner availability (proceeding anyway)")
	} else if runnerStatus.RunnerCount > 0 {
		ui.PrintInfo("Found %d active build runner(s) for your org", runnerStatus.RunnerCount)
	}

	// ── 2. Detect build system and command ────────────────────────
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	platform := uploadPlatformFlag
	if platform == "" {
		platform = "ios"
	}

	if platform != "ios" {
		return fmt.Errorf("remote builds currently only support iOS")
	}

	// Try to detect build command from project config or auto-detection
	buildCmd, scheme, setupCmd, err := detectBuildCommand(cwd, platform)
	if err != nil {
		return fmt.Errorf("failed to detect build command: %w", err)
	}

	// ── 3. Resolve app ───────────────────────────────────────────
	appID, err := resolveAppForRemoteBuild(ctx, client, platform)
	if err != nil {
		return err
	}

	ui.PrintInfo("Starting remote build for app %s", appID)

	// ── 4. Package source via git archive ────────────────────────
	ui.PrintInfo("Packaging source code…")

	if dirty, count := checkDirtyTree(cwd); dirty {
		ui.PrintWarning("%d file(s) have uncommitted changes and will NOT be included in the remote build.", count)
		ui.PrintWarning("Commit your changes first, or use --include-dirty to proceed anyway.")
		includeDirty, _ := cmd.Flags().GetBool("include-dirty")
		if !includeDirty {
			return fmt.Errorf("uncommitted changes detected; commit or pass --include-dirty")
		}
	}

	archivePath, err := createSourceArchive(cwd)
	if err != nil {
		return fmt.Errorf("failed to package source: %w", err)
	}
	defer os.Remove(archivePath)

	archiveInfo, _ := os.Stat(archivePath)
	sizeMB := float64(archiveInfo.Size()) / (1024 * 1024)
	ui.PrintInfo("Source archive: %.1f MB", sizeMB)

	if sizeMB > 500 {
		return fmt.Errorf("source archive too large (%.0f MB). Max 500 MB", sizeMB)
	}

	// ── 5. Get presigned upload URL ──────────────────────────────
	ui.PrintInfo("Uploading source to Revyl…")
	uploadResp, err := client.GetRemoteBuildUploadURL(ctx, appID, "source.tar.gz", archiveInfo.Size())
	if err != nil {
		return fmt.Errorf("failed to get upload URL: %w", err)
	}

	// ── 6. Upload source archive to S3 via presigned POST ────────
	var uploadFields map[string]string
	if uploadResp.UploadFields != nil {
		uploadFields = *uploadResp.UploadFields
	}
	if err := client.UploadFileToPresignedPost(ctx, uploadResp.UploadUrl, uploadFields, archivePath); err != nil {
		return fmt.Errorf("failed to upload source: %w", err)
	}

	ui.PrintSuccess("Source uploaded")

	// ── 7. Trigger remote build ──────────────────────────────────
	ui.PrintInfo("Triggering remote build…")
	setCurrent := buildSetCurr
	triggerReq := &api.RemoteBuildRequest{
		AppId:        appID,
		SourceKey:    uploadResp.SourceKey,
		BuildCommand: buildCmd,
		BuildScheme:  &scheme,
		SetupCommand: stringPtrOrNil(setupCmd),
		CleanBuild:   boolPtrOrNil(uploadCleanFlag),
		Version:      stringPtrOrNil(buildVersion),
		SetAsCurrent: &setCurrent,
		Platform:     &platform,
	}
	triggerResp, err := client.TriggerRemoteBuild(ctx, triggerReq)
	if err != nil {
		return fmt.Errorf("failed to trigger build: %w", err)
	}

	jobID := triggerResp.BuildJobId
	ui.PrintInfo("Build queued: %s", jobID)

	// ── 8. Poll for status ───────────────────────────────────────
	if err := pollBuildStatus(ctx, client, jobID); err != nil {
		return err
	}

	if !buildUploadJSON {
		cwd, _ := os.Getwd()
		testsDir := filepath.Join(cwd, ".revyl", "tests")
		var steps []ui.NextStep
		steps = append(steps, ui.NextStep{
			Label:   "Start a device with this build:",
			Command: fmt.Sprintf("revyl device start --platform %s --app-id %s", platform, appID),
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
		"no %s build configuration found. Add build.platforms.%s.command to .revyl/config.yaml or run 'revyl init'",
		platform, platform,
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

// pollBuildStatus polls the remote build status endpoint until the build
// reaches a terminal state (success or failure).
//
// Parameters:
//   - ctx: Cancellation context.
//   - client: API client.
//   - jobID: Build job UUID to poll.
//
// Returns:
//   - error: If the build fails or polling encounters an error.
func pollBuildStatus(ctx context.Context, client *api.Client, jobID string) error {
	ticker := time.NewTicker(remoteBuildPollInterval)
	defer ticker.Stop()

	lastStatus := ""
	lastLogLines := 0
	startTime := time.Now()
	timeout := 30 * time.Minute

	cancelBuild := func(reason string) {
		ui.PrintWarning("Cancelling remote build (%s)…", reason)
		cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.CancelRemoteBuild(cancelCtx, jobID); err != nil {
			ui.PrintWarning("Failed to cancel build: %v", err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			cancelBuild("interrupted")
			return ctx.Err()
		case <-ticker.C:
			if time.Since(startTime) > timeout {
				cancelBuild("timeout")
				return fmt.Errorf("build timed out after %v", timeout)
			}

			status, err := client.GetRemoteBuildStatus(ctx, jobID)
			if err != nil {
				ui.PrintWarning("Failed to poll status: %v", err)
				continue
			}

			if status.Status != lastStatus {
				elapsed := time.Since(startTime).Round(time.Second)
				ui.PrintInfo("[%s] Build status: %s", elapsed, status.Status)
				lastStatus = status.Status
			}

			if status.LogsTail != nil && *status.LogsTail != "" {
				lines := strings.Split(*status.LogsTail, "\n")
				if len(lines) > lastLogLines {
					for _, line := range lines[lastLogLines:] {
						if line == "" {
							continue
						}
						if ui.IsDebugMode() {
							fmt.Fprintf(os.Stderr, "  %s\n", line)
							continue
						}
						if displayLine, ok := build.FilterBuildOutputLine(line); ok {
							fmt.Fprintf(os.Stderr, "  %s\n", displayLine)
						}
					}
					lastLogLines = len(lines)
				}
			}

			switch status.Status {
			case "success":
				if status.VersionId == nil || strings.TrimSpace(*status.VersionId) == "" {
					return fmt.Errorf("remote build succeeded but returned no build version ID")
				}
				elapsed := time.Since(startTime).Round(time.Second)
				ui.PrintSuccess("Build completed successfully in %s!", elapsed)
				if status.Version != nil && *status.Version != "" {
					ui.PrintInfo("Version: %s", *status.Version)
				}
				if status.VersionId != nil && *status.VersionId != "" {
					ui.PrintInfo("Version ID: %s", *status.VersionId)
				}

				if buildUploadJSON {
					ver := ""
					verID := ""
					if status.Version != nil {
						ver = *status.Version
					}
					if status.VersionId != nil {
						verID = *status.VersionId
					}
					out := map[string]string{
						"status":     "success",
						"version":    ver,
						"version_id": verID,
						"job_id":     jobID,
					}
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					enc.Encode(out)
				}
				return nil

			case "failed":
				if status.Error != nil && *status.Error != "" {
					ui.PrintError("Build failed: %s", *status.Error)
				} else {
					ui.PrintError("Build failed")
				}
				if status.LogsTail != nil && *status.LogsTail != "" {
					fmt.Fprintln(os.Stderr, "\n--- Build log tail ---")
					fmt.Fprintln(os.Stderr, *status.LogsTail)
				}
				return fmt.Errorf("remote build failed")
			case "cancelled":
				if status.Error != nil && *status.Error != "" {
					ui.PrintError("Build cancelled: %s", *status.Error)
				} else {
					ui.PrintError("Build cancelled")
				}
				if status.LogsTail != nil && *status.LogsTail != "" {
					fmt.Fprintln(os.Stderr, "\n--- Build log tail ---")
					fmt.Fprintln(os.Stderr, *status.LogsTail)
				}
				return fmt.Errorf("remote build cancelled")
			}
		}
	}
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
