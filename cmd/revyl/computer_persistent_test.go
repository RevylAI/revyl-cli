package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/testutil"
)

const persistentComputerID = "11111111-1111-4111-8111-111111111111"

func runPersistentComputerCommand(t *testing.T, handler http.HandlerFunc, args ...string) (string, string, error) {
	t.Helper()
	testutil.SetHomeDir(t, t.TempDir())
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newComputerCommand())
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"computer"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func persistentComputerStatusJSON(state string) string {
	return fmt.Sprintf(`{"computer_id":%q,"state":%q,"run_session_id":%q,"saved_at":null,"durable":false,"created_at":"2026-10-10T00:00:00Z"}`, persistentComputerID, state, persistentComputerID)
}

func TestComputerCreateSendsPersistentOnlyWhenAskedAndOnlyForMacs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"persistent", []string{"--os", "macos", "--persistent"}, "true"},
		{"ordinary", []string{"--os", "macos"}, ""},
		{"linux", []string{"--os", "linux", "--persistent"}, "refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []map[string]json.RawMessage
			_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				bodies = append(bodies, body)
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"detail":"computer_limit"}`)
			}, append([]string{"create", "--json"}, tc.args...)...)
			if err == nil {
				t.Fatal("refused create returned success")
			}
			if tc.want == "refused" {
				if len(bodies) != 0 || !strings.Contains(err.Error(), "--persistent requires --os macos") {
					t.Fatalf("linux --persistent sent %d requests: %v", len(bodies), err)
				}
				return
			}
			if len(bodies) != 1 || string(bodies[0]["persistent"]) != tc.want {
				t.Fatalf("create sent persistent %q, want %q", bodies[0]["persistent"], tc.want)
			}
		})
	}
}

func TestComputerStartWaitExitsByTheStateTheComputerReached(t *testing.T) {
	for _, tc := range []struct {
		state string
		code  int
	}{{"running", 0}, {"stopped", 1}} {
		t.Run(tc.state, func(t *testing.T) {
			stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/computer-runs/"+persistentComputerID+"/start" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, persistentComputerStatusJSON(tc.state))
			}, "start", persistentComputerID, "--wait", "--json")
			var exit *commandExitError
			if tc.code == 0 && err != nil || tc.code != 0 && (!errors.As(err, &exit) || exit.code != tc.code || !strings.Contains(err.Error(), "stopped again")) {
				t.Fatalf("start reaching %s returned %v, want exit %d", tc.state, err, tc.code)
			}
			if !json.Valid([]byte(stdout)) || !strings.Contains(stdout, `"state":"`+tc.state+`"`) {
				t.Fatalf("start printed %q", stdout)
			}
		})
	}
}

func persistentComputerReportJSON(persistent string) string {
	return fmt.Sprintf(`{"report_id":"22222222-2222-4222-8222-222222222222","computer_session_id":%q,
		"org_id":"44444444-4444-4444-8444-444444444444","os":"macos","created_at":"2026-10-10T00:00:00Z",
		"expires_at":"2026-10-10T01:00:00Z","as_of":"2026-10-10T00:00:04Z","execution_started_at":"2026-10-10T00:00:00Z",
		"state":"active","release":"pending","steps":[],"persistent":%s}`, persistentComputerID, persistent)
}

func TestComputerStopOfAPersistentComputerPrintsItsStatus(t *testing.T) {
	var requests []string
	stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		fmt.Fprint(w, persistentComputerReportJSON(persistentComputerStatusJSON("stopping")))
	}, "stop", persistentComputerID)
	want := fmt.Sprintf("computer_id=%s state=stopping run_session_id=%s\n", persistentComputerID, persistentComputerID)
	if err != nil || stdout != want || len(requests) != 1 || requests[0] != "POST /api/v1/computer-runs/"+persistentComputerID+"/stop" {
		t.Fatalf("stop printed %q after %v: %v", stdout, requests, err)
	}
}

func TestComputerStopWaitSaysTheDiskIsHeldWhenTheSaveIsUnconfirmed(t *testing.T) {
	_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, persistentComputerReportJSON(persistentComputerStatusJSON("stopping")))
	}, "stop", persistentComputerID, "--wait", "--wait-timeout", "1s")
	var exit *commandExitError
	if !errors.As(err, &exit) || exit.code != 124 || !strings.Contains(err.Error(), "disk stays on its Mac for recovery") {
		t.Fatalf("unconfirmed stop returned %v", err)
	}
}

func TestComputerDeleteNeedsConfirmationAndReportsTheDeletion(t *testing.T) {
	var requests []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
	if _, _, err := runPersistentComputerCommand(t, handler, "delete", persistentComputerID); err == nil || !strings.Contains(err.Error(), "--yes") || len(requests) != 0 {
		t.Fatalf("unconfirmed delete returned %v after %v", err, requests)
	}
	stdout, _, err := runPersistentComputerCommand(t, handler, "delete", persistentComputerID, "--yes", "--json")
	if err != nil || strings.TrimSpace(stdout) != fmt.Sprintf(`{"computer_id":%q,"state":"deleted"}`, persistentComputerID) {
		t.Fatalf("delete printed %q: %v", stdout, err)
	}
	if len(requests) != 1 || requests[0] != "DELETE /api/v1/computer-runs/"+persistentComputerID {
		t.Fatalf("delete sent %v", requests)
	}
}

func stoppedComputerJSON(id string, durable bool) string {
	return fmt.Sprintf(`{"computer_id":%q,"state":"stopped","run_session_id":"33333333-3333-4333-8333-333333333333","saved_at":"2026-10-11T08:00:00Z","durable":%t,"created_at":"2026-10-09T00:00:00Z"}`, id, durable)
}

func TestComputerListPersistentShowsWhereEachSaveIs(t *testing.T) {
	stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/computer-runs/persistent" || r.URL.RawQuery != "limit=25" {
			t.Errorf("unexpected request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		fmt.Fprintf(w, `{"computers":[%s,%s,%s],"total":3,"limit":25,"offset":0}`, persistentComputerStatusJSON("running"), stoppedComputerJSON("22222222-2222-4222-8222-222222222222", false), stoppedComputerJSON("44444444-4444-4444-8444-444444444444", true))
	}, "list", "--persistent")
	want := `ID                                    STATE                     SAVED                    CREATED
