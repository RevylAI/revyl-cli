package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/launchenv"
)

var webCommitPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

var (
	webURLSchemePattern       = regexp.MustCompile(`(?i)^([a-z][a-z\d+.-]*)://`)
	webURLNonPublicSuffixes   = []string{".localhost", ".local", ".internal", ".home.arpa"}
	webURLSharedAddressPrefix = netip.MustParsePrefix("100.64.0.0/10")
	errWebURLInvalid          = errors.New("Enter a website address, such as example.com.")
	errWebURLNonPublic        = errors.New("Revyl can't reach localhost or private addresses.")
)

// NormalizeWebURL returns the website URL a Web URL build registers, following
// the backend's normalize_website_url: a missing scheme becomes https, http is
// upgraded, and credentials, query strings, fragments, localhost, single-label
// hosts, and private addresses are rejected. The backend still canonicalizes
// internationalized hosts and alternate IP spellings itself.
func NormalizeWebURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 2048 || strings.ContainsRune(trimmed, '\\') || strings.IndexFunc(trimmed, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errWebURLInvalid
	}
	rest := trimmed
	if scheme := webURLSchemePattern.FindStringSubmatch(trimmed); scheme != nil {
		if name := strings.ToLower(scheme[1]); name != "http" && name != "https" {
			return "", errWebURLInvalid
		}
		rest = trimmed[len(scheme[0]):]
	}
	authority, path := rest, ""
	if index := strings.IndexAny(rest, "/?#"); index >= 0 {
		authority, path = rest[:index], rest[index:]
	}
	if strings.Contains(authority, "@") {
		return "", errors.New("Remove the username and password from the address.")
	}
	if strings.ContainsAny(path, "?#") {
		return "", errors.New("Remove the part after ? or # from the address.")
	}
	parsed, err := url.Parse("https://" + authority)
	if err != nil || parsed.Hostname() == "" {
		return "", errWebURLInvalid
	}
	port := parsed.Port()
	if port != "" {
		if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
			return "", errWebURLInvalid
		} else if number == 443 {
			port = ""
		}
	}
	host := strings.ToLower(parsed.Hostname())
	name := strings.TrimSuffix(host, ".")
	if address, err := netip.ParseAddr(host); err == nil {
		if address.Is4In6() || !address.IsGlobalUnicast() || address.IsPrivate() || webURLSharedAddressPrefix.Contains(address) {
			return "", errWebURLNonPublic
		}
		if address.Is6() {
			host = "[" + host + "]"
		}
	} else if name == "localhost" || slices.ContainsFunc(webURLNonPublicSuffixes, func(suffix string) bool { return strings.HasSuffix(name, suffix) }) {
		return "", errWebURLNonPublic
	} else if !strings.Contains(name, ".") {
		return "", errors.New("Enter the full domain, such as example.com.")
	}
	if port != "" {
		host += ":" + port
	}
	return "https://" + host + path, nil
}

var webLaunchEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

var webRuntimeReservedEnvNames = []string{"PATH", "HOME", "LANG", "PORT", "HOSTNAME", "WEB_APP_BOOTSTRAP"}

