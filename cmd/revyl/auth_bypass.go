package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

// authBypassRuntime applies a project's auth_bypass config to device sessions:
// launch vars at session start and the deep link after each app (re)launch.
// Mid-session recovery re-fires that same deep link via `revyl dev auth
// refresh` without reminting: launch environment is fixed at boot, so a new
// token would fail apps that compare the link to REVYL_AUTH_BYPASS_TOKEN.
// True token expiry requires `revyl dev stop` then a fresh session.
type authBypassRuntime struct {
	cfg       *config.AuthBypassConfig
	mu        sync.RWMutex
	state     string
	lastError string
}

// devAuthBypass is the active runtime for the current command invocation.
// Initialized by initDevAuthBypass from paths that load the project config.
var devAuthBypass *authBypassRuntime

// initDevAuthBypass activates auth bypass handling for this invocation when
// the project config has an auth_bypass section. Safe to call more than once:
// a reload whose config no longer configures auth bypass clears any previously
// active runtime so a removed auth_bypass section stops firing.
func initDevAuthBypass(cfg *config.ProjectConfig) {
	if cfg == nil || !cfg.AuthBypass.IsConfigured() {
		devAuthBypass = nil
		return
	}
	if devAuthBypass.matchesConfig(cfg.AuthBypass) {
		return
	}
	state := "configured"
	if strings.TrimSpace(cfg.AuthBypass.DeepLink) != "" {
		state = "pending"
	}
	devAuthBypass = &authBypassRuntime{
		cfg:   cfg.AuthBypass,
		state: state,
	}
}

// matchesConfig reports whether a reload preserves the active auth-bypass behavior.
//
// Parameters:
//   - cfg: Reloaded auth-bypass configuration to compare with the active runtime.
//
// Returns:
//   - bool: True when the configurations have equivalent runtime behavior.
func (r *authBypassRuntime) matchesConfig(cfg *config.AuthBypassConfig) bool {
	if r == nil || r.cfg == nil || cfg == nil {
		return false
	}
	return slices.Equal(r.cfg.LaunchVars, cfg.LaunchVars) &&
		strings.TrimSpace(r.cfg.DeepLink) == strings.TrimSpace(cfg.DeepLink)
}

// LaunchVarKeys returns the configured org launch-variable keys.
func (r *authBypassRuntime) LaunchVarKeys() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.cfg.LaunchVars...)
}

// FireDeepLink asks the existing worker proxy to resolve and open the auth
// template with launch variables already attached to the session.
func (r *authBypassRuntime) FireDeepLink(ctx context.Context, requester workerSessionRequester, sessionIndex int) error {
	if r == nil {
		return nil
	}
	template := strings.TrimSpace(r.cfg.DeepLink)
	if template == "" {
		return nil
	}
	// A before_session script may have minted some placeholders locally; those
	// are substituted here so the link posts as a literal URL, since the
	// backend can only resolve from org launch variables.
	err := openURLAfterLaunch(ctx, requester, sessionIndex, applySessionValuesToDeepLink(template))
	if err != nil {
		publicError := authBypassPublicError(err)
		r.setAttemptState("failed", publicError)
		return errors.New(publicError)
	}
	r.setAttemptState("ready", "")
	ui.PrintDim("Fired auth bypass deep link")
	return nil
}

// openURLAfterLaunch opens a literal URL or delegates template resolution to
// the existing backend worker proxy.
func openURLAfterLaunch(ctx context.Context, requester workerSessionRequester, sessionIndex int, urlOrTemplate string) error {
	value := strings.TrimSpace(urlOrTemplate)
	if value == "" {
		return nil
	}
	path := "/open_url"
	body := interface{}(api.DeviceOpenURLRequest{URL: value})
	if strings.Contains(value, "${") {
		path = "/open_url_template"
		body = api.DeviceOpenURLTemplateRequest{URLTemplate: value}
	}
	respBody, err := requester.WorkerRequestForSession(ctx, sessionIndex, path, body)
	if err != nil {
		return err
	}
	return ensureWorkerActionSucceeded(respBody, "open_url")
}

// authBypassPublicError converts an internal failure into a secret-free message.
//
// Parameters:
//   - err: Internal worker, transport, or cancellation failure.
//
// Returns:
//   - string: Stable message safe for CLI, MCP, and persisted status output.
func authBypassPublicError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "auth bypass deep link request was cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "auth bypass deep link request timed out"
	}

	var workerErr *mcppkg.WorkerHTTPError
	if errors.As(err, &workerErr) {
		return fmt.Sprintf(
			"auth bypass deep link failed to open (worker status %d)",
			workerErr.StatusCode,
		)
	}
	return "auth bypass deep link failed to open"
}

// setAttemptState records a secret-free auth-bypass outcome for dev status.
//
// Parameters:
//   - state: Public auth-bypass lifecycle state.
//   - publicError: Sanitized message safe for persisted status output.
func (r *authBypassRuntime) setAttemptState(state string, publicError string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = state
	r.lastError = strings.TrimSpace(publicError)
}

// authBypassStatus is the auth_bypass block surfaced in `revyl dev status`.
type authBypassStatus struct {
	Configured bool     `json:"configured"`
	LaunchVars []string `json:"launch_vars,omitempty"`
	DeepLink   bool     `json:"deep_link_configured"`
	State      string   `json:"state"`
	Error      string   `json:"error,omitempty"`
}

