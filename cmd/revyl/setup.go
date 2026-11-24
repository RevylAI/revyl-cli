package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/build"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

const (
	setupDetectMaxDepth = 4
	setupDetectMaxApps  = 25
	setupCheckBodyLimit = 256 << 10
	setupCheckRedirects = 5
)

var (
	setupDetectSkippedDirs          = map[string]bool{"node_modules": true, "Pods": true, "build": true, "DerivedData": true, "vendor": true, "dist": true, "out": true, "coverage": true, "target": true, "tmp": true}
	setupExamplePathPart            = regexp.MustCompile(`^(examples?|samples?|demos?|fixtures?|templates?|tests?|e2e|__tests__|playground)$`)
	setupStandaloneMobileFrameworks = map[string]bool{"expo": true, "react-native": true, "flutter": true}
	setupStaticAssetDirs            = map[string]bool{"public": true, "static": true, "assets": true, "templates": true, "src": true}
	setupDocsPathPart               = regexp.MustCompile(`^(docs?|documentation|website-docs)$`)
	setupPageTitlePatterns          = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`),
		regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)=["'](?:og:site_name|og:title|application-name|apple-mobile-web-app-title)["'][^>]+content=["']([^"']*)["']`),
	}
	setupSignInPathPattern = regexp.MustCompile(`(?i)/(log-?in|sign-?in|auth|sso)(/|$)`)

	setupPlatformNames       = map[string]string{"ios": "iOS", "android": "Android", "web": "Web"}
	setupOutcomes            = []string{"created", "existing", "skipped", "failed"}
	setupOutcomeReasons      = []string{"user_declined", "no_reachable_url", "url_blocked", "needs_user", "not_the_product", "build_failed", "session_failed", "billing_or_quota", "not_supported", "other"}
	setupLookupHostAddresses = net.DefaultResolver.LookupNetIP
	setupDialCheckedAddress  = (&net.Dialer{}).DialContext
	errSetupInsecureRedirect = errors.New("the address redirects to a non-HTTPS address")
	errSetupNonPublicAddress = errors.New("the address resolves to a private network")
	setupCheckHTTPClient     = func(timeout time.Duration) *http.Client {
		return &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{DialContext: dialSetupCheckAddress, TLSHandshakeTimeout: timeout, ForceAttemptHTTP2: true},
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if request.URL.Scheme != "https" {
					return errSetupInsecureRedirect
				}
				if len(via) >= setupCheckRedirects {
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
	}
)

// dialSetupCheckAddress connects only to public addresses, checked after DNS
// and pinned for the connection, so neither a host name nor a redirect can
// lead check-url to a service Revyl's cloud browser couldn't reach. It
// connects directly, without a proxy, because a proxy would hide the address.
func dialSetupCheckAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses := []netip.Addr{}
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = append(addresses, literal)
	} else if addresses, err = setupLookupHostAddresses(ctx, "ip", host); err != nil {
		return nil, err
	}
	if len(addresses) == 0 || slices.ContainsFunc(addresses, func(address netip.Addr) bool { return mcppkg.IsNonPublicWebAddress(address.Unmap()) }) {
		return nil, errSetupNonPublicAddress
	}
	return setupDialCheckedAddress(ctx, network, net.JoinHostPort(addresses[0].Unmap().String(), port))
}

func newSetupCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "setup",
		Short: "Find the apps in a repository and check what Revyl can open",
		Long: `Commands a coding agent runs while it sets Revyl up for a repository.

  detect     find every iOS, Android, and Web app below a folder
  check-url  check that a Web app's address answers before Revyl opens it
  report     record how setting up one platform ended

Revyl's hosted setup instructions (https://revyl.com/agent-setup/prompt.md)
run them in order.`,
	}
	command.AddCommand(newSetupDetectCommand(), newSetupCheckURLCommand(), newSetupReportCommand())
	return command
}

