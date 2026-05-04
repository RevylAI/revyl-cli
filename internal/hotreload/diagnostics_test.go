package hotreload

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func withDiagnosticProbeTimeouts(t *testing.T, fastTimeout, manifestTimeout time.Duration) {
	t.Helper()
	previousFastTimeout := diagnosticHTTPTimeout
	previousManifestTimeout := expoManifestHTTPTimeout
	diagnosticHTTPTimeout = fastTimeout
	expoManifestHTTPTimeout = manifestTimeout
	t.Cleanup(func() {
		diagnosticHTTPTimeout = previousFastTimeout
		expoManifestHTTPTimeout = previousManifestTimeout
	})
}

func testExpoManifestForTunnel(tunnelURL string) map[string]interface{} {
	trimmed := strings.TrimRight(tunnelURL, "/")
	host := expectedRelayHost(trimmed)
	origin := "https://" + host
	if strings.HasPrefix(trimmed, "http://") {
		origin = "http://" + host
	}
	return map[string]interface{}{
		"launchAsset": map[string]string{"url": origin + "/index.bundle?platform=ios"},
		"extra": map[string]interface{}{
			"expoGo": map[string]string{
				"debuggerHost": host,
			},
			"expoClient": map[string]string{
				"hostUri": host,
				"hostURL": origin,
			},
		},
	}
}

func TestCheckMetroHealth_PassesOnOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	port := serverPort(t, srv)
	c := checkMetroHealth(port, "")
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckMetroHealth_FailsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	port := serverPort(t, srv)
	c := checkMetroHealth(port, "")
	if c.Passed {
		t.Fatal("expected fail on 500 status")
	}
}

func TestCheckMetroHealth_FailsOnConnectionRefused(t *testing.T) {
	c := checkMetroHealth(freePort(t), "")
	if c.Passed {
		t.Fatal("expected fail on connection refused")
	}
}

func TestCheckLocalWebSocket_PassesOn101(t *testing.T) {
	ln := startWebSocketServer(t)
	defer ln.Close()

	port := listenerPort(t, ln)
	c := checkLocalWebSocket(port, "")
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckLocalWebSocket_FailsOnRefused(t *testing.T) {
	c := checkLocalWebSocket(freePort(t), "")
	if c.Passed {
		t.Fatal("expected fail on connection refused")
	}
}

func TestCheckTunnelHTTP_PassesOnOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := checkTunnelHTTP(0, srv.URL)
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckTunnelHTTP_FailsOnBadURL(t *testing.T) {
	c := checkTunnelHTTP(0, "http://127.0.0.1:1")
	if c.Passed {
		t.Fatal("expected fail on unreachable URL")
	}
}

func TestCheckManifestURLs_PassesWhenClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	}))
	defer srv.Close()

	c := checkManifestURLs(8082, srv.URL)
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckManifestURLs_RequestsExpoPlatformHeaderAndQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("expo-platform") != "ios" || r.URL.Query().Get("platform") != "ios" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!DOCTYPE html><html><body>Expo dev tools</body></html>")
			return
		}
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	}))
	defer srv.Close()

	c := checkManifestURLsForPlatform(8082, srv.URL, "ios")
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
}

func TestCheckManifestURLs_HTMLDoesNotCountAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!DOCTYPE html><html><body>Expo dev tools</body></html>")
	}))
	defer srv.Close()

	c := checkManifestURLsForPlatform(8082, srv.URL, "ios")
	if c.Passed {
		t.Fatal("expected HTML manifest response to fail")
	}
	if !strings.Contains(c.Detail, "expo_manifest_parse") {
		t.Fatalf("detail = %q, expected parse-stage failure", c.Detail)
	}
}

func TestCheckManifestURLs_RequestsAndroidPlatform(t *testing.T) {
	var headerPlatform string
	var queryPlatform string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerPlatform = r.Header.Get("expo-platform")
		queryPlatform = r.URL.Query().Get("platform")
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	}))
	defer srv.Close()

	c := checkManifestURLsForPlatform(8081, srv.URL, "android")
	if !c.Passed {
		t.Fatalf("expected pass, got fail: %s", c.Detail)
	}
	if headerPlatform != "android" {
		t.Fatalf("expo-platform = %q, want android", headerPlatform)
	}
	if queryPlatform != "android" {
		t.Fatalf("platform query = %q, want android", queryPlatform)
	}
}

func TestCheckManifestURLs_FailsOnPortLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := expectedRelayHost("http://" + r.Host)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"launchAsset": map[string]string{"url": fmt.Sprintf("https://%s:8082/bundle.js", host)},
			"extra": map[string]interface{}{
				"expoGo":     map[string]string{"debuggerHost": fmt.Sprintf("%s:8082", host)},
				"expoClient": map[string]string{"hostUri": fmt.Sprintf("%s:8082", host)},
			},
		})
	}))
	defer srv.Close()

	c := checkManifestURLs(8082, srv.URL)
	if c.Passed {
		t.Fatal("expected fail on local port leak")
	}
	if !strings.Contains(c.Detail, "launchAsset") {
		t.Fatalf("detail = %q, expected mention of launchAsset", c.Detail)
	}
}

