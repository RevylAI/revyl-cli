package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeSetupFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectSetupAppsFindsMobileAndWebAppsInAMonorepo(t *testing.T) {
	root := t.TempDir()
	writeSetupFixture(t, root, map[string]string{
		"package.json":                                  `{"private":true,"workspaces":["apps/*"]}`,
		"apps/mobile/package.json":                      `{"dependencies":{"expo":"52","react-native":"0.76"}}`,
		"apps/mobile/app.json":                          `{"expo":{"name":"m"}}`,
		"apps/web/package.json":                         `{"scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`,
		"apps/web/node_modules/x/package.json":          `{"scripts":{"dev":"vite"},"dependencies":{"vite":"6"}}`,
		"docs/package.json":                             `{"scripts":{"start":"docusaurus start"},"dependencies":{"@docusaurus/core":"3"}}`,
		"examples/demo/package.json":                    `{"scripts":{"dev":"vite"},"dependencies":{"vite":"6"}}`,
		"apps/mobile/ios/App.xcodeproj/project.pbxproj": "",
		"apps/web/public/index.html":                    "<html></html>",
		"sites/landing/index.html":                      "<html></html>",
		"apps/web/admin/package.json":                   `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"6"}}`,
	})

	output := detectSetupApps(root)

	if !reflect.DeepEqual(output.Platforms, []string{"ios", "android", "web"}) {
		t.Fatalf("platforms = %v", output.Platforms)
	}
	var got []string
	for _, app := range output.Apps {
		got = append(got, app.Path+" "+strings.Join(app.Platforms, ",")+" "+app.Framework+" "+app.Role)
	}
	want := []string{"apps/mobile ios,android expo app", "apps/web web nextjs app", "apps/web/admin web vite app", "docs web docusaurus docs", "examples/demo web vite example", "sites/landing web static app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("apps = %q, want %q", got, want)
	}
	if setupPlatformSetStatus(output.Platforms) != "ios+android+web" {
		t.Fatalf("status = %q", setupPlatformSetStatus(output.Platforms))
	}
}

func TestDetectSetupAppsReportsOnlyWebForAWebRepository(t *testing.T) {
	root := t.TempDir()
	writeSetupFixture(t, root, map[string]string{
		"package.json":      `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"6"}}`,
		"public/index.html": "<html></html>",
	})

	output := detectSetupApps(root)

	if len(output.Apps) != 1 || output.Apps[0].Path != "." || !reflect.DeepEqual(output.Platforms, []string{"web"}) {
		t.Fatalf("output = %+v", output)
	}
}

func TestDetectSetupAppsWithNoAppReportsNone(t *testing.T) {
	output := detectSetupApps(t.TempDir())

	if len(output.Apps) != 0 || setupPlatformSetStatus(output.Platforms) != "none" {
		t.Fatalf("output = %+v", output)
	}
}

func TestCheckSetupURLClassifiesWhatRevylsBrowserWouldSee(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte("<html><title>App</title></html>"))
		case "/private":
			http.Redirect(w, r, "/login?next=/private", http.StatusFound)
		case "/login":
			_, _ = w.Write([]byte("<form>Sign in</form>"))
		case "/vite":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`Blocked request. This host ("abc.trycloudflare.com") is not allowed.`))
		case "/challenge":
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
		case "/protected":
			w.WriteHeader(http.StatusUnauthorized)
		case "/to-http":
			http.Redirect(w, r, "http://app.acme.dev/", http.StatusFound)
		case "/to-private":
			http.Redirect(w, r, "https://10.0.0.5/admin", http.StatusFound)
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	t.Cleanup(server.Close)
	lookup, dial := setupLookupHostAddresses, setupDialCheckedAddress
	t.Cleanup(func() { setupLookupHostAddresses, setupDialCheckedAddress = lookup, dial })
	setupLookupHostAddresses = func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
		if host == "lan.acme.dev" {
			return []netip.Addr{netip.MustParseAddr("192.168.1.20")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
	}
	setupDialCheckedAddress = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client := setupCheckHTTPClient(5 * time.Second)
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test server certificate.

	cases := []struct {
		raw        string
		outcome    string
		reachable  bool
		signInPage bool
	}{
		{"app.acme.dev", "reachable", true, false},
		{"app.acme.dev/private", "reachable", true, true},
		{"abc.trycloudflare.com/vite", "dev_server_host_blocked", false, false},
		{"app.acme.dev/challenge", "bot_check", false, false},
		{"app.acme.dev/protected", "access_denied", false, false},
		{"app.acme.dev/missing", "http_error", false, false},
		{"localhost:3000", "private_address", false, false},
		{"http://192.168.1.4", "private_address", false, false},
		{"lan.acme.dev", "private_address", false, false},
		{"app.acme.dev/to-private", "private_address", false, false},
		{"app.acme.dev/to-http", "http_error", false, false},
		{"not a url", "invalid_url", false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.raw, func(t *testing.T) {
			output := checkSetupURL(context.Background(), testCase.raw, client)

			if output.Outcome != testCase.outcome || output.Reachable != testCase.reachable || output.SignInPage != testCase.signInPage {
				t.Fatalf("checkSetupURL(%q) = %+v", testCase.raw, output)
			}
			if !output.Reachable && output.Hint == "" {
				t.Fatalf("checkSetupURL(%q) has no next step", testCase.raw)
			}
			if testCase.raw == "app.acme.dev" && output.Title != "App" {
				t.Fatalf("title = %q, want App", output.Title)
			}
			if strings.Contains(output.FinalURL, "?") {
				t.Fatalf("final URL keeps its query: %q", output.FinalURL)
			}
		})
	}
}

func TestParseSetupReportAcceptsOnlyBoundedOutcomes(t *testing.T) {
	valid := []struct{ platform, outcome, reason, status string }{
		{"ios", "created", "", "ios_created"},
		{"WEB", "skipped", "no_reachable_url", "web_skipped_no_reachable_url"},
		{"android", "failed", "build_failed", "android_failed_build_failed"},
	}
	for _, testCase := range valid {
		output, err := parseSetupReport(testCase.platform, testCase.outcome, testCase.reason)
		if err != nil {
			t.Fatalf("parseSetupReport(%q, %q, %q) error = %v", testCase.platform, testCase.outcome, testCase.reason, err)
		}
		if status := setupReportStatus(output); status != testCase.status || len(status) > 80 {
			t.Fatalf("status = %q, want %q", status, testCase.status)
		}
	}
	invalid := [][3]string{
		{"macos", "created", ""},
		{"web", "done", ""},
		{"web", "skipped", ""},
		{"web", "created", "user_declined"},
		{"web", "failed", "https://acme.dev is down"},
	}
	for _, testCase := range invalid {
		if _, err := parseSetupReport(testCase[0], testCase[1], testCase[2]); err == nil {
			t.Fatalf("parseSetupReport(%q) accepted an unbounded value", testCase)
		}
	}
}