type setupDetectedApp struct {
	Path      string        `json:"path"`
	Platforms []string      `json:"platforms"`
	Framework string        `json:"framework"`
	Role      string        `json:"role"`
	Web       *build.WebApp `json:"web,omitempty"`
}

type setupDetectOutput struct {
	Platforms []string           `json:"platforms"`
	Apps      []setupDetectedApp `json:"apps"`
	Truncated bool               `json:"truncated,omitempty"`
}

func newSetupDetectCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "detect [folder]",
		Short: "Find every iOS, Android, and Web app below a folder",
		Long: `Scan a folder, the current one by default, and the folders below it for apps
Revyl can run, without changing anything.

Mobile apps are Expo, React Native, Flutter, Xcode, Gradle, Swift, Bazel, and
Kotlin Multiplatform projects. Web apps are Next.js, Remix, React Router,
Astro, Nuxt, SvelteKit, SolidStart, Angular, Gatsby, Docusaurus, Create React
App, Vue CLI, Vite, and other bundler-based single-page apps, plus a plain
index.html site outside other apps. A Capacitor project counts as both.

For each Web app the output names the local dev command and port, and public
addresses its own files name (package.json homepage, CNAME, astro or
docusaurus site settings, fly.toml, and .env.example). It takes addresses only
from regular files in the app's folder, never from .env files or through a
symlink, and never fetches anything; check an address with revyl setup check-url.

Each app has a role: app, docs, or example. "platforms" lists the platforms
of the apps whose role is app, or of every app when none is.`,
		Example: `  revyl setup detect
  revyl setup detect apps --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: runSetupDetect,
	}
	command.Flags().Bool("json", false, "Print the apps as JSON")
	return command
}

func runSetupDetect(cmd *cobra.Command, args []string) error {
	recordSetupRef(cmd)
	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return usageErrorf("can't read folder %s: %v", root, err)
	}
	if info, err := os.Stat(absoluteRoot); err != nil || !info.IsDir() {
		return usageErrorf("%s is not a folder; pass the repository or app folder to scan", root)
	}
	output := detectSetupApps(absoluteRoot)
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Domain:       "setup_detect",
		DomainStatus: setupPlatformSetStatus(output.Platforms),
	})
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		return writeSetupJSON(cmd.OutOrStdout(), output)
	}
	printSetupDetectOutput(output)
	return nil
}

func detectSetupApps(root string) setupDetectOutput {
	output := setupDetectOutput{Apps: []setupDetectedApp{}}
	nativeDirsOfMobileApps := map[string]bool{}
	var appDirs []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if relative != "." {
			name := entry.Name()
			if setupDetectSkippedDirs[name] || strings.HasPrefix(name, ".") || strings.Count(relative, "/")+1 > setupDetectMaxDepth || nativeDirsOfMobileApps[path] {
				return filepath.SkipDir
			}
		}
		parentPlatforms := map[string]bool{}
		for index, dir := range appDirs {
			if strings.HasPrefix(path, dir+string(filepath.Separator)) {
				for _, platform := range output.Apps[index].Platforms {
					parentPlatforms[platform] = true
				}
			}
		}
		staticSite := len(parentPlatforms) == 0 && !setupStaticAssetDirs[entry.Name()]
		app, found := detectSetupApp(path, relative, staticSite, parentPlatforms)
		if !found {
			return nil
		}
		if len(output.Apps) == setupDetectMaxApps {
			output.Truncated = true
			return filepath.SkipAll
		}
		output.Apps = append(output.Apps, app)
		appDirs = append(appDirs, path)
		for _, platform := range app.Platforms {
			if platform != "web" {
				nativeDirsOfMobileApps[filepath.Join(path, platform)] = true
			}
		}
		return nil
	})
	productPlatforms, allPlatforms := map[string]bool{}, map[string]bool{}
	for _, app := range output.Apps {
		for _, platform := range app.Platforms {
			allPlatforms[platform] = true
			if app.Role == "app" {
				productPlatforms[platform] = true
			}
		}
	}
	if len(productPlatforms) == 0 {
		productPlatforms = allPlatforms
	}
	output.Platforms = sortedSetupPlatforms(productPlatforms)
	return output
}

func detectSetupApp(dir, relative string, staticSite bool, parentPlatforms map[string]bool) (setupDetectedApp, bool) {
	platforms := map[string]bool{}
	framework := ""
	if detected, err := build.Detect(dir); err == nil && detected.System != build.SystemUnknown {
		for platform := range detected.Platforms {
			if platform == "ios" || platform == "android" {
				platforms[platform] = true
			}
		}
		if detected.Platform == "ios" || detected.Platform == "android" {
			platforms[detected.Platform] = true
		}
		if len(platforms) > 0 {
			framework = setupMobileFramework(detected.System)
		}
	}
	if !setupStandaloneMobileFrameworks[framework] {
		for platform := range parentPlatforms {
			delete(platforms, platform)
		}
		if len(platforms) == 0 {
			framework = ""
		}
	}
	webApp, isWeb := build.DetectWebApp(dir, staticSite)
	if isWeb {
		platforms["web"] = true
		if framework == "" {
			framework = webApp.Framework
		} else {
			framework += "+" + webApp.Framework
		}
	}
	if len(platforms) == 0 {
		return setupDetectedApp{}, false
	}
	app := setupDetectedApp{Path: relative, Platforms: sortedSetupPlatforms(platforms), Framework: framework, Role: setupAppRole(relative, webApp)}
	if isWeb {
		app.Web = webApp
	}
	return app, true
}

func setupMobileFramework(system build.BuildSystem) string {
	switch system {
	case build.SystemExpo:
		return "expo"
	case build.SystemReactNative:
		return "react-native"
	case build.SystemFlutter:
		return "flutter"
	case build.SystemXcode:
		return "xcode"
	case build.SystemGradle:
		return "gradle"
	case build.SystemSwift:
		return "swift"
	case build.SystemBazel:
		return "bazel"
	case build.SystemKMP:
		return "kmp"
	default:
		return "unknown"
	}
}

func setupAppRole(relative string, webApp *build.WebApp) string {
	for _, part := range strings.Split(relative, "/") {
		if setupExamplePathPart.MatchString(strings.ToLower(part)) {
			return "example"
		}
	}
	if webApp != nil && webApp.Framework == "docusaurus" {
		return "docs"
	}
	for _, part := range strings.Split(relative, "/") {
		if setupDocsPathPart.MatchString(strings.ToLower(part)) {
			return "docs"
		}
	}
	return "app"
}

func sortedSetupPlatforms(platforms map[string]bool) []string {
	ordered := []string{}
	for _, platform := range []string{"ios", "android", "web"} {
		if platforms[platform] {
			ordered = append(ordered, platform)
		}
	}
	return ordered
}

func setupPlatformSetStatus(platforms []string) string {
	if len(platforms) == 0 {
		return "none"
	}
	return strings.Join(platforms, "+")
}

func printSetupDetectOutput(output setupDetectOutput) {
	if len(output.Apps) == 0 {
		ui.PrintWarning("No iOS, Android, or Web app found in this folder or the folders below it")
		ui.PrintDim("Run it from the repository root, or pass the app folder: revyl setup detect <folder>")
		return
	}
	names := make([]string, 0, len(output.Platforms))
	for _, platform := range output.Platforms {
		names = append(names, setupPlatformNames[platform])
	}
	ui.PrintSuccess("Found %s", strings.Join(names, ", "))
	for _, app := range output.Apps {
		platformNames := make([]string, 0, len(app.Platforms))
		for _, platform := range app.Platforms {
			platformNames = append(platformNames, setupPlatformNames[platform])
		}
		role := ""
		if app.Role != "app" {
			role = " [" + app.Role + "]"
		}
		ui.PrintInfo("%s  %s (%s)%s", app.Path, strings.Join(platformNames, ", "), app.Framework, role)
		if app.Web == nil {
			continue
		}
		if app.Web.DevCommand != "" {
			ui.PrintDim("  dev server: %s (port %d)", app.Web.DevCommand, app.Web.DevPort)
		}
		for _, hint := range app.Web.URLHints {
			ui.PrintDim("  address: %s (from %s)", hint.URL, hint.Source)
		}
	}
	if output.Truncated {
		ui.PrintDim("Stopped after %d apps; pass a narrower folder to see the rest.", setupDetectMaxApps)
	}
}

type setupCheckURLOutput struct {
	URL        string `json:"url"`
	FinalURL   string `json:"final_url,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	Outcome    string `json:"outcome"`
	Reachable  bool   `json:"reachable"`
	SignInPage bool   `json:"sign_in_page,omitempty"`
	Title      string `json:"title,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

func newSetupCheckURLCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "check-url <url>",
		Short: "Check that a Web app's address answers before Revyl opens it",
		Long: `Check a Web app's public address the way Revyl's cloud browser will need it:
