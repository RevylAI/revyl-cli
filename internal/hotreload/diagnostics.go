package hotreload

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DiagnosticCheck represents the result of a single diagnostic probe.
type DiagnosticCheck struct {
	// Name is a short human-readable label for this check (e.g. "Metro health").
	Name string

	// Passed indicates whether the check succeeded.
	Passed bool

	// Detail provides additional context on success or the error message on failure.
	Detail string
}

// DiagnosticResult aggregates the outcomes of all post-startup HMR health checks.
type DiagnosticResult struct {
	// Checks contains each individual probe result in execution order.
	Checks []DiagnosticCheck

	// AllPassed is true only when every check succeeded.
	AllPassed bool
}

// diagnosticHTTPTimeout is the fast timeout for lightweight HTTP and WebSocket
// probe connections.
var diagnosticHTTPTimeout = 5 * time.Second

// expoManifestHTTPTimeout gives Expo's first manifest request room to generate
// without making lightweight relay health probes wait on Metro work.
var expoManifestHTTPTimeout = 30 * time.Second

// expoManifestReadyTimeout bounds the total wait for Expo's first platform-aware
// manifest without weakening fast transport readiness checks.
var expoManifestReadyTimeout = 90 * time.Second

// expoBundlePrewarmHTTPTimeout gives large Expo apps enough room to compile the
// first JS bundle before the device asks for it through the relay.
var expoBundlePrewarmHTTPTimeout = 180 * time.Second

// metroTunnelReadyTimeout bounds how long startup waits for the tunnel to
// become externally reachable before failing fast.
const metroTunnelReadyTimeout = 20 * time.Second

// metroTunnelReadyPollInterval controls how frequently tunnel readiness is re-checked.
const metroTunnelReadyPollInterval = 1500 * time.Millisecond

// diagnosticCheckFunc probes a single post-startup hot reload invariant.
type diagnosticCheckFunc func(localPort int, tunnelURL string) DiagnosticCheck

// RunPostStartupDiagnostics probes the HMR pipeline after the dev loop reports
// ready and returns structured pass/fail results. Checks run synchronously in
// order so the first failure can short-circuit if needed.
//
// Checks performed:
//  1. Metro health endpoint (GET http://127.0.0.1:{port}/status)
//  2. Local HMR WebSocket upgrade (ws://127.0.0.1:{port}/hot)
//  3. Tunnel HTTP reachability (GET {tunnelURL}/status)
//  4. Tunnel WebSocket upgrade (wss://{tunnelURL}/hot)
//  5. Manifest URL correctness — Expo only (no local-port leaks in launchAsset.url, debuggerHost, hostUri)
//
// Parameters:
//   - localPort: The local Metro dev server port
//   - tunnelURL: The public relay URL (e.g. "https://hr-abc.revyl.ai")
//   - providerName: The hot reload provider (e.g. "expo", "react-native").
//     The manifest URL check is skipped for non-Expo providers because bare
//     Metro does not serve a JSON manifest at the root path.
//
// Returns:
//   - *DiagnosticResult: Aggregated results with per-check detail
func RunPostStartupDiagnostics(localPort int, tunnelURL string, providerName string) *DiagnosticResult {
	return RunPostStartupDiagnosticsForPlatform(localPort, tunnelURL, providerName, "")
}

// RunPostStartupDiagnosticsForPlatform is like RunPostStartupDiagnostics, but
// requests Expo manifests using the target device platform. Expo returns HTML
// for root requests that do not include a platform signal.
func RunPostStartupDiagnosticsForPlatform(localPort int, tunnelURL string, providerName string, targetPlatform string) *DiagnosticResult {
	checks := []diagnosticCheckFunc{
		checkMetroHealth,
		checkLocalWebSocket,
		checkTunnelHTTP,
		checkTunnelWebSocket,
	}
	if providerName == "expo" {
		checks = append(checks, func(localPort int, tunnelURL string) DiagnosticCheck {
			return checkManifestURLsForPlatform(localPort, tunnelURL, targetPlatform)
		})
	}
	return runDiagnosticChecks(localPort, tunnelURL, checks)
}

