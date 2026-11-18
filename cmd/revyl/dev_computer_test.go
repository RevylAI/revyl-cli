package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/testutil"
)

const devTestComputerID = "11111111-1111-4111-8111-111111111111"

func devComputerTestCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "dev"}
	cmd.Flags().String("computer", "", "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestResolveDevComputerTargetPlacesTheDevice(t *testing.T) {
	const otherComputerID = "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		name, environment string
		args              []string
		wantSessionID     string
		wantLocal         bool
	}{
		{"outside a computer", "", nil, "", false},
		{"inside a computer", devTestComputerID, nil, devTestComputerID, true},
		{"inside a computer, its ID named explicitly", devTestComputerID, []string{"--computer", strings.ToUpper(devTestComputerID)}, strings.ToUpper(devTestComputerID), true},
		{"inside a computer, opted out", devTestComputerID, []string{"--computer=none"}, "", false},
		{"inside a computer, another computer named", devTestComputerID, []string{"--computer", otherComputerID}, otherComputerID, false},
		{"outside a computer, a computer named", "", []string{"--computer", devTestComputerID}, devTestComputerID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REVYL_COMPUTER_ID", tc.environment)

			target := resolveDevComputerTarget(devComputerTestCommand(t, tc.args...))

			if target.SessionID != tc.wantSessionID || target.Local != tc.wantLocal {
				t.Fatalf("target = %+v, want session %q local %t", target, tc.wantSessionID, tc.wantLocal)
			}
		})
	}
}

func TestDevComputerStartOptionsHostTheSelectedBuildInsideTheComputer(t *testing.T) {
	standalone := mcppkg.StartSessionOptions{
		Platform:       "ios",
		AppID:          "app-1",
		BuildVersionID: "build-1",
		AppURL:         "https://artifacts.example/app.zip",
		AppPackage:     "com.example.app",
		AppLink:        "exp+example://expo-development-client/?url=https%3A%2F%2Frelay.example",
		IdleTimeout:    5 * time.Minute,
		SkipAppInstall: true,
	}

	hosted := devComputerTarget{SessionID: devTestComputerID, Local: true}.startOptions(standalone)

	want := mcppkg.StartSessionOptions{
		Platform:          "ios",
		AppID:             "app-1",
		BuildVersionID:    "build-1",
		IdleTimeout:       5 * time.Minute,
		ComputerSessionID: devTestComputerID,
	}
	if fmt.Sprintf("%+v", hosted) != fmt.Sprintf("%+v", want) {
		t.Fatalf("hosted start = %+v, want %+v", hosted, want)
	}
	if err := mcppkg.ValidateComputerSessionStart(hosted, "ios", devTestComputerID); err != nil {
		t.Fatalf("hosted start refused: %v", err)
	}
	if got := (devComputerTarget{}).startOptions(standalone); fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", standalone) {
		t.Fatalf("standalone start changed to %+v", got)
	}
}

func TestRequireDevComputerHostsRefusesComputersThatCannotHostThePlatform(t *testing.T) {
	for _, tc := range []struct {
		name, computerID, hosting, wantError string
		status                               int
	}{
		{name: "running Mac hosts iOS", computerID: devTestComputerID, status: http.StatusOK, hosting: `{"platforms":["web","ios"],"max_children":2}`},
		{name: "not running", computerID: devTestComputerID, status: http.StatusOK, hosting: `{"platforms":[],"max_children":0}`, wantError: "isn't running"},
		{name: "cannot host iOS", computerID: devTestComputerID, status: http.StatusOK, hosting: `{"platforms":["web","android"],"max_children":2}`, wantError: "can't host ios devices (it hosts web, android); pass --computer=none"},
		{name: "unknown computer", computerID: devTestComputerID, status: http.StatusNotFound, hosting: `{"detail":"not found"}`, wantError: "revyl computer get " + devTestComputerID},
		{name: "not a computer ID", computerID: "my-mac", wantError: "--computer must be a computer ID or none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/computer-runs/"+devTestComputerID+"/hosting" {
					t.Errorf("unexpected request %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.hosting)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			err := requireDevComputerHosts(context.Background(), api.NewClientWithDevMode("fixture-key", false), tc.computerID, "ios")

			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("refused a computer that hosts iOS: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantError)
			}
		})
	}
}

func TestDevViewerStaysClosedInsideAComputerUnlessOpenIsExplicit(t *testing.T) {
	previousOpen, previousNoOpen := devStartOpen, devStartNoOpen
	t.Cleanup(func() { devStartOpen, devStartNoOpen = previousOpen, previousNoOpen })
	devStartOpen, devStartNoOpen = true, false
	t.Setenv("REVYL_COMPUTER_ID", devTestComputerID)

	inside := devComputerTestCommand(t)
	inside.Flags().Bool("open", true, "")
	if effectiveDevOpenBrowser(inside, "") {
		t.Fatal("opened the viewer on the computer's desktop by default")
	}
	explicit := devComputerTestCommand(t)
	explicit.Flags().Bool("open", true, "")
	if err := explicit.Flags().Set("open", "true"); err != nil {
		t.Fatal(err)
	}
	if !effectiveDevOpenBrowser(explicit, "") {
		t.Fatal("ignored an explicit --open inside a computer")
	}
	t.Setenv("REVYL_COMPUTER_ID", "")
	outside := devComputerTestCommand(t)
	outside.Flags().Bool("open", true, "")
	if !effectiveDevOpenBrowser(outside, "") {
		t.Fatal("stopped opening the viewer for a standalone device")
	}
}