the address must be public, answer over HTTPS, and return a page rather than
an error, a bot check, or a dev server that refuses the host.

The address follows the same rules as revyl device start --url: a missing
scheme becomes https, and localhost and private addresses are refused because
Revyl's browser runs in the cloud. Expose a local dev server through a public
HTTPS tunnel first.

The request comes from this machine, so an address only this network can reach
can still pass here and fail in Revyl. Exits 1 unless the outcome is reachable.

Outcomes: reachable, invalid_url, private_address, unreachable, timeout,
http_error, access_denied, bot_check, dev_server_host_blocked.`,
		Example: `  revyl setup check-url my-app.vercel.app
  revyl setup check-url https://abc.trycloudflare.com --json`,
		Args: cobra.ExactArgs(1),
		RunE: runSetupCheckURL,
	}
	command.Flags().Bool("json", false, "Print the result as JSON")
	command.Flags().Duration("timeout", 20*time.Second, "How long to wait for the page")
	return command
}

func runSetupCheckURL(cmd *cobra.Command, args []string) error {
	recordSetupRef(cmd)
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if timeout <= 0 || timeout > 2*time.Minute {
		return usageErrorf("--timeout must be between 1s and 2m")
	}
	output := checkSetupURL(cmd.Context(), args[0], setupCheckHTTPClient(timeout))
	completion := analytics.CommandCompletion{Domain: "setup_check_url", DomainStatus: output.Outcome}
	analytics.SetCommandCompletion(cmd.Context(), completion)
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		if err := writeSetupJSON(cmd.OutOrStdout(), output); err != nil {
			return err
		}
	} else if output.Reachable {
		ui.PrintSuccess("%s answers (HTTP %d)", output.URL, output.StatusCode)
		if output.Title != "" {
			ui.PrintInfo("Page title: %s", output.Title)
		}
		if output.SignInPage {
			ui.PrintInfo("%s", output.Hint)
		}
	} else {
		ui.PrintWarning("%s is not ready for Revyl: %s", setupCheckDisplayURL(output), output.Outcome)
		ui.PrintInfo("%s", output.Hint)
	}
	if output.Reachable {
		return nil
	}
	completion.ExitCode = 1
	return analytics.CompletedWithExitCode(fmt.Errorf("address check ended %s", output.Outcome), completion)
}

