package main

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/revyl/cli/internal/beforesession"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

// rememberBeforeSessionBootValues persists minted values for later auth refresh.
//
// Parameters:
//   - workDir: Project root that owns `.revyl/`.
//   - sessionID: Device session the values were minted for.
func rememberBeforeSessionBootValues(workDir, sessionID string) {
	values := devBeforeSession.Values()
	if len(values) == 0 {
		return
	}
	_ = beforesession.SaveSessionValues(workDir, sessionID, values)
}

// hydrateBeforeSessionForRefresh restores boot-time before_session values so
// auth refresh re-fires the deep link with the same token the app received as
// launch environment. It never re-runs the mint script.
//
// Parameters:
//   - workDir: Project root that owns `.revyl/`.
//   - sessionID: Active device session to hydrate.
//
// Returns:
//   - error: Load failure from the local boot-values cache.
func hydrateBeforeSessionForRefresh(workDir, sessionID string) error {
	if devBeforeSession == nil {
		return nil
	}
	if len(devBeforeSession.Values()) > 0 {
		return nil
	}
	values, err := beforesession.LoadSessionValues(workDir, sessionID)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	devBeforeSession.RestoreValues(values)
	return nil
}

// beforeSessionRuntime runs a project's before_session script before each
// device session starts and holds the values it produced for that session.
//
// It is the producer half of the pair whose consumer is authBypassRuntime:
// before_session mints values, auth_bypass applies them. The dependency runs
// one way only, so the two blocks keep independent failure semantics —
// auth_bypass stays best-effort, before_session is fatal.
type beforeSessionRuntime struct {
	cfg      *config.AuthoredBeforeScript
	repoRoot string

	mu        sync.RWMutex
	values    map[string]string
	state     string
	lastError string
}

// devBeforeSession is the active runtime for the current command invocation.
// Initialized by initDevBeforeSession from paths that load the project config.
var devBeforeSession *beforeSessionRuntime