// WaitForMetroTunnel waits until the public Metro tunnel is reachable.
//
// This is primarily used by bare React Native startup. If the tunnel URL has
// not propagated yet, the cloud device can launch before the JavaScript bundle
// is reachable and remain on a blank white screen.
//
// Parameters:
//   - ctx: Context for cancellation while waiting.
//   - localPort: The local Metro dev server port.
//   - tunnelURL: The public relay URL.
//   - timeout: Maximum time to wait for the tunnel to become reachable.
//   - interval: Delay between retry attempts.
//
// Returns:
//   - *DiagnosticResult: The final probe result.
//   - error: Any timeout, cancellation, or readiness failure.
func WaitForMetroTunnel(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	interval time.Duration,
) (*DiagnosticResult, error) {
	checks := []diagnosticCheckFunc{
		checkTunnelHTTP,
		checkTunnelWebSocket,
	}

	return waitForDiagnosticChecks(ctx, localPort, tunnelURL, timeout, interval, checks, "Metro tunnel readiness")
}

// WaitForExpoMetroTransport waits until local Metro and the public relay status
// path are both reachable. It intentionally excludes Expo manifest generation,
// which can be much slower than lightweight readiness probes.
func WaitForExpoMetroTransport(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	interval time.Duration,
) (*DiagnosticResult, error) {
	checks := []diagnosticCheckFunc{
		checkMetroHealth,
		checkTunnelHTTP,
	}

	return waitForDiagnosticChecks(ctx, localPort, tunnelURL, timeout, interval, checks, "Expo relay transport readiness")
}

// WaitForExpoManifest waits until the public relay can serve a platform-aware
// Expo manifest whose launch/HMR URLs point at the relay host.
func WaitForExpoManifest(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	interval time.Duration,
	targetPlatform string,
) (*DiagnosticResult, error) {
	_, result, err := waitForExpoManifestFetch(ctx, localPort, tunnelURL, timeout, interval, targetPlatform)
	return result, err
}

// WaitForExpoBundlePrewarm fetches the platform-aware Expo manifest once, then
// proves the selected JS bundle starts flowing through the public relay.
func WaitForExpoBundlePrewarm(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	targetPlatform string,
) (*DiagnosticResult, error) {
	check := checkExpoBundlePrewarmForPlatformWithTimeout(ctx, localPort, tunnelURL, targetPlatform, timeout)
	return diagnosticResultForSingleCheck("Expo bundle prewarm failed", check)
}

// WaitForExpoBundlePrewarmFromManifest proves the selected JS bundle from an
// already-validated platform-aware Expo manifest starts flowing through the
// public relay. It waits for response headers and the first non-empty body byte,
// then lets the remaining body drain in the background.
func WaitForExpoBundlePrewarmFromManifest(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	fetched expoManifestFetchResult,
) (*DiagnosticResult, error) {
	check := checkExpoBundlePrewarmFromManifestWithTimeout(ctx, localPort, tunnelURL, fetched, timeout)
	return diagnosticResultForSingleCheck("Expo bundle prewarm failed", check)
}

func diagnosticResultForSingleCheck(errorPrefix string, check DiagnosticCheck) (*DiagnosticResult, error) {
	result := &DiagnosticResult{
		Checks:    []DiagnosticCheck{check},
		AllPassed: check.Passed,
	}
	if check.Passed {
		return result, nil
	}
	return result, fmt.Errorf("%s: %s", errorPrefix, formatFailedChecks(result.Checks))
}

// WaitForExpoMetroRelay waits until the public relay can serve the Expo Metro
// status endpoint and a manifest whose bundle URLs resolve through the relay.
//
// Expo dev clients fail with "There was a problem loading the project" if the
// device opens the dev-client deep link before the relay can reach Metro or
// before Expo has applied the relay URL rewriting. This check blocks that
// launch until the externally-visible path is usable.
func WaitForExpoMetroRelay(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	interval time.Duration,
) (*DiagnosticResult, error) {
	return WaitForExpoMetroRelayForPlatform(ctx, localPort, tunnelURL, timeout, interval, "")
}

// WaitForExpoMetroRelayForPlatform waits for the public Expo relay using a
// platform-specific manifest request. Expo dev clients include the platform in
// manifest requests; without it, current Expo CLI serves the root HTML page.
func WaitForExpoMetroRelayForPlatform(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	interval time.Duration,
	targetPlatform string,
) (*DiagnosticResult, error) {
	transport, err := WaitForExpoMetroTransport(ctx, localPort, tunnelURL, timeout, interval)
	if err != nil {
		return transport, err
	}
	return WaitForExpoManifest(ctx, localPort, tunnelURL, timeout, interval, targetPlatform)
}