func setupCheckDisplayURL(output setupCheckURLOutput) string {
	if output.URL != "" {
		return output.URL
	}
	return "The address"
}

func checkSetupURL(ctx context.Context, raw string, client *http.Client) setupCheckURLOutput {
	normalized, err := mcppkg.NormalizeWebURL(raw)
	if err != nil {
		if mcppkg.IsNonPublicWebURL(err) {
			return setupCheckURLOutput{Outcome: "private_address", Hint: setupPrivateAddressHint}
		}
		return setupCheckURLOutput{Outcome: "invalid_url", Hint: err.Error()}
	}
	output := setupCheckURLOutput{URL: normalized}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		output.Outcome, output.Hint = "invalid_url", "Enter a website address, such as example.com."
		return output
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; revyl-cli setup check)")
	request.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	response, err := client.Do(request)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, errSetupNonPublicAddress) {
			output.Outcome, output.Hint = "private_address", setupPrivateAddressHint
		} else if errors.Is(err, errSetupInsecureRedirect) {
			output.Outcome, output.Hint = "http_error", "The address redirects to a plain HTTP address. Use an HTTPS address that serves the app directly."
		} else if errors.As(err, &netErr) && netErr.Timeout() {
			output.Outcome, output.Hint = "timeout", "The address did not answer in time. Check that the server or tunnel is running, then try again."
		} else {
			output.Outcome, output.Hint = "unreachable", "Nothing answered at this address. Check the host name, and that the deployment, dev server, or tunnel is running."
		}
		return output
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, setupCheckBodyLimit))
	if err != nil {
		output.Outcome, output.Hint = "unreachable", "The address stopped answering while the page loaded. Check that the server or tunnel is running, then try again."
		return output
	}
	output.StatusCode = response.StatusCode
	if response.Request != nil && response.Request.URL != nil {
		final := *response.Request.URL
		final.RawQuery, final.Fragment = "", ""
		output.FinalURL = final.String()
		output.SignInPage = setupSignInPathPattern.MatchString(final.Path)
	}
	output.Outcome, output.Hint = classifySetupURLResponse(response, string(body))
	output.Title = setupPageTitle(body)
	output.Reachable = output.Outcome == "reachable"
	if output.Reachable && output.SignInPage {
		output.Hint = "The address opens a sign-in page. Give Revyl a way past it before you test screens behind sign-in."
	}
	return output
}