func TestCheckManifestURLs_FailsOnWrongRelayHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"launchAsset": map[string]string{"url": "https://other-relay.example.com/bundle.js"},
			"extra": map[string]interface{}{
				"expoGo":     map[string]string{"debuggerHost": "other-relay.example.com"},
				"expoClient": map[string]string{"hostUri": "other-relay.example.com"},
			},
		})
	}))
	defer srv.Close()

	c := checkManifestURLs(8082, srv.URL)
	if c.Passed {
		t.Fatal("expected fail on wrong relay host")
	}
	if !strings.Contains(c.Detail, "instead of relay host") {
		t.Fatalf("detail = %q, expected relay host mismatch", c.Detail)
	}
}

func TestValidateExpoManifestContractRejectsLocalHosts(t *testing.T) {
	manifest := map[string]any{
		"launchAsset": map[string]any{"url": "http://127.0.0.1:19000/bundle.js"},
	}

	result := validateExpoManifestContract(manifest, "", 8081)
	if result.Passed {
		t.Fatal("expected local host manifest URL to fail")
	}
	if !strings.Contains(result.Detail, "local host") {
		t.Fatalf("detail = %q, expected local host rejection", result.Detail)
	}
}

func TestValidateExpoManifestContractAcceptsLegacyBundleURL(t *testing.T) {
	manifest := map[string]any{
		"bundleUrl": "https://relay.revyl.ai/index.bundle?platform=ios",
	}

	result := validateExpoManifestContract(manifest, "https://relay.revyl.ai", 8081)
	if !result.Passed {
		t.Fatalf("expected legacy bundleUrl to pass, got %s", result.Detail)
	}
	if result.Variant != "legacy" {
		t.Fatalf("variant = %q, want legacy", result.Variant)
	}
}

func TestExpoSDKManifestFixturesValidateContract(t *testing.T) {
	tests := []struct {
		name           string
		file           string
		platform       string
		wantBundlePath string
	}{
		{
			name:           "sdk50 ios",
			file:           "sdk50-ios.json",
			platform:       "ios",
			wantBundlePath: "/App.bundle?platform=ios&dev=true&hot=false&transform.engine=hermes&transform.bytecode=true&transform.routerRoot=app",
		},
		{
			name:           "sdk50 android",
			file:           "sdk50-android.json",
			platform:       "android",
			wantBundlePath: "/App.bundle?platform=android&dev=true&hot=false&transform.engine=hermes&transform.bytecode=true&transform.routerRoot=app",
		},
		{
			name:           "sdk53 ios",
			file:           "sdk53-ios.json",
			platform:       "ios",
			wantBundlePath: "/index.bundle?platform=ios&dev=true&hot=false&transform.engine=hermes&transform.bytecode=1&transform.routerRoot=app&unstable_transformProfile=hermes-stable",
		},
		{
			name:           "sdk53 android",
			file:           "sdk53-android.json",
			platform:       "android",
			wantBundlePath: "/index.bundle?platform=android&dev=true&hot=false&transform.engine=hermes&transform.bytecode=1&transform.routerRoot=app&unstable_transformProfile=hermes-stable",
		},
		{
			name:           "sdk54 dev client ios",
			file:           "sdk54-dev-client-ios.json",
			platform:       "ios",
			wantBundlePath: "/node_modules/expo-router/entry.bundle?platform=ios&dev=true&hot=false&lazy=true&transform.engine=hermes&transform.bytecode=1&transform.routerRoot=app&unstable_transformProfile=hermes-stable",
		},
		{
			name:           "sdk54 dev client android",
			file:           "sdk54-dev-client-android.json",
			platform:       "android",
			wantBundlePath: "/node_modules/expo-router/entry.bundle?platform=android&dev=true&hot=false&lazy=true&transform.engine=hermes&transform.bytecode=1&transform.routerRoot=app&unstable_transformProfile=hermes-stable",
		},
		{
			name:           "sdk55 ios",
			file:           "sdk55-ios.json",
			platform:       "ios",
			wantBundlePath: "/index.bundle?platform=ios&dev=true&hot=false&lazy=true&transform.engine=hermes&transform.bytecode=1&transform.routerRoot=app&unstable_transformProfile=hermes-stable",
		},
		{
			name:           "sdk55 android",
			file:           "sdk55-android.json",
			platform:       "android",
			wantBundlePath: "/index.bundle?platform=android&dev=true&hot=false&lazy=true&transform.engine=hermes&transform.bytecode=1&transform.routerRoot=app&unstable_transformProfile=hermes-stable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := loadExpoManifestFixture(t, tt.file)
			tunnelURL := "https://relay.revyl.test"

			contract := validateExpoManifestContract(manifest, tunnelURL, 8081)
			if !contract.Passed {
				t.Fatalf("expected manifest contract to pass, got %s", contract.Detail)
			}
			if contract.Variant != "current" {
				t.Fatalf("variant = %q, want current", contract.Variant)
			}

			bundleURL, ok := selectExpoBundleURLField(manifest)
			if !ok {
				t.Fatal("expected fixture to expose a bundle URL")
			}
			if bundleURL.Path != "launchAsset.url" {
				t.Fatalf("bundle field = %q, want launchAsset.url", bundleURL.Path)
			}
			if got := bundleRequestPath(bundleURL.Value); got != tt.wantBundlePath {
				t.Fatalf("bundle path = %q, want %q", got, tt.wantBundlePath)
			}

			expectedHost := expectedRelayHost(tunnelURL)
			if reason := validateExpoBundleURLCandidate(bundleURL.Value, expectedHost, 8081, tt.platform); reason != "" {
				t.Fatalf("expected bundle URL to pass validation, got %s", reason)
			}

			wrongPlatform := "android"
			if tt.platform == "android" {
				wrongPlatform = "ios"
			}
			if reason := validateExpoBundleURLCandidate(bundleURL.Value, expectedHost, 8081, wrongPlatform); !strings.Contains(reason, "instead of target platform") {
				t.Fatalf("wrong-platform validation = %q, want platform mismatch", reason)
			}
		})
	}
}

