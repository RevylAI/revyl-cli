package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newWebActionTestCommand(t *testing.T, source *cobra.Command, flags ...string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "revyl"}
	device := &cobra.Command{Use: "device"}
	cmd := &cobra.Command{Use: source.Use, Args: source.Args, RunE: source.RunE}
	root.AddCommand(device)
	device.AddCommand(cmd)
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("dev", false, "")
	source.Flags().VisitAll(func(flag *pflag.Flag) {
		switch flag.Value.Type() {
		case "string":
			cmd.Flags().StringP(flag.Name, flag.Shorthand, flag.DefValue, "")
		case "bool":
			cmd.Flags().BoolP(flag.Name, flag.Shorthand, flag.DefValue == "true", "")
		case "int":
			value := 0
			if _, err := fmt.Sscan(flag.DefValue, &value); err != nil {
				t.Fatal(err)
			}
			cmd.Flags().IntP(flag.Name, flag.Shorthand, value, "")
		default:
			t.Fatalf("unexpected action flag type %s", flag.Value.Type())
		}
	})
	if err := cmd.ParseFlags(flags); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestWebActionCommandRegistration(t *testing.T) {
	for _, name := range []string{"key", "scroll", "select"} {
		cmd, _, err := deviceCmd.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Fatalf("device %s is not registered: %v", name, err)
		}
		for _, flag := range []string{"s", "session-id", "json"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("device %s is missing --%s", name, flag)
			}
		}
		if err := cmd.ValidateArgs([]string{"one", "two"}); err == nil {
			t.Errorf("device %s accepts extra positional arguments", name)
		}
	}
}

func TestWebActionCommandsWireAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, platform, action, wantBody, wantError string
		command                                     *cobra.Command
		args, flags                                 []string
		grounded                                    bool
	}{
		{name: "escape", command: deviceKeyCmd, args: []string{"Escape"}, action: "key", wantBody: `{"key":"Escape"}`},
		{name: "shortcut", command: deviceKeyCmd, args: []string{"Meta+K"}, action: "key", wantBody: `{"key":"k","modifiers":["meta"]}`},
		{name: "flag key", command: deviceKeyCmd, flags: []string{"--key", "Tab"}, action: "key", wantBody: `{"key":"Tab"}`},
		{name: "viewport down", command: deviceScrollCmd, args: []string{"down"}, action: "scroll", wantBody: `{"dx":0,"dy":600}`},
		{name: "raw left zero", command: deviceScrollCmd, args: []string{"left"}, flags: []string{"--amount", "100", "--x", "0", "--y", "0"}, action: "scroll", wantBody: `{"x":0,"y":0,"dx":-100,"dy":0}`},
		{name: "target up", command: deviceScrollCmd, flags: []string{"--direction", "up", "--amount", "20", "--target", "Results"}, action: "scroll", grounded: true, wantBody: `{"x":12,"y":34,"dx":0,"dy":-20,"screen_token":"fixture-observation"}`},
		{name: "native select", command: deviceSelectCmd, args: []string{"Canada"}, flags: []string{"--target", "Country"}, action: "select", grounded: true, wantBody: `{"x":12,"y":34,"option_label":"Canada","screen_token":"fixture-observation"}`},
		{name: "raw select", command: deviceSelectCmd, args: []string{"Newest"}, flags: []string{"--x", "0", "--y", "20"}, action: "select", wantBody: `{"x":0,"y":20,"option_label":"Newest"}`},
		{name: "bad chord", command: deviceKeyCmd, args: []string{"Meta+synthetic-secret"}, wantError: "key"},
		{name: "plain letter", command: deviceKeyCmd, args: []string{"k"}, wantError: "key"},
		{name: "duplicate modifier", command: deviceKeyCmd, args: []string{"Ctrl+Ctrl+A"}, wantError: "modifier"},
		{name: "scroll too much", command: deviceScrollCmd, args: []string{"down"}, flags: []string{"--amount", "16385"}, wantError: "amount"},
		{name: "scroll zero", command: deviceScrollCmd, args: []string{"down"}, flags: []string{"--amount", "0"}, wantError: "amount"},
		{name: "scroll wrong direction", command: deviceScrollCmd, args: []string{"diagonal"}, wantError: "direction"},
		{name: "scroll missing y", command: deviceScrollCmd, args: []string{"down"}, flags: []string{"--x", "20"}, wantError: "x"},
		{name: "scroll negative coordinate", command: deviceScrollCmd, args: []string{"down"}, flags: []string{"--x", "-1", "--y", "20"}, wantError: "coordinate"},
		{name: "scroll conflicting targets", command: deviceScrollCmd, args: []string{"down"}, flags: []string{"--x", "20", "--y", "30", "--target", "Results"}, wantError: "target"},
		{name: "scroll conflicting direction", command: deviceScrollCmd, args: []string{"down"}, flags: []string{"--direction", "up"}, wantError: "direction"},
		{name: "select missing target", command: deviceSelectCmd, args: []string{"Canada"}, wantError: "target"},
		{name: "select empty label", command: deviceSelectCmd, args: []string{""}, flags: []string{"--x", "20", "--y", "30"}, wantError: "label"},
		{name: "select long label", command: deviceSelectCmd, args: []string{strings.Repeat("a", 257)}, flags: []string{"--x", "20", "--y", "30"}, wantError: "label"},
		{name: "ios key refusal", platform: "ios", command: deviceKeyCmd, args: []string{"Escape"}, wantError: "not supported on this platform"},
		{name: "android key refusal", platform: "android", command: deviceKeyCmd, args: []string{"Meta+K"}, wantError: "not supported on this platform"},
		{name: "ios scroll refusal", platform: "ios", command: deviceScrollCmd, args: []string{"down"}, wantError: "not supported on this platform"},
		{name: "android select refusal", platform: "android", command: deviceSelectCmd, args: []string{"Canada"}, flags: []string{"--target", "Country"}, wantError: "not supported on this platform"},
		{name: "ios legacy return", platform: "ios", command: deviceKeyCmd, args: []string{"return"}, action: "key", wantBody: `{"key":"ENTER"}`},
		{name: "android legacy delete", platform: "android", command: deviceKeyCmd, args: []string{"delete"}, action: "key", wantBody: `{"key":"BACKSPACE"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "fixture-key")
			t.Setenv(sessionIDEnvVar, "")
			platform := tc.platform
			if platform == "" {
				platform = "web"
			}
			var bodies []string
			resolveCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/execution/device-sessions/" + flowSessionID:
					fmt.Fprintf(w, `{"id":%q,"org_id":"fixture-org","platform":%q,"viewer_kind":"whep","status":"running","workflow_run_id":%q}`, flowSessionID, platform, flowWorkflowRunID)
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/resolve_target":
					resolveCalls++
					io.WriteString(w, `{"found":true,"x":12,"y":34,"screen_token":"fixture-observation"}`)
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/" + tc.action:
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					bodies = append(bodies, string(body))
					fmt.Fprintf(w, `{"success":true,"action":%q,"latency_ms":12}`, tc.action)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			cmd := newWebActionTestCommand(t, tc.command, append([]string{"--session-id", flowSessionID, "--json"}, tc.flags...)...)
			stdout, _, err, terminal := runStopWithAnalytics(t, cmd, cmd.RunE, tc.args)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v; want %q", err, tc.wantError)
				}
				if len(bodies) != 0 || resolveCalls != 0 || stdout != "" {
					t.Fatalf("invalid request dispatched: actions=%d resolutions=%d output=%q", len(bodies), resolveCalls, stdout)
				}
				if strings.Contains(err.Error(), "synthetic-secret") {
					t.Fatal("invalid key leaked in error")
				}
				return
			}
			if err != nil || len(bodies) != 1 {
				t.Fatalf("error = %v; actions = %d, want one", err, len(bodies))
			}
			if platform != "web" {
				if bodies[0] != tc.wantBody {
					t.Fatalf("phone wire changed: %q, want %q", bodies[0], tc.wantBody)
				}
			} else {
				var got, want map[string]any
				if json.Unmarshal([]byte(bodies[0]), &got) != nil || json.Unmarshal([]byte(tc.wantBody), &want) != nil {
					t.Fatal("invalid request JSON")
				}
				actual, _ := json.Marshal(got)
				expected, _ := json.Marshal(want)
				if string(actual) != string(expected) {
					t.Fatalf("body = %s; want %s", actual, expected)
				}
			}
			if (resolveCalls == 1) != tc.grounded {
				t.Fatalf("resolve calls = %d; grounded = %t", resolveCalls, tc.grounded)
			}
			var result map[string]any
			if json.Unmarshal([]byte(stdout), &result) != nil || result["success"] != true || result["action"] != tc.action {
				t.Fatalf("invalid success output: %s", stdout)
			}
			if _, present := terminal["command"]; !present {
				t.Fatal("missing centralized command analytics")
			}
			telemetry, err := json.Marshal(terminal)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"Canada", "Newest", "Country", "Results", "Meta+K"} {
				if strings.Contains(stdout, private) || strings.Contains(string(telemetry), private) {
					t.Fatal("action output or telemetry echoed input values")
				}
			}
		})
	}
}