const setupPrivateAddressHint = "Revyl's browser runs in the cloud and can't reach localhost or a private address. Expose the dev server through a public HTTPS tunnel, or use a deployed or preview URL."

func setupPageTitle(body []byte) string {
	for _, pattern := range setupPageTitlePatterns {
		if match := pattern.FindSubmatch(body); match != nil {
			if title := strings.Join(strings.Fields(html.UnescapeString(string(match[1]))), " "); title != "" {
				return truncate(title, 120)
			}
		}
	}
	return ""
}

func classifySetupURLResponse(response *http.Response, body string) (string, string) {
	lowerBody := strings.ToLower(body)
	switch {
	case strings.Contains(body, "Blocked request. This host") || strings.Contains(body, "Invalid Host header"):
		return "dev_server_host_blocked", "The dev server refuses the tunnel's host name: restart it for this run with the tunnel's host allowed, such as __VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=<tunnel host> before Vite's dev command, without editing the app's config files, and check again."
	case response.Header.Get("Cf-Mitigated") == "challenge" || strings.Contains(lowerBody, "challenges.cloudflare.com") || strings.Contains(lowerBody, "<title>just a moment") || strings.Contains(lowerBody, "<title>attention required"):
		return "bot_check", "The address shows a bot check, which blocks Revyl's browser. Use a preview or staging URL without bot protection, or allowlist Revyl's IP addresses: https://docs.revyl.com/security/allowlist-ip-addresses"
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return "access_denied", fmt.Sprintf("The address answered HTTP %d. A protected preview needs its protection bypass or an allowlist for Revyl's IP addresses: https://docs.revyl.com/security/allowlist-ip-addresses", response.StatusCode)
	case response.StatusCode >= 400:
		return "http_error", fmt.Sprintf("The address answered HTTP %d. Check that the deployment, dev server, or tunnel serves the app, then check again.", response.StatusCode)
	case response.StatusCode >= 300:
		return "http_error", "The address kept redirecting. Use the address the redirects end at."
	default:
		return "reachable", ""
	}
}