func TestExpoSDKManifestFixturesPrewarmThroughRelayHost(t *testing.T) {
	tests := []struct {
		file     string
		platform string
	}{
		{file: "sdk50-ios.json", platform: "ios"},
		{file: "sdk50-android.json", platform: "android"},
		{file: "sdk53-ios.json", platform: "ios"},
		{file: "sdk53-android.json", platform: "android"},
		{file: "sdk54-dev-client-ios.json", platform: "ios"},
		{file: "sdk54-dev-client-android.json", platform: "android"},
		{file: "sdk55-ios.json", platform: "ios"},
		{file: "sdk55-android.json", platform: "android"},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			var manifestHeaderPlatform string
			var manifestQueryPlatform string
			var bundleHeaderPlatform string
			var bundleRequest string

			mux := http.NewServeMux()
			srv := httptest.NewServer(mux)
			defer srv.Close()

			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				manifestHeaderPlatform = r.Header.Get("expo-platform")
				manifestQueryPlatform = r.URL.Query().Get("platform")
				if manifestHeaderPlatform != tt.platform || manifestQueryPlatform != tt.platform {
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, "<!DOCTYPE html><html><body>Expo dev tools</body></html>")
					return
				}
				_ = json.NewEncoder(w).Encode(loadExpoManifestFixtureForTunnel(t, tt.file, srv.URL))
			})
			mux.HandleFunc("/App.bundle", func(w http.ResponseWriter, r *http.Request) {
				bundleHeaderPlatform = r.Header.Get("expo-platform")
				bundleRequest = r.URL.String()
				w.Header().Set("Content-Type", "application/javascript")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("console.log('sdk50');"))
			})
			mux.HandleFunc("/index.bundle", func(w http.ResponseWriter, r *http.Request) {
				bundleHeaderPlatform = r.Header.Get("expo-platform")
				bundleRequest = r.URL.String()
				w.Header().Set("Content-Type", "application/javascript")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("console.log('sdk');"))
			})
			mux.HandleFunc("/node_modules/expo-router/entry.bundle", func(w http.ResponseWriter, r *http.Request) {
				bundleHeaderPlatform = r.Header.Get("expo-platform")
				bundleRequest = r.URL.String()
				w.Header().Set("Content-Type", "application/javascript")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("console.log('sdk54');"))
			})

			c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, srv.URL, tt.platform, 500*time.Millisecond)
			if !c.Passed {
				t.Fatalf("expected bundle prewarm to pass, got %s", c.Detail)
			}
			if manifestHeaderPlatform != tt.platform {
				t.Fatalf("manifest expo-platform = %q, want %q", manifestHeaderPlatform, tt.platform)
			}
			if manifestQueryPlatform != tt.platform {
				t.Fatalf("manifest platform query = %q, want %q", manifestQueryPlatform, tt.platform)
			}
			if bundleHeaderPlatform != tt.platform {
				t.Fatalf("bundle expo-platform = %q, want %q", bundleHeaderPlatform, tt.platform)
			}
			if !strings.Contains(bundleRequest, "platform="+tt.platform) {
				t.Fatalf("bundle request = %q, expected platform query", bundleRequest)
			}
		})
	}
}