// ValidateWebLaunchEnv bounds a Web session's plain and secret environment
// overrides the way the backend does. Errors name keys, never values.
func ValidateWebLaunchEnv(plain, secret map[string]string) error {
	env := make(map[string]string, len(plain)+len(secret))
	for name, value := range plain {
		env[name] = value
	}
	for name, value := range secret {
		if _, ok := plain[name]; ok {
			return fmt.Errorf("launch_env %s is either plain or secret, not both", name)
		}
		if value == "" {
			return fmt.Errorf("secret launch_env %s is empty", name)
		}
		env[name] = value
	}
	if len(env) > 32 {
		return fmt.Errorf("a Web session accepts at most 32 launch_env overrides")
	}
	totalBytes := 0
	for name, value := range env {
		totalBytes += len(name) + len(value)
		if !webLaunchEnvNamePattern.MatchString(name) {
			return fmt.Errorf("launch_env name %q must match [A-Za-z_][A-Za-z0-9_]{0,127} on platform web", name)
		}
		if err := launchenv.KeyError(name); err != nil {
			return fmt.Errorf("invalid launch_env name: %w", err)
		}
		if slices.Contains(webRuntimeReservedEnvNames, strings.ToUpper(name)) {
			return fmt.Errorf("launch_env name %q is reserved by the Web runtime", name)
		}
		if len(value) > 4096 {
			return fmt.Errorf("launch_env %s exceeds 4096 bytes", name)
		}
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("launch_env %s contains control characters", name)
		}
	}
	if totalBytes > 128*1024 {
		return fmt.Errorf("launch_env overrides exceed the Web runtime's 131072-byte environment limit")
	}
	return nil
}

func ValidateWebSessionStartOptions(opts StartSessionOptions) error {
	if strings.ToLower(strings.TrimSpace(opts.Platform)) != "web" {
		if opts.Web != nil || opts.WebURL != "" || opts.WebCommitSHA != "" {
			return fmt.Errorf("url, commit, and viewport are only available on platform web")
		}
		return nil
	}
	appID, err := uuid.Parse(strings.TrimSpace(opts.AppID))
	if err != nil || appID == uuid.Nil {
		return fmt.Errorf("platform web requires an app UUID; pass --app or set build.profiles.<name>.web.app_id (MCP: app_id)")
	}
	if opts.BuildVersionID != "" {
		buildID, err := uuid.Parse(strings.TrimSpace(opts.BuildVersionID))
		if err != nil || buildID == uuid.Nil {
			return fmt.Errorf("build_version_id must be a build UUID")
		}
	}
	if opts.IdleTimeout < 0 {
		return fmt.Errorf("idle_timeout must be positive on platform web")
	}
	var secretLaunchEnv map[string]string
	if opts.Web != nil {
		secretLaunchEnv = opts.Web.SecretEnvVars
	}
	if err := ValidateWebLaunchEnv(opts.LaunchEnv, secretLaunchEnv); err != nil {
		return err
	}
	phoneInputs := []struct {
		name    string
		present bool
	}{
		{"app_url", opts.AppURL != ""}, {"app_link", opts.AppLink != ""}, {"app_package", opts.AppPackage != ""},
		{"launch_vars", len(opts.LaunchVars) > 0}, {"launch_arg_sets", len(opts.LaunchArgSets) > 0}, {"launch_arguments", len(opts.LaunchArguments) > 0},
		{"disable_inherited_launch_vars", opts.DisableInheritedLaunchVars}, {"test_id", opts.TestID != ""},
		{"skip_app_install", opts.SkipAppInstall}, {"orientation", opts.InitialOrientation != ""}, {"locale", opts.InitialLocale != ""},
		{"device_model", opts.DeviceModel != ""}, {"os_version", opts.OsVersion != ""}, {"device_runner_id", opts.DeviceRunnerID != ""},
	}
	for _, input := range phoneInputs {
		if input.present {
			return fmt.Errorf("%s is not available on platform web", input.name)
		}
	}
	if err := (api.BuildSelection{BranchID: opts.BranchID, BranchName: opts.BranchName}).Validate(); err != nil {
		return err
	}
	branchSelected := opts.BranchID != "" || opts.BranchName != ""
	switch api.BuildSelectionMode(opts.BuildSelection) {
	case "", api.BuildSelectionModeLatest:
	case api.BuildSelectionModeLatestAllBranches:
		if branchSelected || opts.BuildVersionID != "" || opts.WebURL != "" {
			return fmt.Errorf("latest_all_branches takes no branch, build_version_id, or url")
		}
	default:
		return fmt.Errorf("build_selection %s is not available on platform web; Web starts open the newest build on the selected branch (latest) or across all branches (latest_all_branches), or an exact build_version_id", opts.BuildSelection)
	}
	if branchSelected && opts.BuildVersionID != "" {
		return fmt.Errorf("provide only one of branch or build_version_id")
	}
	if opts.WebURL != "" {
		if opts.BuildVersionID != "" {
			return fmt.Errorf("provide only one of url or build_version_id")
		}
		if _, err := NormalizeWebURL(opts.WebURL); err != nil {
			return err
		}
	} else if opts.WebCommitSHA != "" {
		return fmt.Errorf("commit requires url on platform web")
	}
	if opts.WebCommitSHA != "" && !webCommitPattern.MatchString(opts.WebCommitSHA) {
		return fmt.Errorf("commit must be a lowercase hexadecimal SHA of 7 to 40 characters")
	}
	if opts.Web != nil && opts.Web.Viewport != nil {
		v := opts.Web.Viewport
		if v.Width != nil && (*v.Width < 320 || *v.Width > 3840) || v.Height != nil && (*v.Height < 240 || *v.Height > 2160) || v.DeviceScaleFactor != nil && (math.IsNaN(*v.DeviceScaleFactor) || math.IsInf(*v.DeviceScaleFactor, 0) || *v.DeviceScaleFactor < 1 || *v.DeviceScaleFactor > 3) {
			return fmt.Errorf("viewport requires width 320–3840, height 240–2160, and device_scale_factor 1–3")
		}
	}
	return nil
}