// Status reports auth bypass state for dev status consumers. Nil receiver
// (auth bypass not configured) returns nil so the JSON field is omitted.
func (r *authBypassRuntime) Status() *authBypassStatus {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &authBypassStatus{
		Configured: true,
		LaunchVars: r.LaunchVarKeys(),
		DeepLink:   strings.TrimSpace(r.cfg.DeepLink) != "",
		State:      r.state,
		Error:      r.lastError,
	}
}

// fireAuthBypassAfterLaunch re-authenticates the app after a (re)launch or
// session reuse. Warn-only: an auth failure should never kill the dev loop.
func fireAuthBypassAfterLaunch(ctx context.Context, requester workerSessionRequester, sessionIndex int) {
	if devAuthBypass == nil {
		return
	}
	if err := devAuthBypass.FireDeepLink(ctx, requester, sessionIndex); err != nil {
		ui.PrintWarning("%v", err)
	}
}

// applyAuthBypassSessionDefaults merges auth bypass defaults into session start
// options while preserving explicit and inherited launch variables.
// Session-start callers fire the deep-link template after the app is ready
// through the worker proxy so secret values never cross into the CLI.
func applyAuthBypassSessionDefaults(_ context.Context, opts mcppkg.StartSessionOptions) mcppkg.StartSessionOptions {
	if devAuthBypass == nil {
		return opts
	}
	seen := make(map[string]struct{}, len(opts.LaunchVars))
	for _, launchVar := range opts.LaunchVars {
		seen[launchVar] = struct{}{}
	}
	for _, launchVar := range devAuthBypass.LaunchVarKeys() {
		if _, exists := seen[launchVar]; exists {
			continue
		}
		opts.LaunchVars = append(opts.LaunchVars, launchVar)
		seen[launchVar] = struct{}{}
	}
	return opts
}

var devAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage auth bypass state for the running dev session",
}

var devAuthRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Re-fire the auth bypass deep link from session boot values",
	Long: `Re-open the auth_bypass deep link on the active dev session using the same
launch values applied at session boot. Use when the app under test shows a
logged-out state mid-session but the boot token is still valid.

Does not remint before_session or org launch variables: launch environment is
fixed at boot, and apps that compare the deep-link token to
REVYL_AUTH_BYPASS_TOKEN (or another launch-env gate) reject a newly minted
value. If the token itself expired, run ` + "`revyl dev stop`" + ` then
` + "`revyl dev`" + ` so a fresh mint is applied as launch environment.`,
	Example: `  revyl dev auth refresh
  revyl dev auth refresh --json`,
	RunE: runDevAuthRefresh,
}

// devAuthRefreshError is the structured expected-failure output for
// `revyl dev auth refresh --json`: JSON on stdout, concise error on stderr,
// non-zero exit, never a usage dump.
func devAuthRefreshError(cmd *cobra.Command, code, message, action string) error {
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"ok":      false,
			"code":    code,
			"message": message,
			"action":  action,
		}, "", "  ")
		fmt.Println(string(data))
	}
	return fmt.Errorf("%s", message)
}

func runDevAuthRefresh(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if repoRoot, rootErr := config.FindRepoRoot(cwd); rootErr == nil {
		cwd = repoRoot
	}
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		return devAuthRefreshError(cmd, "no_config",
			fmt.Sprintf("failed to load %s: %v", configPath, err),
			"run `revyl init` in the app directory")
	}
	if !cfg.AuthBypass.IsConfigured() {
		return devAuthRefreshError(cmd, "no_auth_bypass",
			fmt.Sprintf("no auth_bypass section in %s", configPath),
			"add an auth_bypass section (see the revyl-cli-auth-bypass skill)")
	}
	if strings.TrimSpace(cfg.AuthBypass.DeepLink) == "" {
		return devAuthRefreshError(cmd, "no_deep_link",
			"auth_bypass.deep_link is not set — launch vars apply only at session boot",
			"restart_session: run `revyl dev stop` then `revyl dev` so the refreshed launch vars apply")
	}

	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}
	initDevAuthBypass(cfg)

	// Reuse boot-time before_session values. Reminting here would put a new
	// token in the deep link while launch env stays fixed at session boot.
	initDevBeforeSession(cfg, cwd)

	session, err := resolveSessionFlag(cmd, deviceMgr)
	if err != nil {
		return devAuthRefreshError(cmd, "no_session",
			fmt.Sprintf("no active device session: %v", err),
			"start one with `revyl dev` or `revyl device start`")
	}
	if err := hydrateBeforeSessionForRefresh(deviceMgr.WorkDir(), session.SessionID); err != nil {
		return devAuthRefreshError(cmd, "before_session_hydrate_failed", err.Error(),
			"restart_session: run `revyl dev stop` then `revyl dev` so before_session mints again at boot")
	}

	if err := devAuthBypass.FireDeepLink(cmd.Context(), deviceMgr, session.Index); err != nil {
		return devAuthRefreshError(cmd, "deep_link_failed",
			err.Error(),
			"check the device session is alive (`revyl dev status`)")
	}

	result := map[string]interface{}{
		"ok":              true,
		"deep_link_fired": true,
		"session_id":      session.SessionID,
		"session_index":   session.Index,
	}
	jsonOrPrint(cmd, result, "Auth bypass deep link re-fired")
	return nil
}

func init() {
	devAuthRefreshCmd.Flags().IntP("s", "s", -1, "Session index to target (-1 for active)")
	devAuthRefreshCmd.Flags().Bool("json", false, "Output result as JSON")
	devAuthCmd.AddCommand(devAuthRefreshCmd)
	devCmd.AddCommand(devAuthCmd)
}
