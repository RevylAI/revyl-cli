package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const computerSnapshotID = "55555555-5555-4555-8555-555555555555"

func computerSnapshotJSON(name string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"computer_id":%q,"saved_at":"2026-10-11T08:00:00Z","expires_at":null}`, computerSnapshotID, name, persistentComputerID)
}

func TestComputerSnapshotSaveNamesTheStoppedComputersSave(t *testing.T) {
	var requests []string
	var body map[string]string
	stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, computerSnapshotJSON("golden-1"))
	}, "snapshot", "save", persistentComputerID, "--name", "golden-1")
	want := fmt.Sprintf("snapshot_id=%s name=golden-1 computer_id=%s saved_at=2026-10-11T08:00:00Z\n", computerSnapshotID, persistentComputerID)
	if err != nil || stdout != want {
		t.Fatalf("snapshot save printed %q: %v", stdout, err)
	}
	if len(requests) != 1 || requests[0] != "POST /api/v1/computer-runs/"+persistentComputerID+"/snapshots" || body["name"] != "golden-1" {
		t.Fatalf("snapshot save sent %v with %v", requests, body)
	}
}

func TestComputerSnapshotSaveRefusesANameThatCouldNotNameOneSnapshot(t *testing.T) {
	for _, name := range []string{"", "-leading-dash", "has space", computerSnapshotID, strings.Repeat("a", 65)} {
		requests := 0
		_, _, err := runPersistentComputerCommand(t, func(http.ResponseWriter, *http.Request) { requests++ }, "snapshot", "save", persistentComputerID, "--name", name)
		if err == nil || requests != 0 {
			t.Fatalf("name %q sent %d requests: %v", name, requests, err)
		}
	}
}

func TestComputerSnapshotSaveExplainsARefusalInPlainWords(t *testing.T) {
	_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"detail":"snapshot_name_taken"}`)
	}, "snapshot", "save", persistentComputerID, "--name", "golden-1")
	if err == nil || !strings.Contains(err.Error(), "already has a snapshot with this name") {
		t.Fatalf("taken name returned %v", err)
	}
}

func TestComputerCreateFromSnapshotImpliesAMacAndSendsTheSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"implied os", []string{"--from-snapshot", "golden-1"}, `"golden-1"`},
		{"explicit os", []string{"--os", "macos", "--from-snapshot", computerSnapshotID}, fmt.Sprintf("%q", computerSnapshotID)},
		{"linux", []string{"--os", "linux", "--from-snapshot", "golden-1"}, "refused"},
		{"bad reference", []string{"--from-snapshot", "has space"}, "refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []map[string]json.RawMessage
			_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				bodies = append(bodies, body)
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"detail":"snapshot_expired"}`)
			}, append([]string{"create"}, tc.args...)...)
			if tc.want == "refused" {
				if err == nil || len(bodies) != 0 || !strings.Contains(err.Error(), "--from-snapshot requires") {
					t.Fatalf("create sent %d requests: %v", len(bodies), err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "Save a new snapshot") {
				t.Fatalf("expired snapshot returned %v", err)
			}
			if len(bodies) != 1 || string(bodies[0]["from_snapshot"]) != tc.want || string(bodies[0]["os"]) != `"macos"` {
				t.Fatalf("create sent %v", bodies)
			}
		})
	}
}

func TestComputerSnapshotListShowsEachSnapshotsSave(t *testing.T) {
	stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/computer-runs/snapshots" || r.URL.RawQuery != "limit=25" {
			t.Errorf("unexpected request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		fmt.Fprintf(w, `{"snapshots":[%s],"total":2,"limit":25,"offset":0}`, computerSnapshotJSON("golden-1"))
	}, "snapshot", "list")
	want := `ID                                    NAME      COMPUTER                              SAVED                    EXPIRES
55555555-5555-4555-8555-555555555555  golden-1  11111111-1111-4111-8111-111111111111  2026-10-11 08:00:00 UTC  -
Showing the newest 1 of 2 snapshots; use --limit (up to 100) for more.
`
	if err != nil || stdout != want {
		t.Fatalf("snapshot list printed\n%s\nwant\n%s(%v)", stdout, want, err)
	}
}

func TestComputerSnapshotGetRejectsAResponseForAnotherSnapshot(t *testing.T) {
	_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, computerSnapshotJSON("other"))
	}, "snapshot", "get", "golden-1")
	if err == nil {
		t.Fatal("get accepted another snapshot")
	}
}

func TestComputerSnapshotDeleteNeedsConfirmationAndDeletesByName(t *testing.T) {
	var requests []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
	if _, _, err := runPersistentComputerCommand(t, handler, "snapshot", "delete", "golden-1"); err == nil || !strings.Contains(err.Error(), "--yes") || len(requests) != 0 {
		t.Fatalf("unconfirmed delete returned %v after %v", err, requests)
	}
	stdout, _, err := runPersistentComputerCommand(t, handler, "snapshot", "delete", "golden-1", "--yes", "--json")
	if err != nil || strings.TrimSpace(stdout) != `{"snapshot":"golden-1","state":"deleted"}` {
		t.Fatalf("delete printed %q: %v", stdout, err)
	}
	if len(requests) != 1 || requests[0] != "DELETE /api/v1/computer-runs/snapshots/golden-1" {
		t.Fatalf("delete sent %v", requests)
	}
}

func TestComputerHelpKeepsSnapshotsOutOfSight(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"create", "--help"}} {
		stdout, _, err := runPersistentComputerCommand(t, http.NotFound, args...)
		if err != nil || strings.Contains(strings.ToLower(stdout), "snapshot") {
			t.Fatalf("%v help mentions snapshots (%v):\n%s", args, err, stdout)
		}
	}
}