11111111-1111-4111-8111-111111111111  running                   -                        2026-10-10 00:00:00 UTC
22222222-2222-4222-8222-222222222222  stopped (saved on host)   2026-10-11 08:00:00 UTC  2026-10-09 00:00:00 UTC
44444444-4444-4444-8444-444444444444  stopped (saved off host)  2026-10-11 08:00:00 UTC  2026-10-09 00:00:00 UTC
`
	if err != nil || stdout != want {
		t.Fatalf("list --persistent printed\n%s\nwant\n%s(%v)", stdout, want, err)
	}
}

func TestComputerGetShowsAStartedComputersSavedDisk(t *testing.T) {
	stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"report_id":"22222222-2222-4222-8222-222222222222","computer_session_id":%q,
			"org_id":"44444444-4444-4444-8444-444444444444","os":"macos","created_at":"2026-10-10T00:00:00Z",
			"expires_at":"2026-10-10T01:00:00Z","as_of":"2026-10-10T00:00:04Z","execution_started_at":"2026-10-10T00:00:00Z",
			"state":"active","release":"pending","steps":[],
			"persistent":{"computer_id":%q,"state":"running","run_session_id":"33333333-3333-4333-8333-333333333333","saved_at":"2026-10-09T23:00:00Z","durable":true,"created_at":"2026-10-09T00:00:00Z"}}`,
			persistentComputerID, persistentComputerID)
	}, "get", persistentComputerID)
	if err != nil || !strings.Contains(stdout, "Persistent  running (saved 2026-10-09 23:00:00 UTC)\n") {
		t.Fatalf("get printed\n%s(%v)", stdout, err)
	}
}

func TestComputerGetSaysWhetherAStoppedComputersSaveIsOffItsHost(t *testing.T) {
	for _, tc := range []struct {
		durable bool
		want    string
	}{{false, "saved on host"}, {true, "saved off host"}} {
		t.Run(tc.want, func(t *testing.T) {
			stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"report_id":"22222222-2222-4222-8222-222222222222","computer_session_id":%q,
					"org_id":"44444444-4444-4444-8444-444444444444","os":"macos","created_at":"2026-10-10T00:00:00Z",
					"expires_at":"2026-10-10T01:00:00Z","as_of":"2026-10-10T00:00:04Z","execution_started_at":"2026-10-10T00:00:00Z",
					"state":"active","release":"pending","steps":[],"persistent":%s}`, persistentComputerID, stoppedComputerJSON(persistentComputerID, tc.durable))
			}, "get", persistentComputerID)
			if err != nil || !strings.Contains(stdout, "Persistent  stopped ("+tc.want+" 2026-10-11 08:00:00 UTC)\n") {
				t.Fatalf("get printed\n%s(%v)", stdout, err)
			}
		})
	}
}

func TestComputerHelpKeepsTheInternalPersistencePreviewOutOfSight(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"create", "--help"}, {"list", "--help"}, {"stop", "--help"}} {
		stdout, _, err := runPersistentComputerCommand(t, http.NotFound, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, hidden := range []string{"persistent", "saved disk", "delete"} {
			if strings.Contains(strings.ToLower(stdout), hidden) {
				t.Fatalf("%v help mentions %q:\n%s", args, hidden, stdout)
			}
		}
	}
}

func TestComputerStartSendsADisplayAndLifetimeOnlyWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"defaults", nil, ""},
		{"display_and_lifetime", []string{"--display", "1920x1080", "--timeout", "1h"}, `{"display":{"height":1080,"width":1920},"timeout_seconds":3600}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []string
			_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				bodies = append(bodies, string(body))
				fmt.Fprint(w, persistentComputerStatusJSON("starting"))
			}, append([]string{"start", persistentComputerID, "--json"}, tc.args...)...)
			if err != nil || len(bodies) != 1 || bodies[0] != tc.want {
				t.Fatalf("start sent %q: %v, want %q", bodies, err, tc.want)
			}
		})
	}
}

func TestComputerStartRefusesALifetimeCreateWouldRefuse(t *testing.T) {
	requests := 0
	_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
	}, "start", persistentComputerID, "--timeout", "30s")
	if err == nil || requests != 0 || !strings.Contains(err.Error(), "--timeout must be whole seconds from 1m to 24h") {
		t.Fatalf("start with a 30s lifetime sent %d requests: %v", requests, err)
	}
}