func TestSelectExpoBundleURLFieldPriorityOrder(t *testing.T) {
	manifest := map[string]any{
		"launchAsset": map[string]any{"url": "https://relay.revyl.test/current.bundle?platform=ios"},
		"bundleUrl":   "https://relay.revyl.test/legacy-lower.bundle?platform=ios",
		"bundleURL":   "https://relay.revyl.test/legacy-upper.bundle?platform=ios",
	}

	field, ok := selectExpoBundleURLField(manifest)
	if !ok {
		t.Fatal("expected launchAsset.url to be selected")
	}
	if field.Path != "launchAsset.url" {
		t.Fatalf("selected %q, want launchAsset.url", field.Path)
	}

	delete(manifest, "launchAsset")
	field, ok = selectExpoBundleURLField(manifest)
	if !ok {
		t.Fatal("expected bundleUrl to be selected")
	}
	if field.Path != "bundleUrl" {
		t.Fatalf("selected %q, want bundleUrl", field.Path)
	}

	delete(manifest, "bundleUrl")
	field, ok = selectExpoBundleURLField(manifest)
	if !ok {
		t.Fatal("expected bundleURL to be selected")
	}
	if field.Path != "bundleURL" {
		t.Fatalf("selected %q, want bundleURL", field.Path)
	}
}

func TestRunPostStartupDiagnostics_AllPass(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	})
	mux.HandleFunc("/hot", websocketUpgradeHandler)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	port := serverPort(t, srv)
	result := RunPostStartupDiagnostics(port, srv.URL, "expo")

	if !result.AllPassed {
		for _, c := range result.Checks {
			if !c.Passed {
				t.Errorf("check %q failed: %s", c.Name, c.Detail)
			}
		}
		t.Fatal("expected all checks to pass")
	}
	if len(result.Checks) != 5 {
		t.Fatalf("got %d checks, want 5", len(result.Checks))
	}
}

func TestRunPostStartupDiagnostics_BareRN_SkipsManifest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Metro debugger</body></html>")
	})
	mux.HandleFunc("/hot", websocketUpgradeHandler)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	port := serverPort(t, srv)
	result := RunPostStartupDiagnostics(port, srv.URL, "react-native")

	if !result.AllPassed {
		for _, c := range result.Checks {
			if !c.Passed {
				t.Errorf("check %q failed: %s", c.Name, c.Detail)
			}
		}
		t.Fatal("expected all checks to pass for bare RN (manifest check should be skipped)")
	}
	if len(result.Checks) != 4 {
		t.Fatalf("got %d checks, want 4 (manifest check should be excluded)", len(result.Checks))
	}
}

func TestWaitForMetroTunnel_PassesAfterRetry(t *testing.T) {
	var ready atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/hot", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		websocketUpgradeHandler(w, r)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	go func() {
		time.Sleep(100 * time.Millisecond)
		ready.Store(true)
	}()

	result, err := WaitForMetroTunnel(
		context.Background(),
		serverPort(t, srv),
		srv.URL,
		time.Second,
		25*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected tunnel to become ready, got error: %v", err)
	}
	if result == nil || !result.AllPassed {
		t.Fatalf("expected passing result, got %+v", result)
	}
}

func TestWaitForMetroTunnel_TimesOutWithFailedChecks(t *testing.T) {
	result, err := WaitForMetroTunnel(
		context.Background(),
		0,
		"http://127.0.0.1:1",
		150*time.Millisecond,
		25*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if result == nil {
		t.Fatal("expected final diagnostic result on failure")
	}
	if !strings.Contains(err.Error(), "Tunnel HTTP") {
		t.Fatalf("expected failed check in error, got %q", err.Error())
	}
}

func TestWaitForExpoMetroTransport_TimesOutWithoutManifestChecks(t *testing.T) {
	result, err := WaitForExpoMetroTransport(
		context.Background(),
		0,
		"http://127.0.0.1:1",
		120*time.Millisecond,
		25*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected transport timeout error")
	}
	if result == nil {
		t.Fatal("expected final diagnostic result on failure")
	}
	errText := err.Error()
	if !strings.Contains(errText, "Expo relay transport readiness") {
		t.Fatalf("expected transport readiness in error, got %q", errText)
	}
	if strings.Contains(errText, "Manifest URLs") {
		t.Fatalf("transport readiness should not include manifest checks: %q", errText)
	}
}

func TestWaitForExpoMetroRelay_PassesAfterStatusAndManifestReady(t *testing.T) {
	var ready atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.Header.Get("expo-platform") != "ios" || r.URL.Query().Get("platform") != "ios" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!DOCTYPE html><html><body>Expo dev tools</body></html>")
			return
		}
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	localPort := serverPort(t, srv)

	go func() {
		time.Sleep(100 * time.Millisecond)
		ready.Store(true)
	}()

	result, err := WaitForExpoMetroRelay(
		context.Background(),
		localPort,
		srv.URL,
		time.Second,
		25*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected Expo relay to become ready, got error: %v", err)
	}
	if result == nil || !result.AllPassed {
		t.Fatalf("expected passing result, got %+v", result)
	}
}