// WebSignInFromSession maps project session.auth_bypass.sign_in onto the web
// start request. Only the secret's name leaves the project config.
func WebSignInFromSession(session *config.AuthoredSession) *api.DeviceWebSignIn {
	if session == nil || session.AuthBypass == nil || session.AuthBypass.SignIn == nil {
		return nil
	}
	signIn := session.AuthBypass.SignIn
	return &api.DeviceWebSignIn{Path: stringValue(signIn.Path), SecretName: stringValue(signIn.Secret)}
}

// PrepareWebStartArtifact resolves the Web app and build a start runs,
// registering opts.WebURL as a URL build when it is set, or returns
// opts.ResolvedWebArtifact when the caller already prepared it.
// The app is checked before a URL build is registered; a build lookup, which
// writes nothing, runs alongside the app read.
func (m *DeviceSessionManager) PrepareWebStartArtifact(ctx context.Context, opts StartSessionOptions) (startdevice.ResolvedStartArtifact, error) {
	if opts.ResolvedWebArtifact != nil {
		return *opts.ResolvedWebArtifact, nil
	}
	appID := strings.TrimSpace(opts.AppID)
	checkApp := func() error {
		app, err := m.apiClient.GetApp(ctx, appID)
		if err != nil {
			return err
		}
		if app == nil || app.ID != appID || !strings.EqualFold(app.Platform, "web") {
			return fmt.Errorf("platform web requires a Web app")
		}
		return nil
	}
	if opts.WebURL != "" {
		if err := checkApp(); err != nil {
			return startdevice.ResolvedStartArtifact{}, err
		}
		return m.resolveWebStartBuild(ctx, appID, opts)
	}
	var appErr error
	var appRead sync.WaitGroup
	appRead.Go(func() { appErr = checkApp() })
	artifact, err := m.resolveWebStartBuild(ctx, appID, opts)
	appRead.Wait()
	if appErr != nil {
		return startdevice.ResolvedStartArtifact{}, appErr
	}
	return artifact, err
}