func waitForDiagnosticChecks(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	interval time.Duration,
	checks []diagnosticCheckFunc,
	label string,
) (*DiagnosticResult, error) {
	deadline := time.Now().Add(timeout)
	var lastResult *DiagnosticResult

	for {
		lastResult = runDiagnosticChecks(localPort, tunnelURL, checks)
		if lastResult.AllPassed {
			return lastResult, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return lastResult, fmt.Errorf(
				"timed out after %s waiting for %s: %s",
				timeout,
				label,
				formatFailedChecks(lastResult.Checks),
			)
		}

		sleepFor := interval
		if sleepFor > remaining {
			sleepFor = remaining
		}

		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return lastResult, ctx.Err()
		case <-timer.C:
		}
	}
}

// runDiagnosticChecks executes a list of diagnostic probes and aggregates the results.
func runDiagnosticChecks(localPort int, tunnelURL string, checks []diagnosticCheckFunc) *DiagnosticResult {
	result := &DiagnosticResult{AllPassed: true}

	for _, check := range checks {
		c := check(localPort, tunnelURL)
		result.Checks = append(result.Checks, c)
		if !c.Passed {
			result.AllPassed = false
		}
	}

	return result
}

// formatFailedChecks summarizes the failed probe names and details.
func formatFailedChecks(checks []DiagnosticCheck) string {
	failed := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.Passed {
			continue
		}
		failed = append(failed, fmt.Sprintf("%s (%s)", check.Name, check.Detail))
	}
	if len(failed) == 0 {
		return "unknown failure"
	}
	return strings.Join(failed, "; ")
}

