// Package main provides dev context management for named, worktree-local dev loops.
//
// A dev context owns local bootstrap state (provider, tunnel, rebuild cache)
// and is bound to exactly one runtime platform and one primary device session.
// Multiple agents or terminals may share one existing context.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

// DevContext represents a named local dev loop in a worktree.
// Each context is bound to one runtime platform and one primary device session.
//
// Fields:
//   - Name: context identifier (e.g. "default", "ios-main")
//   - Platform: resolved runtime platform ("ios" or "android")
//   - PlatformKey: build.platforms key (e.g. "ios-dev")
//   - Provider: hot reload provider name (e.g. "expo", "react-native", "swift")
//   - SessionID: primary device session ID
//   - SessionIndex: local session index in the DeviceSessionManager
//   - SessionOwned: true if this context created the session (vs attached)
//   - ViewerURL: browser URL for the live device viewer
//   - TunnelURL: public URL of the hot-reload tunnel (empty for rebuild-only)
//   - DeepLinkURL: deep link URL for launching the dev client (empty for rebuild-only)
//   - Transport: public transport type ("relay")
//   - RelayID: backend relay identifier when transport=relay
//   - PID: process ID of the owning dev loop
//   - State: "running" or "stopped"
//   - Port: local dev server port
//   - CreatedAt: when the context was first created
//   - LastActivity: most recent activity timestamp
type DevContext struct {
	Name          string    `json:"name"`
	Platform      string    `json:"platform"`
	PlatformKey   string    `json:"platform_key,omitempty"`
	Provider      string    `json:"provider,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	SessionIndex  int       `json:"session_index"`
	SessionOwned  bool      `json:"session_owned"`
	ViewerURL     string    `json:"viewer_url,omitempty"`
	TunnelURL     string    `json:"tunnel_url,omitempty"`
	DeepLinkURL   string    `json:"deep_link_url,omitempty"`
	Transport     string    `json:"transport,omitempty"`
	RelayID       string    `json:"relay_id,omitempty"`
	PID           int       `json:"pid"`
	StartedAtNano int64     `json:"started_at_nano,omitempty"`
	State         string    `json:"state"`
	Port          int       `json:"port,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastActivity  time.Time `json:"last_activity"`
}

const (
	devContextsDir         = "dev-sessions"
	devContextMetaFile     = "context.json"
	devContextPIDFileName  = "dev.pid"
	devContextStatusFile   = "status.json"
	devContextManifestFile = "manifest.json"
	devContextCurrentFile  = "_current"

	devContextStateRunning = "running"
	devContextStateStopped = "stopped"

	defaultDevContextName = "default"
)

// devCtxDir returns the directory for a named dev context.
//
// Params:
//   - repoRoot: worktree root containing .revyl/
//   - name: context name
//
// Returns:
//   - absolute path to the context directory
func devCtxDir(repoRoot, name string) string {
	return filepath.Join(repoRoot, ".revyl", devContextsDir, name)
}

// devCtxPIDPath returns the PID file path for a context.
func devCtxPIDPath(repoRoot, name string) string {
	return filepath.Join(devCtxDir(repoRoot, name), devContextPIDFileName)
}

// devCtxStatusPath returns the status file path for a context.
func devCtxStatusPath(repoRoot, name string) string {
	return filepath.Join(devCtxDir(repoRoot, name), devContextStatusFile)
}

// devCtxManifestPath returns the push manifest path for a context.
func devCtxManifestPath(repoRoot, name string) string {
	return filepath.Join(devCtxDir(repoRoot, name), devContextManifestFile)
}

// loadDevContext reads a dev context from disk.
//
// Params:
//   - repoRoot: worktree root
//   - name: context name
//
// Returns:
//   - *DevContext: loaded context, or nil on error
//   - error: if the file cannot be read or parsed
func loadDevContext(repoRoot, name string) (*DevContext, error) {
	path := filepath.Join(devCtxDir(repoRoot, name), devContextMetaFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ctx DevContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("failed to parse context %s: %w", name, err)
	}
	return &ctx, nil
}