func TestWaitForExpoMetroRelay_AllowsManifestSlowerThanFastProbeTimeout(t *testing.T) {
	withDiagnosticProbeTimeouts(t, 50*time.Millisecond, 500*time.Millisecond)

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(125 * time.Millisecond)
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	localPort := serverPort(t, srv)

	result, err := WaitForExpoMetroRelay(
		context.Background(),
		localPort,
		srv.URL,
		800*time.Millisecond,
		25*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected Expo relay to tolerate slow manifest headers, got error: %v", err)
	}
	if result == nil || !result.AllPassed {
		t.Fatalf("expected passing result, got %+v", result)
	}
}

func TestWaitForExpoMetroRelay_UsesAndroidPlatform(t *testing.T) {
	var headerPlatform string
	var queryPlatform string

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		headerPlatform = r.Header.Get("expo-platform")
		queryPlatform = r.URL.Query().Get("platform")
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	localPort := serverPort(t, srv)

	result, err := WaitForExpoMetroRelayForPlatform(
		context.Background(),
		localPort,
		srv.URL,
		time.Second,
		25*time.Millisecond,
		"android",
	)
	if err != nil {
		t.Fatalf("expected Expo relay to become ready, got error: %v", err)
	}
	if result == nil || !result.AllPassed {
		t.Fatalf("expected passing result, got %+v", result)
	}
	if headerPlatform != "android" {
		t.Fatalf("expo-platform = %q, want android", headerPlatform)
	}
	if queryPlatform != "android" {
		t.Fatalf("platform query = %q, want android", queryPlatform)
	}
}

func TestWaitForExpoMetroRelay_TimesOutOnManifestPortLeak(t *testing.T) {
	var localPort int
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"launchAsset": map[string]string{"url": fmt.Sprintf("http://127.0.0.1:%d/bundle.js", localPort)},
			"extra": map[string]interface{}{
				"expoGo":     map[string]string{"debuggerHost": fmt.Sprintf("127.0.0.1:%d", localPort)},
				"expoClient": map[string]string{"hostUri": fmt.Sprintf("127.0.0.1:%d", localPort)},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	localPort = serverPort(t, srv)

	result, err := WaitForExpoMetroRelay(
		context.Background(),
		localPort,
		srv.URL,
		150*time.Millisecond,
		25*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if result == nil {
		t.Fatal("expected final diagnostic result on failure")
	}
	if !strings.Contains(err.Error(), "Manifest URLs") {
		t.Fatalf("expected manifest failure in error, got %q", err.Error())
	}
}

func TestWaitForExpoMetroRelay_ManifestHeaderTimeoutDetail(t *testing.T) {
	withDiagnosticProbeTimeouts(t, 25*time.Millisecond, 75*time.Millisecond)

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	localPort := serverPort(t, srv)

	result, err := WaitForExpoMetroRelay(
		context.Background(),
		localPort,
		srv.URL,
		180*time.Millisecond,
		10*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if result == nil {
		t.Fatal("expected final diagnostic result on failure")
	}
	errText := err.Error()
	if !strings.Contains(errText, "Manifest URLs") {
		t.Fatalf("expected manifest failure in error, got %q", errText)
	}
	if !strings.Contains(errText, "expo_manifest_headers") {
		t.Fatalf("expected response-header timeout detail, got %q", errText)
	}
	if !strings.Contains(errText, "75ms") {
		t.Fatalf("expected manifest timeout duration in detail, got %q", errText)
	}
	if strings.Contains(errText, "Tunnel HTTP") {
		t.Fatalf("expected only manifest readiness to fail, got %q", errText)
	}
}

func TestCheckManifestURLs_ManifestBodyTimeoutDetail(t *testing.T) {
	withDiagnosticProbeTimeouts(t, 25*time.Millisecond, 50*time.Millisecond)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(150 * time.Millisecond)
		json.NewEncoder(w).Encode(testExpoManifestForTunnel("http://" + r.Host))
	}))
	defer srv.Close()

	c := checkManifestURLsForPlatformWithTimeout(8081, srv.URL, "ios", 50*time.Millisecond)
	if c.Passed {
		t.Fatal("expected manifest body timeout")
	}
	if !strings.Contains(c.Detail, "expo_manifest_body") {
		t.Fatalf("detail = %q, expected body timeout detail", c.Detail)
	}
}

func TestCheckExpoBundlePrewarm_AllowsSlowBundleHeaders(t *testing.T) {
	var bundleHeaderPlatform string
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"launchAsset": map[string]string{"url": srv.URL + "/apps/mobile/index.ts.bundle?platform=ios"},
		})
	})
	mux.HandleFunc("/apps/mobile/index.ts.bundle", func(w http.ResponseWriter, r *http.Request) {
		bundleHeaderPlatform = r.Header.Get("expo-platform")
		time.Sleep(125 * time.Millisecond)
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("console.log('warm');"))
	})

	c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, srv.URL, "ios", 500*time.Millisecond)
	if !c.Passed {
		t.Fatalf("expected bundle prewarm to pass, got %s", c.Detail)
	}
	if bundleHeaderPlatform != "ios" {
		t.Fatalf("bundle expo-platform = %q, want ios", bundleHeaderPlatform)
	}
	if !strings.Contains(c.Detail, "/apps/mobile/index.ts.bundle?platform=ios") {
		t.Fatalf("detail = %q, expected bundle path", c.Detail)
	}
}