// deepLinkPlaceholderPattern matches the ${KEY} placeholders a deep link may
// carry, using the same key rule as launch variables.
var deepLinkPlaceholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]{0,127})\}`)

// initDevBeforeSession activates before-session handling for this invocation
// when the project config declares a before_session script.
//
// Safe to call more than once: a reload whose config no longer declares a
// script clears any previously active runtime, and an unchanged config keeps
// the running session's already-minted values so a rebuild can re-fire the
// deep link.
//
// Parameters:
//   - cfg: The canonical before_script block, or nil when none was configured.
//   - repoRoot: Absolute path to the repository root, used as the script's
//     working directory and containment boundary.
func initDevBeforeSession(cfg *config.AuthoredBeforeScript, repoRoot string) {
	if authoredBeforeScriptPath(cfg) == "" {
		devBeforeSession = nil
		return
	}
	if devBeforeSession.matchesConfig(cfg, repoRoot) {
		return
	}
	devBeforeSession = &beforeSessionRuntime{
		cfg:      cloneAuthoredBeforeScript(cfg),
		repoRoot: repoRoot,
		state:    "pending",
	}
}

// matchesConfig reports whether a reload preserves the active before-session
// behavior, so an unrelated config edit does not discard minted values.
//
// Parameters:
//   - cfg: Reloaded before-session configuration.
//   - repoRoot: Repository root resolved for the reload.
//
// Returns:
//   - bool: True when the configurations have equivalent runtime behavior.
func (r *beforeSessionRuntime) matchesConfig(cfg *config.AuthoredBeforeScript, repoRoot string) bool {
	if r == nil || r.cfg == nil || cfg == nil {
		return false
	}
	return r.repoRoot == repoRoot &&
		authoredBeforeScriptPath(r.cfg) == authoredBeforeScriptPath(cfg) &&
		authoredBeforeScriptTimeoutSeconds(r.cfg) == authoredBeforeScriptTimeoutSeconds(cfg)
}

// Run executes the before_session script and records the values it printed.
//
// Called once per device session start so every session gets freshly minted
// values. A nil receiver (no before_session configured) is a no-op.
//
// Parameters:
//   - ctx: Cancellation scope for the script run.
//
// Returns:
//   - error: A secret-free failure that must abort session start. Unlike an
//     auth-bypass failure, this is never downgraded to a warning: starting a
//     session against an unprepared app produces a confident wrong result.
func (r *beforeSessionRuntime) Run(ctx context.Context) error {
	if r == nil {
		return nil
	}

	result, err := beforesession.Run(ctx, r.repoRoot, r.cfg)

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.values = nil
		r.state = "failed"
		r.lastError = err.Error()
		return err
	}
	r.values = result.Values
	r.state = "ready"
	r.lastError = ""
	return nil
}

// Values returns the session-scoped values produced by the last run.
func (r *beforeSessionRuntime) Values() map[string]string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(r.values))
	for key, value := range r.values {
		copied[key] = value
	}
	return copied
}

// RestoreValues installs previously minted boot values without re-running the
// script. Used by auth refresh so the deep link keeps matching launch env.
//
// Parameters:
//   - values: KEY=VALUE pairs minted at session start for this device session.
func (r *beforeSessionRuntime) RestoreValues(values map[string]string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(values) == 0 {
		r.values = nil
		r.state = "pending"
		r.lastError = ""
		return
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	r.values = copied
	r.state = "ready"
	r.lastError = ""
}

// beforeSessionStatus is the before_session block surfaced in `revyl dev status`.
// Produced keys are reported by name only; values never leave the process.
type beforeSessionStatus struct {
	Configured bool     `json:"configured"`
	Script     string   `json:"script"`
	Keys       []string `json:"keys,omitempty"`
	State      string   `json:"state"`
	Error      string   `json:"error,omitempty"`
}

// Status reports before-session state for dev status consumers. Nil receiver
// (no script configured) returns nil so the JSON field is omitted.
func (r *beforeSessionRuntime) Status() *beforeSessionStatus {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := make([]string, 0, len(r.values))
	for key := range r.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return &beforeSessionStatus{
		Configured: true,
		Script:     authoredBeforeScriptPath(r.cfg),
		Keys:       keys,
		State:      r.state,
		Error:      r.lastError,
	}
}

func authoredBeforeScriptPath(cfg *config.AuthoredBeforeScript) string {
	if cfg == nil || cfg.ScriptPath == nil {
		return ""
	}
	return strings.TrimSpace(*cfg.ScriptPath)
}

func authoredBeforeScriptTimeoutSeconds(cfg *config.AuthoredBeforeScript) int {
	if cfg != nil && cfg.TimeoutSeconds != nil && *cfg.TimeoutSeconds > 0 {
		return *cfg.TimeoutSeconds
	}
	return config.DefaultBeforeSessionTimeoutSeconds
}

func cloneAuthoredBeforeScript(cfg *config.AuthoredBeforeScript) *config.AuthoredBeforeScript {
	if cfg == nil {
		return nil
	}
	cloned := &config.AuthoredBeforeScript{}
	if cfg.ScriptPath != nil {
		value := *cfg.ScriptPath
		cloned.ScriptPath = &value
	}
	if cfg.TimeoutSeconds != nil {
		value := *cfg.TimeoutSeconds
		cloned.TimeoutSeconds = &value
	}
	return cloned
}

// prepareSessionStartOptions runs before_session and folds both its values and
// the auth_bypass defaults into session start options.
//
// Every device-session start goes through here, so the script always completes
// before the device boots. That ordering is required rather than convenient:
// launch environment is fixed at boot, which is the same reason
// `revyl dev auth refresh` re-fires the boot deep link and must not remint.
//
// Parameters:
//   - ctx: Cancellation scope for the script run.
//   - opts: Session start options assembled by the caller.
//
// Returns:
//   - mcppkg.StartSessionOptions: Options with launch vars and session-scoped
//     launch environment applied.
//   - error: A secret-free setup failure. Callers must abort session start;
//     because no session exists yet there is nothing to tear down.
func prepareSessionStartOptions(
	ctx context.Context,
	opts mcppkg.StartSessionOptions,
) (mcppkg.StartSessionOptions, error) {
	if err := devBeforeSession.Run(ctx); err != nil {
		return opts, err
	}

	opts = applyAuthBypassSessionDefaults(ctx, opts)

	values := devBeforeSession.Values()
	if err := validateDeepLinkResolution(values); err != nil {
		return opts, err
	}
	return applyBeforeSessionLaunchEnv(opts, values), nil
}

// applyBeforeSessionLaunchEnv merges minted values into the session's launch
// environment.
//
// Values ride on the session itself rather than org launch variables, so
// concurrent sessions — several PR proof runs on one repo, for example — can
// never read or overwrite each other's tokens.
//
// Parameters:
//   - opts: Session start options with auth-bypass defaults already applied.
//   - values: Values printed by the before_session script.
//
// Returns:
//   - mcppkg.StartSessionOptions: Options carrying the session-scoped values.
//     Explicit --launch-env entries win; any key supplied inline is dropped
//     from LaunchVars so it is not also looked up in org state, where it may
//     not exist at all.
func applyBeforeSessionLaunchEnv(
	opts mcppkg.StartSessionOptions,
	values map[string]string,
) mcppkg.StartSessionOptions {
	if len(values) == 0 {
		return opts
	}

	merged := make(map[string]string, len(opts.LaunchEnv)+len(values))
	for key, value := range values {
		merged[key] = value
	}
	for key, value := range opts.LaunchEnv {
		merged[key] = value
	}
	opts.LaunchEnv = merged

	if len(opts.LaunchVars) > 0 {
		remaining := make([]string, 0, len(opts.LaunchVars))
		for _, keyOrID := range opts.LaunchVars {
			if _, inline := values[strings.TrimSpace(keyOrID)]; inline {
				continue
			}
			remaining = append(remaining, keyOrID)
		}
		opts.LaunchVars = remaining
	}

	return opts
}

// validateDeepLinkResolution rejects an auth deep link that would need both
// session-scoped and org-scoped values.
//
// A mixed template is unimplementable rather than merely awkward: the backend
// resolves ${VAR} only from org launch-variable rows attached to the session
// and cannot see an inline value, while local substitution cannot read org
// secrets. Failing here keeps the failure at setup time instead of producing a
// half-substituted URL the app silently rejects.
//
// Parameters:
//   - values: Values printed by the before_session script.
//
// Returns:
//   - error: Non-nil when the configured deep link mixes the two channels,
//     naming the keys the script must also print.
func validateDeepLinkResolution(values map[string]string) error {
	if devAuthBypass == nil || len(values) == 0 {
		return nil
	}
	template := authoredAuthBypassDeepLink(devAuthBypass.cfg)
	if template == "" {
		return nil
	}

	var inline, orgOnly []string
	for _, key := range deepLinkPlaceholders(template) {
		if _, ok := values[key]; ok {
			inline = append(inline, key)
			continue
		}
		orgOnly = append(orgOnly, key)
	}
	if len(inline) == 0 || len(orgOnly) == 0 {
		return nil
	}

	return fmt.Errorf(
		"session.auth_bypass.deep_link mixes value sources: %s come from session.before_script but %s must resolve from org launch variables. "+
			"Print every placeholder from the script, or none of them",
		strings.Join(inline, ", "),
		strings.Join(orgOnly, ", "),
	)
}

// deepLinkPlaceholders returns the distinct ${KEY} names in a deep link
// template, in first-appearance order.
func deepLinkPlaceholders(template string) []string {
	matches := deepLinkPlaceholderPattern.FindAllStringSubmatch(template, -1)
	keys := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		key := match[1]
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

// applySessionValuesToDeepLink substitutes session-scoped values into a deep
// link template so a fully inline link is posted as a literal URL.
//
// Mixed templates cannot reach here — validateDeepLinkResolution rejects them
// before the session starts — so the result either has no placeholders left
// (fully inline) or is returned unchanged for backend resolution (fully org).
//
// Parameters:
//   - template: The configured auth_bypass deep link.
//
// Returns:
//   - string: The template with any inline placeholders substituted.
func applySessionValuesToDeepLink(template string) string {
	values := devBeforeSession.Values()
	if len(values) == 0 {
		return template
	}
	resolved := template
	for key, value := range values {
		resolved = strings.ReplaceAll(resolved, "${"+key+"}", value)
	}
	return resolved
}