// checkMetroHealth verifies the local Metro server is responding.
// Uses 127.0.0.1 to match the relay path and avoid false failures on
// machines where "localhost" resolves to IPv6.
func checkMetroHealth(localPort int, _ string) DiagnosticCheck {
	client := &http.Client{Timeout: diagnosticHTTPTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/status", localPort))
	if err != nil {
		return DiagnosticCheck{Name: "Metro health", Passed: false, Detail: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DiagnosticCheck{Name: "Metro health", Passed: false, Detail: fmt.Sprintf("status %d", resp.StatusCode)}
	}
	return DiagnosticCheck{Name: "Metro health", Passed: true, Detail: "OK"}
}

// checkLocalWebSocket attempts a WebSocket upgrade to the local HMR endpoint.
// Uses 127.0.0.1 to match the relay path and avoid false failures on
// machines where "localhost" resolves to IPv6.
func checkLocalWebSocket(localPort int, _ string) DiagnosticCheck {
	addr := fmt.Sprintf("127.0.0.1:%d", localPort)
	err := probeWebSocketUpgrade(addr, false)
	if err != nil {
		return DiagnosticCheck{Name: "Local WebSocket (/hot)", Passed: false, Detail: err.Error()}
	}
	return DiagnosticCheck{Name: "Local WebSocket (/hot)", Passed: true, Detail: "OK"}
}

// checkTunnelHTTP verifies the tunnel forwards HTTP to Metro.
func checkTunnelHTTP(_ int, tunnelURL string) DiagnosticCheck {
	client := &http.Client{Timeout: diagnosticHTTPTimeout}
	statusURL := strings.TrimRight(tunnelURL, "/") + "/status"
	start := time.Now()
	resp, err := client.Get(statusURL)
	duration := time.Since(start).Round(time.Millisecond)
	if err != nil {
		return DiagnosticCheck{Name: "Tunnel HTTP", Passed: false, Detail: fmt.Sprintf("%s duration=%s", err, duration)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DiagnosticCheck{Name: "Tunnel HTTP", Passed: false, Detail: fmt.Sprintf("status %d duration=%s", resp.StatusCode, duration)}
	}
	return DiagnosticCheck{Name: "Tunnel HTTP", Passed: true, Detail: fmt.Sprintf("OK duration=%s", duration)}
}

// checkTunnelWebSocket attempts a WebSocket upgrade through the tunnel.
func checkTunnelWebSocket(_ int, tunnelURL string) DiagnosticCheck {
	host := strings.TrimPrefix(strings.TrimPrefix(tunnelURL, "https://"), "http://")
	host = strings.TrimRight(host, "/")
	err := probeWebSocketUpgrade(host, strings.HasPrefix(tunnelURL, "https://"))
	if err != nil {
		return DiagnosticCheck{Name: "Tunnel WebSocket", Passed: false, Detail: err.Error()}
	}
	return DiagnosticCheck{Name: "Tunnel WebSocket", Passed: true, Detail: "OK"}
}

// checkManifestURLs fetches the manifest through the tunnel and verifies that
// bundle/host URLs don't leak the local Metro port (which the cloud device
// cannot reach).
func checkManifestURLs(localPort int, tunnelURL string) DiagnosticCheck {
	return checkManifestURLsForPlatform(localPort, tunnelURL, "")
}

func checkManifestURLsForPlatform(localPort int, tunnelURL string, targetPlatform string) DiagnosticCheck {
	return checkManifestURLsForPlatformWithTimeout(localPort, tunnelURL, targetPlatform, expoManifestHTTPTimeout)
}

func checkManifestURLsForPlatformWithTimeout(
	localPort int,
	tunnelURL string,
	targetPlatform string,
	timeout time.Duration,
) DiagnosticCheck {
	_, check := checkExpoManifestContractForPlatformWithTimeout(
		context.Background(),
		localPort,
		tunnelURL,
		targetPlatform,
		timeout,
	)
	return check
}

func checkExpoManifestContractForPlatformWithTimeout(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	targetPlatform string,
	timeout time.Duration,
) (expoManifestFetchResult, DiagnosticCheck) {
	if timeout <= 0 {
		timeout = expoManifestHTTPTimeout
	}
	fetched, failure := fetchExpoManifestForPlatform(ctx, tunnelURL, targetPlatform, timeout)
	if failure != nil {
		failure.Name = "Manifest URLs"
		return expoManifestFetchResult{}, *failure
	}

	contract := validateExpoManifestContract(fetched.Manifest, tunnelURL, localPort)
	if !contract.Passed {
		return fetched, DiagnosticCheck{
			Name:   "Manifest URLs",
			Passed: false,
			Detail: fmt.Sprintf("%s platform=%s duration=%s %s", contract.Stage, fetched.Platform, fetched.Duration, contract.Detail),
		}
	}
	return fetched, DiagnosticCheck{Name: "Manifest URLs", Passed: true, Detail: fmt.Sprintf("OK platform=%s duration=%s variant=%s", fetched.Platform, fetched.Duration, contract.Variant)}
}

func waitForExpoManifestFetch(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	timeout time.Duration,
	interval time.Duration,
	targetPlatform string,
) (expoManifestFetchResult, *DiagnosticResult, error) {
	deadline := time.Now().Add(timeout)
	var lastFetched expoManifestFetchResult
	var lastResult *DiagnosticResult

	for {
		fetched, check := checkExpoManifestContractForPlatformWithTimeout(
			ctx,
			localPort,
			tunnelURL,
			targetPlatform,
			expoManifestHTTPTimeout,
		)
		lastFetched = fetched
		lastResult = &DiagnosticResult{
			Checks:    []DiagnosticCheck{check},
			AllPassed: check.Passed,
		}
		if check.Passed {
			return fetched, lastResult, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return lastFetched, lastResult, fmt.Errorf(
				"timed out after %s waiting for Expo manifest readiness: %s",
				timeout,
				formatFailedChecks(lastResult.Checks),
			)
		}

		sleepFor := interval
		if sleepFor > remaining {
			sleepFor = remaining
		}

		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return lastFetched, lastResult, ctx.Err()
		case <-timer.C:
		}
	}
}

type expoManifestFetchResult struct {
	Manifest map[string]any
	Platform string
	Duration time.Duration
}

func fetchExpoManifestForPlatform(
	ctx context.Context,
	tunnelURL string,
	targetPlatform string,
	timeout time.Duration,
) (expoManifestFetchResult, *DiagnosticCheck) {
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	platform := normalizeExpoPlatform(targetPlatform)
	manifestURL, err := expoManifestURL(tunnelURL, platform)
	if err != nil {
		return expoManifestFetchResult{}, &DiagnosticCheck{Name: "Manifest URLs", Passed: false, Detail: fmt.Sprintf("build request failed: %s", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return expoManifestFetchResult{}, &DiagnosticCheck{Name: "Manifest URLs", Passed: false, Detail: fmt.Sprintf("build request failed: %s", err)}
	}
	req.Header.Set("expo-platform", platform)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(start).Round(time.Millisecond)
	if err != nil {
		return expoManifestFetchResult{}, &DiagnosticCheck{Name: "Manifest URLs", Passed: false, Detail: formatManifestRequestError("fetch", timeout, platform, duration, err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return expoManifestFetchResult{}, &DiagnosticCheck{
			Name:   "Manifest URLs",
			Passed: false,
			Detail: fmt.Sprintf("expo_manifest_http_status platform=%s status=%d duration=%s", platform, resp.StatusCode, duration),
		}
	}

	body, err := io.ReadAll(resp.Body)
	duration = time.Since(start).Round(time.Millisecond)
	if err != nil {
		return expoManifestFetchResult{}, &DiagnosticCheck{Name: "Manifest URLs", Passed: false, Detail: formatManifestRequestError("read", timeout, platform, duration, err)}
	}

	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		return expoManifestFetchResult{}, &DiagnosticCheck{
			Name:   "Manifest URLs",
			Passed: false,
			Detail: fmt.Sprintf("expo_manifest_parse platform=%s duration=%s content_type=%q body_prefix=%q error=%s", platform, duration, resp.Header.Get("Content-Type"), bodyPrefix(body), err),
		}
	}

	return expoManifestFetchResult{Manifest: manifest, Platform: platform, Duration: duration}, nil
}

func formatManifestRequestError(stage string, timeout time.Duration, platform string, duration time.Duration, err error) string {
	if isTimeoutError(err) {
		switch stage {
		case "fetch":
			return fmt.Sprintf("expo_manifest_headers platform=%s timeout=%s duration=%s error=%s", platform, timeout, duration, err)
		case "read":
			return fmt.Sprintf("expo_manifest_body platform=%s timeout=%s duration=%s error=%s", platform, timeout, duration, err)
		}
	}
	return fmt.Sprintf("expo_manifest_%s platform=%s duration=%s error=%s", stage, platform, duration, err)
}

func checkExpoBundlePrewarmForPlatformWithTimeout(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	targetPlatform string,
	timeout time.Duration,
) DiagnosticCheck {
	if timeout <= 0 {
		timeout = expoBundlePrewarmHTTPTimeout
	}
	fetched, failure := fetchExpoManifestForPlatform(ctx, tunnelURL, targetPlatform, expoManifestHTTPTimeout)
	if failure != nil {
		return DiagnosticCheck{Name: "Bundle prewarm", Passed: false, Detail: fmt.Sprintf("bundle_manifest %s", failure.Detail)}
	}
	return checkExpoBundlePrewarmFromManifestWithTimeout(ctx, localPort, tunnelURL, fetched, timeout)
}

func checkExpoBundlePrewarmFromManifestWithTimeout(
	ctx context.Context,
	localPort int,
	tunnelURL string,
	fetched expoManifestFetchResult,
	timeout time.Duration,
) DiagnosticCheck {
	if timeout <= 0 {
		timeout = expoBundlePrewarmHTTPTimeout
	}
	bundleURL, ok := selectExpoBundleURLField(fetched.Manifest)
	if !ok {
		return DiagnosticCheck{
			Name:   "Bundle prewarm",
			Passed: false,
			Detail: fmt.Sprintf("bundle_url_contract platform=%s no known bundle URL fields were present", fetched.Platform),
		}
	}

	expectedHost := expectedRelayHost(tunnelURL)
	if reason := validateExpoBundleURLCandidate(bundleURL.Value, expectedHost, localPort, fetched.Platform); reason != "" {
		return DiagnosticCheck{
			Name:   "Bundle prewarm",
			Passed: false,
			Detail: fmt.Sprintf("bundle_url_contract platform=%s %s %s", fetched.Platform, bundleURL.Path, reason),
		}
	}

	return requestExpoBundleFirstByte(ctx, bundleURL.Value, fetched.Platform, expectedHost, localPort, timeout)
}

func requestExpoBundleFirstByte(
	ctx context.Context,
	rawBundleURL string,
	platform string,
	expectedHost string,
	localPort int,
	timeout time.Duration,
) DiagnosticCheck {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	currentURL := strings.TrimSpace(rawBundleURL)
	start := time.Now()
	var redirects int

	for {
		if reason := validateExpoBundleURLCandidate(currentURL, expectedHost, localPort, platform); reason != "" {
			cancel()
			return DiagnosticCheck{
				Name:   "Bundle prewarm",
				Passed: false,
				Detail: fmt.Sprintf("bundle_url_contract platform=%s path=%s %s", platform, bundleRequestPath(currentURL), reason),
			}
		}

		resp, ttfb, check, done := doExpoBundleRequest(reqCtx, client, currentURL, platform, timeout, start)
		if !done {
			cancel()
			return check
		}

		if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
			redirectURL, reason := resolveBundleRedirectURL(currentURL, resp.Header.Get("Location"), expectedHost, localPort, platform)
			_ = resp.Body.Close()
			if reason != "" {
				cancel()
				return DiagnosticCheck{
					Name:   "Bundle prewarm",
					Passed: false,
					Detail: fmt.Sprintf("bundle_redirect_url platform=%s status=%d ttfb=%s path=%s %s", platform, resp.StatusCode, ttfb, bundleRequestPath(currentURL), reason),
				}
			}
			redirects++
			if redirects > 2 {
				cancel()
				return DiagnosticCheck{
					Name:   "Bundle prewarm",
					Passed: false,
					Detail: fmt.Sprintf("bundle_redirect_url platform=%s status=%d ttfb=%s path=%s too many redirects", platform, resp.StatusCode, ttfb, bundleRequestPath(currentURL)),
				}
			}
			currentURL = redirectURL
			continue
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			_ = resp.Body.Close()
			cancel()
			return DiagnosticCheck{
				Name:   "Bundle prewarm",
				Passed: false,
				Detail: fmt.Sprintf("bundle_http_status platform=%s status=%d ttfb=%s path=%s", platform, resp.StatusCode, ttfb, bundleRequestPath(currentURL)),
			}
		}

		readErr := readFirstNonEmptyBodyByte(resp.Body)
		firstByteAt := time.Since(start).Round(time.Millisecond)
		if readErr != nil {
			_ = resp.Body.Close()
			cancel()
			return DiagnosticCheck{
				Name:   "Bundle prewarm",
				Passed: false,
				Detail: fmt.Sprintf("bundle_body_first_byte platform=%s timeout=%s ttfb=%s first_byte=%s path=%s error=%s", platform, timeout, ttfb, firstByteAt, bundleRequestPath(currentURL), readErr),
			}
		}

		go drainExpoBundleBody(resp.Body, cancel)
		return DiagnosticCheck{
			Name:   "Bundle prewarm",
			Passed: true,
			Detail: fmt.Sprintf("OK platform=%s status=%d ttfb=%s first_byte=%s path=%s drain=background bundle_background_drain=started", platform, resp.StatusCode, ttfb, firstByteAt, bundleRequestPath(currentURL)),
		}
	}
}

func doExpoBundleRequest(
	ctx context.Context,
	client *http.Client,
	rawBundleURL string,
	platform string,
	timeout time.Duration,
	start time.Time,
) (*http.Response, time.Duration, DiagnosticCheck, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawBundleURL, nil)
	if err != nil {
		return nil, 0, DiagnosticCheck{
			Name:   "Bundle prewarm",
			Passed: false,
			Detail: fmt.Sprintf("bundle_url_contract platform=%s path=%s error=%s", platform, bundleRequestPath(rawBundleURL), err),
		}, false
	}
	req.Header.Set("expo-platform", platform)
	req.Header.Set("Accept", "application/javascript, */*")

	resp, err := client.Do(req)
	ttfb := time.Since(start).Round(time.Millisecond)
	if err != nil {
		stage := "bundle_headers"
		if !isTimeoutError(err) {
			stage = "bundle_fetch"
		}
		return nil, ttfb, DiagnosticCheck{
			Name:   "Bundle prewarm",
			Passed: false,
			Detail: fmt.Sprintf("%s platform=%s timeout=%s ttfb=%s path=%s error=%s", stage, platform, timeout, ttfb, bundleRequestPath(rawBundleURL), err),
		}, false
	}
	return resp, ttfb, DiagnosticCheck{}, true
}

func readFirstNonEmptyBodyByte(body io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			return nil
		}
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("bundle response ended before first body byte")
		}
		if err != nil {
			return err
		}
	}
}