func TestCheckExpoBundlePrewarm_HeadersTimeoutDetail(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"launchAsset": map[string]string{"url": srv.URL + "/index.bundle?platform=ios"},
		})
	})
	mux.HandleFunc("/index.bundle", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, srv.URL, "ios", 50*time.Millisecond)
	if c.Passed {
		t.Fatal("expected bundle prewarm timeout")
	}
	if !strings.Contains(c.Detail, "bundle_headers") {
		t.Fatalf("detail = %q, expected bundle header timeout detail", c.Detail)
	}
}

func TestCheckExpoBundlePrewarm_RejectsUnsafeBundleURLs(t *testing.T) {
	tests := []struct {
		name      string
		bundleURL func(relayHost string) string
	}{
		{
			name: "wrong host",
			bundleURL: func(string) string {
				return "https://other-relay.example.com/index.bundle?platform=ios"
			},
		},
		{
			name: "localhost",
			bundleURL: func(string) string {
				return "http://localhost:8081/index.bundle?platform=ios"
			},
		},
		{
			name: "local ip",
			bundleURL: func(string) string {
				return "http://10.0.0.5/index.bundle?platform=ios"
			},
		},
		{
			name: "metro port leak on relay host",
			bundleURL: func(relayHost string) string {
				return "http://" + relayHost + ":8081/index.bundle?platform=ios"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{
					"launchAsset": map[string]string{"url": tt.bundleURL(expectedRelayHost("http://" + r.Host))},
				})
			}))
			defer srv.Close()

			c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, srv.URL, "ios", 500*time.Millisecond)
			if c.Passed {
				t.Fatal("expected bundle prewarm URL rewrite failure")
			}
			if !strings.Contains(c.Detail, "bundle_url_contract") {
				t.Fatalf("detail = %q, expected bundle URL rewrite failure", c.Detail)
			}
		})
	}
}

func TestCheckExpoBundlePrewarm_UsesLegacyBundleURL(t *testing.T) {
	for _, field := range []string{"bundleUrl", "bundleURL"} {
		t.Run(field, func(t *testing.T) {
			var requestedPath string
			mux := http.NewServeMux()
			srv := httptest.NewServer(mux)
			defer srv.Close()

			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{
					field: srv.URL + "/legacy.bundle?platform=ios",
				})
			})
			mux.HandleFunc("/legacy.bundle", func(w http.ResponseWriter, r *http.Request) {
				requestedPath = r.URL.String()
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("console.log('legacy');"))
			})

			c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, srv.URL, "ios", 500*time.Millisecond)
			if !c.Passed {
				t.Fatalf("expected legacy %s prewarm to pass, got %s", field, c.Detail)
			}
			if requestedPath != "/legacy.bundle?platform=ios" {
				t.Fatalf("requested bundle path = %q, want legacy bundle path", requestedPath)
			}
		})
	}
}

func TestCheckExpoBundlePrewarm_FailsWhenManifestHasNoBundleURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"extra": map[string]any{"expoGo": map[string]string{"debuggerHost": r.Host}},
		})
	}))
	defer srv.Close()

	c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, srv.URL, "ios", 500*time.Millisecond)
	if c.Passed {
		t.Fatal("expected missing bundle URL failure")
	}
	if !strings.Contains(c.Detail, "bundle_url_contract") {
		t.Fatalf("detail = %q, expected bundle contract detail", c.Detail)
	}
}

func TestCheckExpoBundlePrewarm_UsesAndroidPlatform(t *testing.T) {
	var manifestHeaderPlatform string
	var manifestQueryPlatform string
	var bundleHeaderPlatform string
	var bundleQueryPlatform string
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		manifestHeaderPlatform = r.Header.Get("expo-platform")
		manifestQueryPlatform = r.URL.Query().Get("platform")
		json.NewEncoder(w).Encode(map[string]any{
			"launchAsset": map[string]string{"url": srv.URL + "/index.bundle?platform=android"},
		})
	})
	mux.HandleFunc("/index.bundle", func(w http.ResponseWriter, r *http.Request) {
		bundleHeaderPlatform = r.Header.Get("expo-platform")
		bundleQueryPlatform = r.URL.Query().Get("platform")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("console.log('android');"))
	})

	c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, srv.URL, "android", 500*time.Millisecond)
	if !c.Passed {
		t.Fatalf("expected android bundle prewarm to pass, got %s", c.Detail)
	}
	if manifestHeaderPlatform != "android" {
		t.Fatalf("manifest expo-platform = %q, want android", manifestHeaderPlatform)
	}
	if manifestQueryPlatform != "android" {
		t.Fatalf("manifest platform query = %q, want android", manifestQueryPlatform)
	}
	if bundleHeaderPlatform != "android" {
		t.Fatalf("bundle expo-platform = %q, want android", bundleHeaderPlatform)
	}
	if bundleQueryPlatform != "android" {
		t.Fatalf("bundle platform query = %q, want android", bundleQueryPlatform)
	}
}