// saveDevContext persists a dev context to disk atomically.
//
// Params:
//   - repoRoot: worktree root
//   - ctx: context to persist
//
// Returns:
//   - error: if the directory cannot be created or the file cannot be written
func saveDevContext(repoRoot string, ctx *DevContext) error {
	dir := devCtxDir(repoRoot, ctx.Name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, devContextMetaFile)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// listDevContexts returns all dev contexts in the worktree, sorted by name.
// Updates liveness state based on PID checks.
//
// Params:
//   - repoRoot: worktree root
//
// Returns:
//   - []*DevContext: sorted list of contexts (empty slice if none)
//   - error: if the directory cannot be read (os.IsNotExist returns nil, nil)
func listDevContexts(repoRoot string) ([]*DevContext, error) {
	dir := filepath.Join(repoRoot, ".revyl", devContextsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var contexts []*DevContext
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), "_") {
			continue
		}
		ctx, loadErr := loadDevContext(repoRoot, entry.Name())
		if loadErr != nil {
			continue
		}
		pidPath := devCtxPIDPath(repoRoot, ctx.Name)
		if running, _ := isDevCtxProcessAlive(ctx.PID, ctx.StartedAtNano, pidPath); !running {
			ctx.State = devContextStateStopped
		}
		contexts = append(contexts, ctx)
	}
	sort.Slice(contexts, func(i, j int) bool {
		return contexts[i].Name < contexts[j].Name
	})
	return contexts, nil
}

// findLiveDevContexts returns the names of currently running dev contexts.
// Used to warn users when they start a test without --context while a dev
// loop is already active.
//
// Parameters:
//   - repoRoot: The project root directory
//
// Returns:
//   - []string: Names of contexts in "running" state, or nil on any error
func findLiveDevContexts(repoRoot string) []string {
	contexts, err := listDevContexts(repoRoot)
	if err != nil {
		return nil
	}
	var live []string
	for _, ctx := range contexts {
		if ctx.State == devContextStateRunning {
			live = append(live, ctx.Name)
		}
	}
	return live
}

// validateDevContextName rejects context names that could escape the
// dev-sessions directory via path traversal or contain problematic chars.
//
// Params:
//   - name: context name to validate
//
// Returns:
//   - error: if the name is empty, contains path separators, or has ".."
func validateDevContextName(name string) error {
	if name == "" {
		return fmt.Errorf("context name cannot be empty")
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return fmt.Errorf("context name %q contains invalid characters", name)
	}
	return nil
}