func drainExpoBundleBody(body io.ReadCloser, cancel context.CancelFunc) {
	defer cancel()
	defer body.Close()
	_, _ = io.Copy(io.Discard, body)
}

func resolveBundleRedirectURL(
	currentURL string,
	location string,
	expectedHost string,
	localPort int,
	platform string,
) (string, string) {
	trimmed := strings.TrimSpace(location)
	if trimmed == "" {
		return "", "missing Location header"
	}
	base, err := url.Parse(strings.TrimSpace(currentURL))
	if err != nil {
		return "", fmt.Sprintf("current bundle URL is invalid: %s", err)
	}
	next, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Sprintf("Location header is invalid: %s", err)
	}
	resolved := base.ResolveReference(next).String()
	if reason := validateExpoBundleURLCandidate(resolved, expectedHost, localPort, platform); reason != "" {
		return "", reason
	}
	return resolved, ""
}

func validateExpoBundleURLCandidate(value string, expectedHost string, localPort int, platform string) string {
	trimmed := strings.TrimSpace(value)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Sprintf("must be an absolute http(s) URL, got %q", value)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Sprintf("uses unsupported scheme %q", parsed.Scheme)
	}

	host := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
	port := parsed.Port()
	if host == "" {
		return fmt.Sprintf("has unsupported URL/host %q", value)
	}
	if localPort > 0 && port == fmt.Sprintf("%d", localPort) {
		return fmt.Sprintf("contains local Metro port %d", localPort)
	}
	expectedHost = strings.ToLower(strings.TrimSpace(expectedHost))
	if expectedHost != "" && host != expectedHost {
		return fmt.Sprintf("points at %q instead of relay host %q", host, expectedHost)
	}
	if expectedHost == "" && isLocalManifestHost(host) {
		return fmt.Sprintf("points at local host %q", value)
	}
	if queryPlatform := strings.ToLower(strings.TrimSpace(parsed.Query().Get("platform"))); queryPlatform != "" && queryPlatform != platform {
		return fmt.Sprintf("has platform=%q instead of target platform %q", queryPlatform, platform)
	}
	return ""
}