func TestCheckExpoBundlePrewarm_FirstBodyByteTimeoutDetail(t *testing.T) {
	server := newExpoDogfoodServer(t, expoDogfoodScenario{
		bundleFirstByteDelay: 150 * time.Millisecond,
	})

	c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, server.URL, "ios", 50*time.Millisecond)
	if c.Passed {
		t.Fatal("expected bundle first-byte timeout")
	}
	if !strings.Contains(c.Detail, "bundle_body_first_byte") {
		t.Fatalf("detail = %q, expected first-byte timeout detail", c.Detail)
	}
}

func TestCheckExpoBundlePrewarm_BackgroundDrainsAfterFirstByte(t *testing.T) {
	server := newExpoDogfoodServer(t, expoDogfoodScenario{
		bundleNeverEndsAfterFirstByte: true,
	})

	c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, server.URL, "ios", 150*time.Millisecond)
	if !c.Passed {
		t.Fatalf("expected bundle prewarm to pass after first byte, got %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "drain=background") {
		t.Fatalf("detail = %q, expected background drain marker", c.Detail)
	}
}

func TestCheckExpoBundlePrewarm_RejectsUnsafeRedirect(t *testing.T) {
	server := newExpoDogfoodServer(t, expoDogfoodScenario{
		redirectLocation: "http://localhost:8081/index.bundle?platform=ios",
	})

	c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, server.URL, "ios", 500*time.Millisecond)
	if c.Passed {
		t.Fatal("expected bundle redirect URL failure")
	}
	if !strings.Contains(c.Detail, "bundle_redirect_url") {
		t.Fatalf("detail = %q, expected redirect URL failure", c.Detail)
	}
}

func TestCheckExpoBundlePrewarm_RejectsPlatformMismatch(t *testing.T) {
	server := newExpoDogfoodServer(t, expoDogfoodScenario{
		bundlePlatform: "ios",
	})

	c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, server.URL, "android", 500*time.Millisecond)
	if c.Passed {
		t.Fatal("expected platform mismatch failure")
	}
	if !strings.Contains(c.Detail, `platform="ios"`) {
		t.Fatalf("detail = %q, expected platform mismatch detail", c.Detail)
	}
}

func TestExpoDogfoodHarnessScenarios(t *testing.T) {
	tests := []struct {
		name       string
		scenario   expoDogfoodScenario
		timeout    time.Duration
		platform   string
		wantPassed bool
		wantDetail string
	}{
		{
			name: "slow manifest and slow bundle header pass",
			scenario: expoDogfoodScenario{
				manifestDelay:     25 * time.Millisecond,
				bundleHeaderDelay: 35 * time.Millisecond,
			},
			timeout:    250 * time.Millisecond,
			platform:   "ios",
			wantPassed: true,
			wantDetail: "first_byte=",
		},
		{
			name: "slow first byte fails",
			scenario: expoDogfoodScenario{
				bundleFirstByteDelay: 120 * time.Millisecond,
			},
			timeout:    50 * time.Millisecond,
			platform:   "ios",
			wantPassed: false,
			wantDetail: "bundle_body_first_byte",
		},
		{
			name: "never ending body passes after first byte",
			scenario: expoDogfoodScenario{
				bundleNeverEndsAfterFirstByte: true,
			},
			timeout:    120 * time.Millisecond,
			platform:   "ios",
			wantPassed: true,
			wantDetail: "drain=background",
		},
		{
			name: "localhost bundle leak fails",
			scenario: expoDogfoodScenario{
				bundleURLOverride: "http://localhost:8081/index.bundle?platform=ios",
			},
			timeout:    250 * time.Millisecond,
			platform:   "ios",
			wantPassed: false,
			wantDetail: "bundle_url_contract",
		},
		{
			name: "wrong relay host fails",
			scenario: expoDogfoodScenario{
				bundleURLOverride: "https://other-relay.example.com/index.bundle?platform=ios",
			},
			timeout:    250 * time.Millisecond,
			platform:   "ios",
			wantPassed: false,
			wantDetail: "bundle_url_contract",
		},
		{
			name: "platform mismatch fails",
			scenario: expoDogfoodScenario{
				bundlePlatform: "ios",
			},
			timeout:    250 * time.Millisecond,
			platform:   "android",
			wantPassed: false,
			wantDetail: "platform=\"ios\"",
		},
		{
			name: "unsafe redirect fails",
			scenario: expoDogfoodScenario{
				redirectLocation: "http://10.0.0.5/index.bundle?platform=ios",
			},
			timeout:    250 * time.Millisecond,
			platform:   "ios",
			wantPassed: false,
			wantDetail: "bundle_redirect_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newExpoDogfoodServer(t, tt.scenario)
			c := checkExpoBundlePrewarmForPlatformWithTimeout(context.Background(), 8081, server.URL, tt.platform, tt.timeout)
			if c.Passed != tt.wantPassed {
				t.Fatalf("Passed = %v, want %v; detail=%s", c.Passed, tt.wantPassed, c.Detail)
			}
			if !strings.Contains(c.Detail, tt.wantDetail) {
				t.Fatalf("detail = %q, want substring %q", c.Detail, tt.wantDetail)
			}
		})
	}
}