// resolveDevContextName resolves the target context name using the resolution rules:
//  1. If explicitContext is non-empty, use it.
//  2. Else if a current-context marker exists, use that.
//  3. Else if exactly one context exists, use it.
//  4. Else if no contexts exist, use "default".
//  5. Else fail with an ambiguity error.
//
// The resolved name is validated against path traversal before returning.
//
// Params:
//   - repoRoot: worktree root
//   - explicitContext: value from --context flag (may be empty)
//
// Returns:
//   - string: resolved context name
//   - error: if resolution is ambiguous or the name is invalid
func resolveDevContextName(repoRoot, explicitContext string) (string, error) {
	var resolved string
	if explicitContext != "" {
		resolved = explicitContext
	} else if current, err := readCurrentDevContext(repoRoot); err == nil && current != "" {
		resolved = current
	} else {
		contexts, err := listDevContexts(repoRoot)
		if err != nil || len(contexts) == 0 {
			resolved = defaultDevContextName
		} else if len(contexts) == 1 {
			resolved = contexts[0].Name
		} else {
			return "", fmt.Errorf("multiple dev contexts exist in this worktree; pass --context or run 'revyl dev list'")
		}
	}
	if err := validateDevContextName(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// readCurrentDevContext reads the current-context marker file.
func readCurrentDevContext(repoRoot string) (string, error) {
	path := filepath.Join(repoRoot, ".revyl", devContextsDir, devContextCurrentFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// setCurrentDevContext writes the current-context marker file.
//
// Params:
//   - repoRoot: worktree root
//   - name: context name to mark as current
//
// Returns:
//   - error: if the marker cannot be written
func setCurrentDevContext(repoRoot, name string) error {
	dir := filepath.Join(repoRoot, ".revyl", devContextsDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, devContextCurrentFile)
	return os.WriteFile(path, []byte(name+"\n"), 0644)
}

// isDevCtxProcessAlive checks if a PID belongs to the dev loop that wrote it.
// Guards against PID reuse by comparing the start-time nonce stored in the
// PID file against the value in the DevContext.
//
// Params:
//   - pid: process ID from context.json
//   - startedAtNano: nonce from context.json (0 means legacy, skip nonce check)
//   - pidFilePath: path to the PID file for nonce comparison
//
// Returns:
//   - bool: true if the process is alive AND the nonce matches
//   - error: from os.FindProcess or Signal
func isDevCtxProcessAlive(pid int, startedAtNano int64, pidFilePath string) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	if !isProcessAlive(pid) {
		return false, nil
	}
	if startedAtNano == 0 {
		return true, nil
	}
	_, fileNonce := readDevCtxPIDFile(pidFilePath)
	if fileNonce == 0 {
		return true, nil
	}
	return fileNonce == startedAtNano, nil
}

// writeDevCtxPIDFile writes a PID file with a start-time nonce.
// Format: "<pid> <unix_nano_nonce>\n"
func writeDevCtxPIDFile(path string, pid int, nonce int64) error {
	content := fmt.Sprintf("%d %d", pid, nonce)
	return os.WriteFile(path, []byte(content), 0644)
}

// readDevCtxPIDFile reads a PID file, returning the PID and optional nonce.
// Backward-compatible with old single-PID format.
func readDevCtxPIDFile(path string) (pid int, nonce int64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	parts := strings.Fields(strings.TrimSpace(string(data)))
	if len(parts) == 0 {
		return 0, 0
	}
	pid, _ = strconv.Atoi(parts[0])
	if len(parts) >= 2 {
		nonce, _ = strconv.ParseInt(parts[1], 10, 64)
	}
	return pid, nonce
}

// resolveSessionIndexByID re-resolves a session index from a session ID
// using the DeviceSessionManager. Session indices can shift between CLI
// invocations as SyncSessions reassigns them, so the saved SessionIndex
// in DevContext may be stale.
//
// Params:
//   - mgr: device session manager (already synced)
//   - sessionID: the durable session identifier
//
// Returns:
//   - int: current session index, or -1 if the session cannot be found
func resolveSessionIndexByID(mgr *mcppkg.DeviceSessionManager, sessionID string) int {
	if sessionID == "" || mgr == nil {
		return -1
	}
	sessions := mgr.ListSessions()
	for _, s := range sessions {
		if s.SessionID == sessionID {
			return s.Index
		}
	}
	return -1
}

// SessionReuseResult holds the outcome of trying to reuse a saved device
// session from a DevContext.
//
// Fields:
//   - Session: the resolved live device session
//   - SessionOwned: whether the context owns (created) this session
type SessionReuseResult struct {
	Session      *mcppkg.DeviceSession
	SessionOwned bool
}

// tryReuseDevContextSession checks whether a saved DevContext has a live,
// reusable device session. First tries the local session manager, then
// falls back to re-attaching via the backend API. Returns nil when the
// caller should provision a new session instead.
//
// Params:
//   - ctx: context for backend API calls
//   - mgr: device session manager (already synced)
//   - saved: the DevContext loaded from disk (must not be nil)
//   - requestedPlatform: the platform the caller resolved ("ios" or "android")
//
// Returns:
//   - *SessionReuseResult: non-nil if a live session was resolved
func tryReuseDevContextSession(
	ctx context.Context,
	mgr *mcppkg.DeviceSessionManager,
	saved *DevContext,
	requestedPlatform string,
) *SessionReuseResult {
	if saved == nil || saved.SessionID == "" {
		return nil
	}
	if saved.Platform != "" && saved.Platform != requestedPlatform {
		ui.PrintDebug("saved session platform %s != requested %s; skipping reuse", saved.Platform, requestedPlatform)
		return nil
	}

	if mgr == nil {
		return nil
	}

	idx := resolveSessionIndexByID(mgr, saved.SessionID)
	if idx >= 0 {
		session := mgr.GetSession(idx)
		if session != nil {
			checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
			alive, reason := mgr.CheckSessionAlive(checkCtx, session)
			checkCancel()
			if alive {
				ui.PrintInfo("Reusing attached device session %s", truncatePrefix(saved.SessionID, 8))
				return &SessionReuseResult{
					Session:      session,
					SessionOwned: saved.SessionOwned,
				}
			}
			ui.PrintDim("Attached session %s is no longer alive (%s); provisioning new session", truncatePrefix(saved.SessionID, 8), reason)
			return nil
		}
	}

	_, session, err := mgr.AttachBySessionID(ctx, saved.SessionID)
	if err != nil {
		ui.PrintDebug("could not re-attach session %s: %v; provisioning new session", truncatePrefix(saved.SessionID, 8), err)
		return nil
	}

	ui.PrintInfo("Re-attached device session %s", truncatePrefix(saved.SessionID, 8))
	return &SessionReuseResult{
		Session:      session,
		SessionOwned: saved.SessionOwned,
	}
}

// loadDevContextTunnel checks whether a named dev context has a running dev
// loop with an active hot-reload tunnel. Returns the tunnel and deep-link URLs
// only when the owning process is still alive and the URLs are populated.
//
// Params:
//   - repoRoot: worktree root
//   - ctxName: context name
//
// Returns:
//   - tunnelURL: the public tunnel URL (empty when ok is false)
//   - deepLinkURL: the deep link URL (empty when ok is false)
//   - ok: true only if the dev loop is running and has an active tunnel
func loadDevContextTunnel(repoRoot, ctxName string) (tunnelURL, deepLinkURL string, ok bool) {
	ctx, err := loadDevContext(repoRoot, ctxName)
	if err != nil || ctx == nil {
		return "", "", false
	}
	if ctx.TunnelURL == "" || ctx.DeepLinkURL == "" {
		return "", "", false
	}
	pidPath := devCtxPIDPath(repoRoot, ctxName)
	alive, _ := isDevCtxProcessAlive(ctx.PID, ctx.StartedAtNano, pidPath)
	if !alive {
		return "", "", false
	}
	return ctx.TunnelURL, ctx.DeepLinkURL, true
}

// forceCleanupDevContext performs best-effort local filesystem cleanup when
// a dev loop is force-killed (e.g. double Ctrl+C → os.Exit). It removes the
// PID file and marks the context as stopped so the next invocation does not
// see stale "running" state. No network calls are made.
//
// Params:
//   - repoRoot: worktree root
//   - ctxName: context name
func forceCleanupDevContext(repoRoot, ctxName string) {
	_ = os.Remove(devCtxPIDPath(repoRoot, ctxName))
	if ctx, err := loadDevContext(repoRoot, ctxName); err == nil {
		ctx.State = devContextStateStopped
		ctx.PID = 0
		ctx.TunnelURL = ""
		ctx.DeepLinkURL = ""
		_ = saveDevContext(repoRoot, ctx)
	}
}

// resolveDevCtxPlatformConflict checks whether the existing context's platform
// matches requestedPlatform. When there is no conflict (or no existing context)
// it returns ctxName unchanged. When a conflict is detected and the context was
// implicitly resolved (explicitContext == false), the user is prompted to create
// a new context named after the requested platform. If the user accepts, the
// new context name is returned so the caller proceeds with a fresh context.
//
// Params:
//   - repoRoot: worktree root
//   - ctxName: current context name
//   - requestedPlatform: the platform the caller resolved ("ios" or "android")
//   - explicitContext: true when the user passed --context explicitly
//
// Returns:
//   - string: the context name to use (may differ from ctxName on auto-create)
//   - error: if the conflict cannot be resolved
func resolveDevCtxPlatformConflict(repoRoot, ctxName, requestedPlatform string, explicitContext bool) (string, error) {
	existing, _ := loadDevContext(repoRoot, ctxName)
	if existing == nil || existing.Platform == "" {
		return ctxName, nil
	}
	if existing.Platform == requestedPlatform {
		return ctxName, nil
	}

	if explicitContext {
		return "", fmt.Errorf(
			"context '%s' is configured for %s, but --platform %s was requested",
			ctxName, existing.Platform, requestedPlatform,
		)
	}

	newName := suggestPlatformContextName(repoRoot, requestedPlatform)

	ui.PrintWarning("Context '%s' is configured for %s.", ctxName, existing.Platform)
	confirmed := ui.Confirm(fmt.Sprintf("Create a new '%s' context for %s?", newName, requestedPlatform))
	if !confirmed {
		return "", fmt.Errorf(
			"context '%s' is configured for %s, but --platform %s was requested\n"+
				"  Hint: use --context %s to target a different context",
			ctxName, existing.Platform, requestedPlatform, requestedPlatform,
		)
	}

	return newName, nil
}

// suggestPlatformContextName picks a context name for the given platform that
// does not collide with an existing context bound to a different platform.
// It tries the platform name first (e.g. "ios"), then appends a numeric suffix.
//
// Params:
//   - repoRoot: worktree root
//   - platform: target platform ("ios" or "android")
//
// Returns:
//   - string: a context name safe to use for the given platform
func suggestPlatformContextName(repoRoot, platform string) string {
	candidate := platform
	for i := 2; ; i++ {
		ctx, _ := loadDevContext(repoRoot, candidate)
		if ctx == nil || ctx.Platform == "" || ctx.Platform == platform {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", platform, i)
	}
}

// getDevContextFlag reads the --context flag from a cobra command.
// Falls back to empty string if not set.
func getDevContextFlag(cmd *cobra.Command) string {
	val, _ := cmd.Flags().GetString("context")
	return strings.TrimSpace(val)
}

// resolveDevCwd resolves the working directory to the repo root.
func resolveDevCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	if root, rootErr := config.FindRepoRoot(cwd); rootErr == nil {
		cwd = root
	}
	return cwd, nil
}

// ---------------------------------------------------------------------------
// Dev context subcommands
// ---------------------------------------------------------------------------

var devAttachCmd = &cobra.Command{
	Use:   "attach <session-id|index|active>",
	Short: "Attach a device session to a dev context",
	Long: `Bind an existing device session to a dev context.

Records the session as the primary device for the named context so that
subsequent 'revyl dev --context <name>' reuses it instead of provisioning
a new device session. The session must already be running.

This command does not start the local dev bootstrap (hot reload, tunnel,
rebuild cache) -- run 'revyl dev --context <name>' after attaching to
start the loop on the attached session.

When the dev loop exits, attached sessions are left running (they are
not owned by the context). Use 'revyl device stop' or the viewer to
end the session, or 'revyl dev stop' to detach it from the context.

If no --context is specified, uses the default context resolution rules.
If the target context does not exist, it is created using the session's
platform.

Examples:
  revyl dev attach active
  revyl dev attach 0
  revyl dev attach e2b927a6
  revyl dev attach active --context checkout`,
	Args: cobra.ExactArgs(1),
	RunE: runDevAttach,
}

var devListCmd = &cobra.Command{
	Use:   "list",
	Short: "List dev contexts in the current worktree",
	Long: `Show all dev contexts with their platform, state, session, and viewer URL.

Examples:
  revyl dev list`,
	RunE: runDevList,
}

var devContextUseCmd = &cobra.Command{
	Use:   "use <context>",
	Short: "Set the current dev context",
	Long: `Mark a context as current so status, rebuild, and stop
target it by default.

If the context has a primary device session, also makes that session
the active revyl device session.

Examples:
  revyl dev use ios-main
  revyl dev use default`,
	Args: cobra.ExactArgs(1),
	RunE: runDevContextUse,
}

var devStopCmd = &cobra.Command{
	Use:   "stop [context]",
	Short: "Stop a dev context",
	Long: `Stop the local dev loop for the specified context.

If the context created its device session, that session is stopped too.
If the context attached to an existing session, only the local dev
bootstrap is stopped and the device session is left running.

With --all, stops every context in the current worktree.

Examples:
  revyl dev stop
  revyl dev stop ios-main
  revyl dev stop --all`,
	RunE: runDevStop,
}

var devStopAll bool

func runDevAttach(cmd *cobra.Command, args []string) error {
	cwd, err := resolveDevCwd()
	if err != nil {
		return err
	}

	contextName, err := resolveDevContextName(cwd, getDevContextFlag(cmd))
	if err != nil {
		return err
	}

	existing, _ := loadDevContext(cwd, contextName)
	if existing != nil {
		pidPath := devCtxPIDPath(cwd, contextName)
		if running, _ := isDevCtxProcessAlive(existing.PID, existing.StartedAtNano, pidPath); running {
			return fmt.Errorf(
				"context '%s' already has a running dev loop (PID %d);\n"+
					"use a different --context name or run 'revyl dev stop %s' first",
				contextName, existing.PID, contextName,
			)
		}
		if existing.SessionID != "" {
			return fmt.Errorf(
				"context '%s' already has a primary device session (%s);\n"+
					"create a new context or run 'revyl dev stop %s' first",
				contextName, existing.SessionID, contextName,
			)
		}
	}

	mgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}

	target := strings.TrimSpace(args[0])
	var session *mcppkg.DeviceSession
	var sessionIdx int

	switch {
	case target == "active":
		session, err = mgr.ResolveSession(-1)
		if err != nil {
			return fmt.Errorf("no active device session to attach: %w", err)
		}
		sessionIdx = session.Index
	default:
		if idx, parseErr := strconv.Atoi(target); parseErr == nil {
			session = mgr.GetSession(idx)
			if session == nil {
				return fmt.Errorf("no device session at index %d", idx)
			}
			sessionIdx = idx
		} else {
			sessionIdx, session, err = mgr.AttachBySessionID(cmd.Context(), target)
			if err != nil {
				return fmt.Errorf("failed to attach to session %s: %w", target, err)
			}
		}
	}

	if existing != nil && existing.Platform != "" && existing.Platform != session.Platform {
		return fmt.Errorf(
			"device session platform %s does not match context '%s' (%s)",
			session.Platform, contextName, existing.Platform,
		)
	}

	now := time.Now()
	ctx := &DevContext{
		Name:         contextName,
		Platform:     session.Platform,
		SessionID:    session.SessionID,
		SessionIndex: sessionIdx,
		SessionOwned: false,
		ViewerURL:    session.ViewerURL,
		PID:          0,
		State:        devContextStateStopped,
		CreatedAt:    now,
		LastActivity: now,
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	if cfg, cfgErr := config.LoadProjectConfig(configPath); cfgErr == nil {
		if cfg.HotReload.IsConfigured() {
			registry := hotreload.DefaultRegistry()
			if prov, provCfg, selErr := registry.SelectProvider(&cfg.HotReload, "", cwd); selErr == nil {
				ctx.Provider = prov.Name()
				if provCfg != nil {
					ctx.Port = provCfg.GetPort(prov.Name())
				}
			}
		} else if cfg.Build.System != "" {
			ctx.Provider = cfg.Build.System
		}
		for key := range cfg.Build.Platforms {
			keyLower := strings.ToLower(key)
			if strings.Contains(keyLower, session.Platform) {
				ctx.PlatformKey = key
				break
			}
		}
	}
	if err := saveDevContext(cwd, ctx); err != nil {
		return fmt.Errorf("failed to save context: %w", err)
	}
	if err := setCurrentDevContext(cwd, contextName); err != nil {
		ui.PrintDebug("failed to set current context: %v", err)
	}
	if err := mgr.SetActive(sessionIdx); err != nil {
		ui.PrintDebug("failed to set active session: %v", err)
	}

	ui.PrintSuccess("Attached session %s to dev context '%s' (%s)", session.SessionID, contextName, session.Platform)
	if session.ViewerURL != "" {
		ui.PrintLink("Viewer", session.ViewerURL)
	}
	ui.Println()
	ui.PrintInfo("Start the dev loop on this session:")
	ui.PrintDim("  revyl dev --context %s", contextName)
	ui.Println()
	ui.PrintInfo("Or interact directly:")
	ui.PrintDim("  revyl device screenshot")
	ui.PrintDim("  revyl device tap --target \"Login button\"")

	return nil
}

func runDevList(cmd *cobra.Command, _ []string) error {
	cwd, err := resolveDevCwd()
	if err != nil {
		return err
	}

	contexts, err := listDevContexts(cwd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Flags().GetBool("json")

	if len(contexts) == 0 {
		if jsonOutput {
			fmt.Println("[]")
		} else {
			ui.PrintDim("No dev contexts in this worktree.")
			ui.PrintDim("  Start one: revyl dev")
		}
		return nil
	}

	current, _ := readCurrentDevContext(cwd)

	if jsonOutput {
		out, _ := json.MarshalIndent(contexts, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("  %-3s %-16s %-10s %-10s %-8s %s\n", "", "CONTEXT", "PLATFORM", "STATE", "SESSION", "VIEWER")
	for _, ctx := range contexts {
		marker := "  "
		if ctx.Name == current {
			marker = "* "
		}
		sessionShort := ""
		if ctx.SessionID != "" && len(ctx.SessionID) >= 8 {
			sessionShort = ctx.SessionID[:8]
		} else {
			sessionShort = ctx.SessionID
		}
		ownership := ""
		if !ctx.SessionOwned && ctx.SessionID != "" {
			ownership = " (attached)"
		}
		fmt.Printf("%s %-16s %-10s %-10s %-8s %s%s\n",
			marker, ctx.Name, ctx.Platform, ctx.State, sessionShort, ctx.ViewerURL, ownership)
	}
	return nil
}

func runDevContextUse(cmd *cobra.Command, args []string) error {
	cwd, err := resolveDevCwd()
	if err != nil {
		return err
	}

	name := strings.TrimSpace(args[0])
	if err := validateDevContextName(name); err != nil {
		return err
	}
	ctx, loadErr := loadDevContext(cwd, name)
	if loadErr != nil {
		return fmt.Errorf("context '%s' not found; run 'revyl dev list' to see available contexts", name)
	}

	if err := setCurrentDevContext(cwd, name); err != nil {
		return fmt.Errorf("failed to set current context: %w", err)
	}

	if ctx.SessionID != "" {
		mgr, mgrErr := getDeviceSessionMgr(cmd)
		if mgrErr == nil {
			idx := resolveSessionIndexByID(mgr, ctx.SessionID)
			if idx >= 0 {
				if setErr := mgr.SetActive(idx); setErr != nil {
					ui.PrintDebug("could not set active device session: %v", setErr)
				}
			} else {
				ui.PrintDebug("session %s no longer active; skipping SetActive", ctx.SessionID)
			}
		}
	}

	ui.PrintSuccess("Switched to dev context '%s' (%s)", name, ctx.Platform)
	if running, _ := isDevCtxProcessAlive(ctx.PID, ctx.StartedAtNano, devCtxPIDPath(cwd, name)); running {
		ui.PrintDim("  Dev loop running (PID %d)", ctx.PID)
	} else {
		ui.PrintDim("  Dev loop not running; start with: revyl dev --context %s", name)
	}
	return nil
}

func runDevStop(cmd *cobra.Command, args []string) error {
	cwd, err := resolveDevCwd()
	if err != nil {
		return err
	}

	if devStopAll {
		return stopAllDevContexts(cmd, cwd)
	}

	var contextName string
	if len(args) > 0 {
		contextName = strings.TrimSpace(args[0])
		if err := validateDevContextName(contextName); err != nil {
			return err
		}
	} else {
		contextName, err = resolveDevContextName(cwd, getDevContextFlag(cmd))
		if err != nil {
			return err
		}
	}

	return stopOneDevContext(cmd, cwd, contextName)
}

func stopOneDevContext(cmd *cobra.Command, cwd, name string) error {
	ctx, loadErr := loadDevContext(cwd, name)
	if loadErr != nil {
		return fmt.Errorf("context '%s' not found", name)
	}

	if running, _ := isDevCtxProcessAlive(ctx.PID, ctx.StartedAtNano, devCtxPIDPath(cwd, name)); running {
		proc, _ := os.FindProcess(ctx.PID)
		if proc != nil {
			_ = proc.Signal(syscall.SIGTERM)
			ui.PrintInfo("Sent stop signal to dev loop (PID %d)", ctx.PID)
		}
	}

	if ctx.SessionOwned && ctx.SessionID != "" {
		mgr, mgrErr := getDeviceSessionMgr(cmd)
		if mgrErr == nil {
			idx := resolveSessionIndexByID(mgr, ctx.SessionID)
			if idx >= 0 {
				if stopErr := mgr.StopSession(cmd.Context(), idx); stopErr != nil {
					ui.PrintDebug("failed to stop device session: %v", stopErr)
				} else {
					ui.PrintInfo("Stopped device session %s", ctx.SessionID)
				}
			} else {
				ui.PrintDim("Device session %s no longer active; skipping stop", ctx.SessionID)
			}
		}
	} else if ctx.SessionID != "" {
		ui.PrintWarning("Device session %s is still running (attached, not owned by this context)", ctx.SessionID)
		ui.PrintDim("  To stop the device session:  revyl device stop")
		ui.PrintDim("  To reuse it in a new loop:   revyl dev --context %s", name)
	}

	ctx.State = devContextStateStopped
	ctx.PID = 0
	ctx.SessionID = ""
	ctx.SessionIndex = 0
	ctx.ViewerURL = ""
	_ = saveDevContext(cwd, ctx)

	ui.PrintSuccess("Stopped dev context '%s'", name)
	if ctx.SessionOwned {
		ui.PrintDim("Device session was stopped with the context.")
	}
	return nil
}

func stopAllDevContexts(cmd *cobra.Command, cwd string) error {
	contexts, err := listDevContexts(cwd)
	if err != nil {
		return err
	}
	if len(contexts) == 0 {
		ui.PrintDim("No dev contexts to stop.")
		return nil
	}
	for _, ctx := range contexts {
		if err := stopOneDevContext(cmd, cwd, ctx.Name); err != nil {
			ui.PrintWarning("Failed to stop context '%s': %v", ctx.Name, err)
		}
	}
	return nil
}

// printDevContextAlreadyRunning prints a summary when a context is already
// active and the user tries to start it again.
func printDevContextAlreadyRunning(ctx *DevContext) {
	ui.PrintSuccess("Dev context '%s' is already running (PID %d)", ctx.Name, ctx.PID)
	if ctx.ViewerURL != "" {
		ui.PrintLink("Viewer", ctx.ViewerURL)
	}
	ui.PrintInfo("Session: %s (%s)", ctx.SessionID, ctx.Platform)
	if ctx.SessionOwned {
		ui.PrintDim("  Session is owned by this context and will stop when the context stops.")
	} else {
		ui.PrintDim("  Session is attached (not owned). Stopping this context leaves the device running.")
	}
	ui.Println()
	ui.PrintDim("  revyl dev status --context %s    # check context state", ctx.Name)
	ui.PrintDim("  revyl dev rebuild --context %s   # trigger a rebuild", ctx.Name)
	ui.PrintDim("  revyl dev stop --context %s      # stop this context", ctx.Name)
	ui.Println()
	ui.PrintInfo("Interact with the device:")
	ui.PrintDim("    revyl device tap --target \"Login button\" -s %d    # AI-grounded tap", ctx.SessionIndex)
	ui.PrintDim("    revyl device instruction \"log in and verify\" -s %d  # multi-step AI instruction", ctx.SessionIndex)
	ui.PrintDim("    revyl device screenshot -s %d                       # save a screenshot locally", ctx.SessionIndex)
	ui.Println()
	ui.PrintDim("  Attach from another terminal:")
	ui.PrintDim("    revyl dev attach %s", ctx.Name)
}