type setupReportOutput struct {
	Platform string `json:"platform"`
	Outcome  string `json:"outcome"`
	Reason   string `json:"reason,omitempty"`
}

func newSetupReportCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "report",
		Short: "Record how setting up one platform ended",
		Long: `Record how setting up one platform of this repository ended, once per platform
that revyl setup detect found. Revyl uses it to see where setup gets stuck; it
records only the platform, outcome, and reason below, never names, paths, or
addresses, and changes nothing in your organization.

--outcome is created (Revyl app set up in this run), existing (it was already
set up), skipped, or failed. skipped and failed need a --reason:
user_declined, no_reachable_url, url_blocked, needs_user, not_the_product,
build_failed, session_failed, billing_or_quota, not_supported, or other.`,
		Example: `  revyl setup report --platform ios --outcome created
  revyl setup report --platform web --outcome skipped --reason no_reachable_url`,
		Args: cobra.NoArgs,
		RunE: runSetupReport,
	}
	command.Flags().String("platform", "", "ios, android, or web")
	command.Flags().String("outcome", "", "created, existing, skipped, or failed")
	command.Flags().String("reason", "", "Why a platform was skipped or failed")
	command.Flags().Bool("json", false, "Print the recorded outcome as JSON")
	_ = command.MarkFlagRequired("platform")
	_ = command.MarkFlagRequired("outcome")
	return command
}

func runSetupReport(cmd *cobra.Command, _ []string) error {
	recordSetupRef(cmd)
	platform, _ := cmd.Flags().GetString("platform")
	outcome, _ := cmd.Flags().GetString("outcome")
	reason, _ := cmd.Flags().GetString("reason")
	output, err := parseSetupReport(platform, outcome, reason)
	if err != nil {
		return err
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "setup_platform", DomainStatus: setupReportStatus(output)})
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		return writeSetupJSON(cmd.OutOrStdout(), output)
	}
	if output.Reason != "" {
		ui.PrintSuccess("Recorded %s %s (%s)", setupPlatformNames[output.Platform], output.Outcome, strings.ReplaceAll(output.Reason, "_", " "))
	} else {
		ui.PrintSuccess("Recorded %s %s", setupPlatformNames[output.Platform], output.Outcome)
	}
	return nil
}

func setupReportStatus(output setupReportOutput) string {
	if output.Reason == "" {
		return output.Platform + "_" + output.Outcome
	}
	return output.Platform + "_" + output.Outcome + "_" + output.Reason
}

func parseSetupReport(platform, outcome, reason string) (setupReportOutput, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	reason = strings.ToLower(strings.TrimSpace(reason))
	if _, ok := setupPlatformNames[platform]; !ok {
		return setupReportOutput{}, usageErrorf("--platform must be ios, android, or web")
	}
	if !slices.Contains(setupOutcomes, outcome) {
		return setupReportOutput{}, usageErrorf("--outcome must be one of %s", strings.Join(setupOutcomes, ", "))
	}
	needsReason := outcome == "skipped" || outcome == "failed"
	switch {
	case needsReason && reason == "":
		return setupReportOutput{}, usageErrorf("--outcome %s needs --reason: %s", outcome, strings.Join(setupOutcomeReasons, ", "))
	case !needsReason && reason != "":
		return setupReportOutput{}, usageErrorf("--reason applies only to skipped or failed outcomes")
	case reason != "" && !slices.Contains(setupOutcomeReasons, reason):
		return setupReportOutput{}, usageErrorf("--reason must be one of %s", strings.Join(setupOutcomeReasons, ", "))
	}
	return setupReportOutput{Platform: platform, Outcome: outcome, Reason: reason}, nil
}

func writeSetupJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
