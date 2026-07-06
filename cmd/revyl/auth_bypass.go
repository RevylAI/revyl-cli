package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

// orgLaunchVarLister is the api.Client surface needed to resolve ${VAR}
// placeholders in the auth bypass deep link.
type orgLaunchVarLister interface {
	ListOrgLaunchVariables(ctx context.Context) (*api.OrgLaunchVariablesResponse, error)
}

// authBypassRuntime applies a project's auth_bypass config to device sessions:
// launch vars at session start and the deep link after each app (re)launch.
// Expiry recovery is agent-driven: re-mint the org launch vars with the repo's
// own script, then `revyl dev auth refresh` re-fires the deep link.
type authBypassRuntime struct {
	cfg    *config.AuthBypassConfig
	client orgLaunchVarLister
}

// devAuthBypass is the active runtime for the current command invocation.
// Initialized by initDevAuthBypass from paths that load the project config.
var devAuthBypass *authBypassRuntime

// authBypassPlaceholderRe matches ${VAR} placeholders in an auth bypass deep
// link. It mirrors AUTH_BYPASS_PLACEHOLDER_RE in the backend
// (cognisim_backend/app/services/scm_config_file.py) so the CLI and the
// preview/proof path resolve deep links identically. Unlike os.Expand it does
// NOT expand the bare $VAR form or treat $$ / $5 as special vars.
var authBypassPlaceholderRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// initDevAuthBypass activates auth bypass handling for this invocation when
// the project config has an auth_bypass section. Safe to call more than once:
// a reload whose config no longer configures auth bypass clears any previously
// active runtime so a removed auth_bypass section stops firing.
func initDevAuthBypass(cfg *config.ProjectConfig, client orgLaunchVarLister) {
	if cfg == nil || !cfg.AuthBypass.IsConfigured() {
		devAuthBypass = nil
		return
	}
	devAuthBypass = &authBypassRuntime{
		cfg:    cfg.AuthBypass,
		client: client,
	}
}

// LaunchVarKeys returns the configured org launch-variable keys.
func (r *authBypassRuntime) LaunchVarKeys() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.cfg.LaunchVars...)
}

// ResolveDeepLink returns the deep link with ${VAR} placeholders substituted
// from current org launch-variable values. Returns "" when no deep link is
// configured.
func (r *authBypassRuntime) ResolveDeepLink(ctx context.Context) (string, error) {
	if r == nil {
		return "", nil
	}
	link := strings.TrimSpace(r.cfg.DeepLink)
	if link == "" {
		return "", nil
	}
	if !strings.Contains(link, "${") {
		return link, nil
	}
	resp, err := r.client.ListOrgLaunchVariables(ctx)
	if err != nil {
		return "", fmt.Errorf("could not resolve auth bypass deep link variables: %w", err)
	}
	values := make(map[string]string, len(resp.Result))
	for _, v := range resp.Result {
		values[v.Key] = v.Value
	}
	var missing []string
	resolved := authBypassPlaceholderRe.ReplaceAllStringFunc(link, func(match string) string {
		key := match[2 : len(match)-1] // strip "${" and "}"
		if value, ok := values[key]; ok {
			return value
		}
		missing = append(missing, key)
		return ""
	})
	if len(missing) > 0 {
		return "", fmt.Errorf(
			"auth bypass deep link references org launch vars that don't exist: %s (mint them, e.g. `revyl global launch-var add`)",
			strings.Join(missing, ", "),
		)
	}
	return resolved, nil
}

// FireDeepLink resolves and opens the auth deep link on the session's device.
// Used after app (re)launches; the initial session start authenticates via
// StartSessionOptions.AppLink instead.
func (r *authBypassRuntime) FireDeepLink(ctx context.Context, requester workerSessionRequester, sessionIndex int) error {
	if r == nil {
		return nil
	}
	link, err := r.ResolveDeepLink(ctx)
	if err != nil {
		return err
	}
	if link == "" {
		return nil
	}
	if _, err := requester.WorkerRequestForSession(ctx, sessionIndex, "/open_url", map[string]string{"url": link}); err != nil {
		return fmt.Errorf("auth bypass deep link failed to open: %w", err)
	}
	ui.PrintDim("Fired auth bypass deep link")
	return nil
}

// authBypassStatus is the auth_bypass block surfaced in `revyl dev status`.
type authBypassStatus struct {
	Configured bool     `json:"configured"`
	LaunchVars []string `json:"launch_vars,omitempty"`
	DeepLink   bool     `json:"deep_link_configured"`
}

// Status reports auth bypass state for dev status consumers. Nil receiver
// (auth bypass not configured) returns nil so the JSON field is omitted.
func (r *authBypassRuntime) Status() *authBypassStatus {
	if r == nil {
		return nil
	}
	return &authBypassStatus{
		Configured: true,
		LaunchVars: r.LaunchVarKeys(),
		DeepLink:   strings.TrimSpace(r.cfg.DeepLink) != "",
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
// options: config launch vars when no explicit --launch-var flags were given,
// and the auth deep link as the post-launch AppLink when the caller has none
// (hot-reload loops keep their dev-client link and fire the auth link after).
func applyAuthBypassSessionDefaults(ctx context.Context, opts mcppkg.StartSessionOptions) mcppkg.StartSessionOptions {
	if devAuthBypass == nil {
		return opts
	}
	if len(opts.LaunchVars) == 0 {
		opts.LaunchVars = devAuthBypass.LaunchVarKeys()
	}
	if strings.TrimSpace(opts.AppLink) == "" {
		link, err := devAuthBypass.ResolveDeepLink(ctx)
		if err != nil {
			ui.PrintWarning("%v", err)
		} else if link != "" {
			opts.AppLink = link
		}
	}
	return opts
}

var devAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage auth bypass state for the running dev session",
}

var devAuthRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Re-fire the auth bypass deep link with fresh launch-var values",
	Long: `Re-resolve the auth_bypass deep link from current org launch-variable values
and re-open it on the active dev session's device. Use when the app under test
shows a logged-out state mid-session (expired mint). Re-mint the launch vars
first with your project's own mint script if the values themselves expired.`,
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
	initDevAuthBypass(cfg, deviceMgr.APIClient())

	session, err := resolveSessionFlag(cmd, deviceMgr)
	if err != nil {
		return devAuthRefreshError(cmd, "no_session",
			fmt.Sprintf("no active device session: %v", err),
			"start one with `revyl dev` or `revyl device start`")
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