func selectExpoBundleURLField(manifest map[string]any) (expoManifestURLField, bool) {
	if launchAsset, ok := manifest["launchAsset"].(map[string]any); ok {
		if str, ok := launchAsset["url"].(string); ok && strings.TrimSpace(str) != "" {
			return expoManifestURLField{Path: "launchAsset.url", Value: strings.TrimSpace(str)}, true
		}
	}
	for _, field := range []struct {
		path  string
		value any
	}{
		{path: "bundleUrl", value: manifest["bundleUrl"]},
		{path: "bundleURL", value: manifest["bundleURL"]},
	} {
		if str, ok := field.value.(string); ok && strings.TrimSpace(str) != "" {
			return expoManifestURLField{Path: field.path, Value: strings.TrimSpace(str)}, true
		}
	}
	return expoManifestURLField{}, false
}

func bundleRequestPath(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Path == "" {
		return strings.TrimSpace(raw)
	}
	if parsed.RawQuery != "" {
		return parsed.Path + "?" + parsed.RawQuery
	}
	return parsed.Path
}

type expoManifestContractResult struct {
	Passed  bool
	Stage   string
	Detail  string
	Variant string
}

func validateExpoManifestContract(manifest map[string]any, tunnelURL string, localPort int) expoManifestContractResult {
	candidates := collectExpoManifestURLFields(manifest)
	if len(candidates) == 0 {
		return expoManifestContractResult{
			Passed: false,
			Stage:  "expo_manifest_contract",
			Detail: "no known launch or HMR URL fields were present",
		}
	}

	expectedHost := expectedRelayHost(tunnelURL)
	var failures []string
	for _, candidate := range candidates {
		if reason := validateExpoManifestURLCandidate(candidate.Value, expectedHost, localPort); reason != "" {
			failures = append(failures, fmt.Sprintf("%s %s", candidate.Path, reason))
		}
	}
	if len(failures) > 0 {
		return expoManifestContractResult{
			Passed: false,
			Stage:  "expo_manifest_url_rewrite",
			Detail: strings.Join(failures, "; "),
		}
	}

	variant := "current"
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate.Path, "bundle") {
			variant = "legacy"
			break
		}
	}
	return expoManifestContractResult{Passed: true, Variant: variant}
}