func (m *DeviceSessionManager) resolveWebStartBuild(ctx context.Context, appID string, opts StartSessionOptions) (startdevice.ResolvedStartArtifact, error) {
	var err error
	buildID := strings.TrimSpace(opts.BuildVersionID)
	var build *api.WebBuild
	artifact := startdevice.ResolvedStartArtifact{AppID: appID}
	if opts.WebURL != "" {
		webURL, urlErr := NormalizeWebURL(opts.WebURL)
		if urlErr != nil {
			return startdevice.ResolvedStartArtifact{}, urlErr
		}
		build, err = m.apiClient.CreateExternalURLBuild(ctx, appID, &api.ExternalURLBuildRequest{BranchID: opts.BranchID, BranchName: opts.BranchName, URL: webURL, CommitSHA: opts.WebCommitSHA})
	} else {
		if buildID == "" {
			latest, err := m.apiClient.GetLatestWebBuild(ctx, appID, api.BuildSelection{
				BranchID: opts.BranchID, BranchName: opts.BranchName,
				GitBranchName: opts.GitBranchName, Mode: api.BuildSelectionMode(opts.BuildSelection),
			})
			if err != nil {
				return startdevice.ResolvedStartArtifact{}, err
			}
			if latest == nil {
				return startdevice.ResolvedStartArtifact{}, fmt.Errorf("no Web build found; run 'revyl build --platform web' or register a URL with --app and --url (MCP: app_id and url)")
			}
			buildID = latest.ID
			artifact.BranchID, artifact.BranchName = latest.BranchID, latest.BranchName
			artifact.BranchSource, artifact.GitBranchName = latest.BranchSource, latest.GitBranchName
			artifact.AutoGitBranchEnabled = latest.AutoGitBranchEnabled
		}
		build, err = m.apiClient.GetWebBuild(ctx, buildID)
	}
	if err != nil {
		return startdevice.ResolvedStartArtifact{}, err
	}
	if build == nil || build.AppID != appID || !api.IsWebBuildArtifactKind(build.ArtifactKind) || (buildID != "" && build.ID != buildID) || (opts.WebURL != "" && build.ArtifactKind != "web_external") {
		return startdevice.ResolvedStartArtifact{}, fmt.Errorf("build must be a URL or bundle build on the selected Web app")
	}
	parsedID, err := uuid.Parse(build.ID)
	if err != nil || parsedID == uuid.Nil {
		return startdevice.ResolvedStartArtifact{}, fmt.Errorf("Web build response omitted a valid build ID")
	}
	artifact.BuildID = build.ID
	artifact.ArtifactKind = build.ArtifactKind
	return artifact, nil
}

func isWebSessionDetail(detail *api.DeviceSessionDetail) bool {
	return detail != nil && detail.Platform != nil && *detail.Platform == "web"
}

func webFailureReason(detail *api.DeviceSessionDetail) string {
	if !isWebSessionDetail(detail) {
		return ""
	}
	return detail.FailureReason()
}

func webSessionEndedBeforeReadyError(detail *api.DeviceSessionDetail) error {
	reason := webFailureReason(detail)
	switch {
	case reason == "":
		return fmt.Errorf("web session ended before a ready observation")
	case detail.Status == "failed":
		return fmt.Errorf("web session failed before it was ready: %s", reason)
	default:
		return fmt.Errorf("web session ended before it was ready: %s", reason)
	}
}

func webSessionEndedReason(detail *api.DeviceSessionDetail) string {
	reason := webFailureReason(detail)
	switch {
	case reason == "":
		return "web session has ended or is stopping"
	case detail.Status == "failed":
		return "web session failed: " + reason
	default:
		return "web session ended: " + reason
	}
}

func applyBackendRuntimeInfo(session *DeviceSession, detail *api.DeviceSessionDetail) {
	if session.Platform != "web" {
		return
	}
	if detail.BuildID != "" {
		session.BuildID = detail.BuildID
	}
	session.BuildVersion = stringValue(detail.BuildVersion)
	session.CommitSHA = stringValue(detail.CommitSHA)
	if detail.TraceID != nil {
		session.TraceID = stringValue(detail.TraceID)
	}
	applyBackendScreenDimensions(session, detail.ScreenWidth, detail.ScreenHeight)
}