func TestProbeWebSocketUpgrade_FailsOnHTTPEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	err := probeWebSocketUpgrade(addr, false)
	if err == nil {
		t.Fatal("expected error for non-websocket endpoint")
	}
}

// --- helpers ---

type expoDogfoodScenario struct {
	manifestDelay                 time.Duration
	bundleHeaderDelay             time.Duration
	bundleFirstByteDelay          time.Duration
	bundleNeverEndsAfterFirstByte bool
	bundleURLOverride             string
	bundlePlatform                string
	redirectLocation              string
}

func newExpoDogfoodServer(t *testing.T, scenario expoDogfoodScenario) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if scenario.manifestDelay > 0 {
			time.Sleep(scenario.manifestDelay)
		}
		platform := strings.ToLower(strings.TrimSpace(scenario.bundlePlatform))
		if platform == "" {
			platform = normalizeExpoPlatform(r.URL.Query().Get("platform"))
		}
		bundleURL := strings.TrimSpace(scenario.bundleURLOverride)
		if bundleURL == "" {
			bundleURL = srv.URL + "/index.bundle?platform=" + platform
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"launchAsset": map[string]string{"url": bundleURL},
		})
	})
	mux.HandleFunc("/index.bundle", func(w http.ResponseWriter, r *http.Request) {
		if scenario.bundleHeaderDelay > 0 {
			time.Sleep(scenario.bundleHeaderDelay)
		}
		if scenario.redirectLocation != "" {
			w.Header().Set("Location", scenario.redirectLocation)
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if scenario.bundleFirstByteDelay > 0 {
			time.Sleep(scenario.bundleFirstByteDelay)
		}
		_, _ = w.Write([]byte("c"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if scenario.bundleNeverEndsAfterFirstByte {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte("onsole.log('dogfood');"))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func loadExpoManifestFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	body, err := os.ReadFile("testdata/expo_manifests/" + name)
	if err != nil {
		t.Fatalf("read manifest fixture %s: %v", name, err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("parse manifest fixture %s: %v", name, err)
	}
	return manifest
}

func loadExpoManifestFixtureForTunnel(t *testing.T, name string, tunnelURL string) map[string]any {
	t.Helper()
	body, err := os.ReadFile("testdata/expo_manifests/" + name)
	if err != nil {
		t.Fatalf("read manifest fixture %s: %v", name, err)
	}
	text := string(body)
	tunnelURL = strings.TrimRight(tunnelURL, "/")
	parsedHost := expectedRelayHost(tunnelURL)
	text = strings.ReplaceAll(text, "https://relay.revyl.test", tunnelURL)
	text = strings.ReplaceAll(text, "relay.revyl.test", parsedHost)
	var manifest map[string]any
	if err := json.Unmarshal([]byte(text), &manifest); err != nil {
		t.Fatalf("parse manifest fixture %s: %v", name, err)
	}
	return manifest
}

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr := srv.Listener.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

func listenerPort(t *testing.T, ln net.Listener) int {
	t.Helper()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := listenerPort(t, ln)
	ln.Close()
	return port
}

// startWebSocketServer returns a TCP listener that performs a minimal
// WebSocket upgrade handshake for any connection.
func startWebSocketServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleWSUpgrade(conn)
		}
	}()
	return ln
}

func handleWSUpgrade(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	request := string(buf[:n])
	key := ""
	for _, line := range strings.Split(request, "\r\n") {
		if strings.HasPrefix(line, "Sec-WebSocket-Key:") {
			key = strings.TrimSpace(strings.TrimPrefix(line, "Sec-WebSocket-Key:"))
		}
	}

	accept := computeAcceptKey(key)
	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	conn.Write([]byte(resp))
}

func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-5AB5DC11E65B"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// websocketUpgradeHandler is an http.HandlerFunc that hijacks the connection
// and performs a WebSocket upgrade.
func websocketUpgradeHandler(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
		http.Error(w, "not a websocket request", http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	key := r.Header.Get("Sec-WebSocket-Key")
	accept := computeAcceptKey(key)
	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	buf.WriteString(resp)
	buf.Flush()
}