type expoManifestURLField struct {
	Path  string
	Value string
}

func collectExpoManifestURLFields(manifest map[string]any) []expoManifestURLField {
	var fields []expoManifestURLField
	addStringField := func(path string, value any) {
		if str, ok := value.(string); ok && strings.TrimSpace(str) != "" {
			fields = append(fields, expoManifestURLField{Path: path, Value: strings.TrimSpace(str)})
		}
	}

	if launchAsset, ok := manifest["launchAsset"].(map[string]any); ok {
		addStringField("launchAsset.url", launchAsset["url"])
	}
	addStringField("bundleUrl", manifest["bundleUrl"])
	addStringField("bundleURL", manifest["bundleURL"])
	addStringField("debuggerHost", manifest["debuggerHost"])
	addStringField("hostUri", manifest["hostUri"])

	if extra, ok := manifest["extra"].(map[string]any); ok {
		if expoGo, ok := extra["expoGo"].(map[string]any); ok {
			addStringField("extra.expoGo.debuggerHost", expoGo["debuggerHost"])
		}
		if expoClient, ok := extra["expoClient"].(map[string]any); ok {
			addStringField("extra.expoClient.hostUri", expoClient["hostUri"])
			addStringField("extra.expoClient.hostURL", expoClient["hostURL"])
		}
	}

	return fields
}

func validateExpoManifestURLCandidate(value string, expectedHost string, localPort int) string {
	host, port := manifestURLHostPort(value)
	if host == "" {
		return fmt.Sprintf("has unsupported URL/host %q", value)
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if localPort > 0 && port == fmt.Sprintf("%d", localPort) {
		return fmt.Sprintf("contains local Metro port %d", localPort)
	}
	if expectedHost != "" && host != expectedHost {
		return fmt.Sprintf("points at %q instead of relay host %q", host, expectedHost)
	}
	if expectedHost == "" && isLocalManifestHost(host) {
		return fmt.Sprintf("points at local host %q", value)
	}
	return ""
}

func manifestURLHostPort(raw string) (string, string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Host != "" {
		return parsed.Hostname(), parsed.Port()
	}
	if strings.Contains(value, "://") {
		return "", ""
	}
	hostPort := value
	if slash := strings.Index(hostPort, "/"); slash >= 0 {
		hostPort = hostPort[:slash]
	}
	if comma := strings.Index(hostPort, ","); comma >= 0 {
		hostPort = hostPort[:comma]
	}
	hostPort = strings.TrimSpace(hostPort)
	if hostPort == "" {
		return "", ""
	}
	host, port, err := net.SplitHostPort(hostPort)
	if err == nil {
		return host, port
	}
	if colon := strings.LastIndex(hostPort, ":"); colon > 0 && !strings.Contains(hostPort[colon+1:], ":") {
		portPart := hostPort[colon+1:]
		if isDigits(portPart) {
			return hostPort[:colon], portPart
		}
	}
	return hostPort, ""
}

func expectedRelayHost(tunnelURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(tunnelURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func isLocalManifestHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func bodyPrefix(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 120 {
		return text[:120]
	}
	return text
}

type timeoutError interface {
	Timeout() bool
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeoutErr timeoutError
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}

func normalizeExpoPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "android":
		return "android"
	case "ios":
		return "ios"
	default:
		return "ios"
	}
}

func expoManifestURL(tunnelURL string, platform string) (string, error) {
	raw := strings.TrimSpace(tunnelURL)
	if raw == "" {
		return "", fmt.Errorf("empty tunnel URL")
	}
	parsed, err := url.Parse(strings.TrimRight(raw, "/") + "/")
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	q.Set("platform", normalizeExpoPlatform(platform))
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// probeWebSocketUpgrade performs a raw TCP WebSocket upgrade handshake and
// returns nil if the server responds with 101 Switching Protocols.
//
// Parameters:
//   - hostPort: The host:port to connect to. If no port is present and useTLS
//     is true, ":443" is appended.
//   - useTLS: Whether to wrap the connection with TLS.
//
// Returns:
//   - error: nil on successful 101 response, otherwise describes the failure.
func probeWebSocketUpgrade(hostPort string, useTLS bool) error {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		if useTLS {
			port = "443"
			hostPort = host + ":443"
		} else {
			port = "80"
			hostPort = host + ":80"
		}
	}

	dialer := &net.Dialer{Timeout: diagnosticHTTPTimeout}
	var conn net.Conn
	if useTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", hostPort, &tls.Config{ServerName: host})
	} else {
		conn, err = dialer.Dial("tcp", hostPort)
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(diagnosticHTTPTimeout))

	// Per RFC 7230 §5.4, omit the port from the Host header when it is the
	// default for the scheme (443 for HTTPS, 80 for HTTP).
	hostHeader := host
	if (useTLS && port != "443") || (!useTLS && port != "80") {
		hostHeader = hostPort
	}

	req := fmt.Sprintf(
		"GET /hot HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n",
		hostHeader,
	)
	if _, err := conn.Write([]byte(req)); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	statusLine := string(buf[:n])
	if idx := strings.Index(statusLine, "\r\n"); idx > 0 {
		statusLine = statusLine[:idx]
	}

	if !strings.Contains(statusLine, "101") {
		return fmt.Errorf("unexpected response: %s", statusLine)
	}
	return nil
}
