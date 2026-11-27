package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const computerSnapshotID = "55555555-5555-4555-8555-555555555555"

func computerSnapshotJSON(name string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"computer_id":%q,"saved_at":"2026-10-11T08:00:00Z","expires_at":null}`, computerSnapshotID, name, persistentComputerID)
}

func computerSnapshotJSONWithDescription(name, description string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"description":%s,"computer_id":%q,"saved_at":"2026-10-11T08:00:00Z","expires_at":null}`, computerSnapshotID, name, description, persistentComputerID)
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

func TestComputerSnapshotSaveSendsATrimmedDescriptionOnlyWhenGiven(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want json.RawMessage
	}{
		{"long flag", []string{"--description", "  Xcode 26 and simulators\n"}, json.RawMessage(`"Xcode 26 and simulators"`)},
		{"short flag", []string{"-d", "Xcode 26 and simulators"}, json.RawMessage(`"Xcode 26 and simulators"`)},
		{"blank", []string{"--description", "   "}, nil},
		{"absent", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]json.RawMessage
			_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&body)
				fmt.Fprint(w, computerSnapshotJSON("golden-1"))
			}, append([]string{"snapshot", "save", persistentComputerID, "--name", "golden-1"}, tc.args...)...)
			got, sent := body["description"]
			if err != nil || sent != (tc.want != nil) || string(got) != string(tc.want) {
				t.Fatalf("snapshot save sent description %s (sent=%v): %v", got, sent, err)
			}
		})
	}
}

func TestComputerSnapshotSaveRefusesAnOverlongDescriptionBeforeSending(t *testing.T) {
	requests := 0
	_, _, err := runPersistentComputerCommand(t, func(http.ResponseWriter, *http.Request) { requests++ }, "snapshot", "save", persistentComputerID, "--name", "golden-1", "--description", strings.Repeat("é", 501))
	if err == nil || requests != 0 || !strings.Contains(err.Error(), "at most 500 characters") {
		t.Fatalf("overlong description sent %d requests: %v", requests, err)
	}
}

func TestComputerSnapshotSavePrintsTheDescriptionItSaved(t *testing.T) {
	stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, computerSnapshotJSONWithDescription("golden-1", `"Xcode 26 and simulators"`))
	}, "snapshot", "save", persistentComputerID, "--name", "golden-1", "-d", "Xcode 26 and simulators")
	want := fmt.Sprintf("snapshot_id=%s name=golden-1 computer_id=%s saved_at=2026-10-11T08:00:00Z description=\"Xcode 26 and simulators\"\n", computerSnapshotID, persistentComputerID)
	if err != nil || stdout != want {
		t.Fatalf("snapshot save printed %q: %v", stdout, err)
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

func TestComputerSnapshotSaveExplainsTheOrgsSnapshotCap(t *testing.T) {
	_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"detail":"snapshot_limit"}`)
	}, "snapshot", "save", persistentComputerID, "--name", "golden-1")
	if err == nil || !strings.Contains(err.Error(), "as many snapshots as it may keep") {
		t.Fatalf("snapshot cap returned %v", err)
	}
}

func TestComputerCreateExplainsTheOrgsPersistentComputerCap(t *testing.T) {
	_, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"detail":"persistent_computer_limit"}`)
	}, "create", "--os", "macos", "--persistent")
	if err == nil || !strings.Contains(err.Error(), "as many persistent computers as it may keep") {
		t.Fatalf("persistent computer cap returned %v", err)
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
	want := `ID                                    NAME      COMPUTER                              SAVED                    EXPIRES  DESCRIPTION
55555555-5555-4555-8555-555555555555  golden-1  11111111-1111-4111-8111-111111111111  2026-10-11 08:00:00 UTC  -        -
Showing the newest 1 of 2 snapshots; use --limit (up to 100) for more.
`
	if err != nil || stdout != want {
		t.Fatalf("snapshot list printed\n%s\nwant\n%s(%v)", stdout, want, err)
	}
}

func TestComputerSnapshotListFitsEachDescriptionOnOneLine(t *testing.T) {
	long := strings.Repeat("Xcode 26 ", 10)
	stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"snapshots":[%s,%s],"total":2,"limit":25,"offset":0}`,
			computerSnapshotJSONWithDescription("multi", `"Xcode 26\n  and simulators"`),
			computerSnapshotJSONWithDescription("long", strconv.Quote(long)))
	}, "snapshot", "list")
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if err != nil || len(lines) != 3 ||
		!strings.HasSuffix(lines[1], "  Xcode 26 and simulators") ||
		!strings.HasSuffix(lines[2], "  "+string([]rune(strings.Join(strings.Fields(long), " "))[:57])+"...") {
		t.Fatalf("snapshot list printed\n%s(%v)", stdout, err)
	}
}

func TestComputerSnapshotListJSONCarriesEachDescriptionOrNull(t *testing.T) {
	stdout, _, err := runPersistentComputerCommand(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"snapshots":[%s,%s],"total":2,"limit":25,"offset":0}`,
			computerSnapshotJSONWithDescription("described", `"Xcode 26 and simulators"`),
			computerSnapshotJSON("predates-descriptions"))
	}, "snapshot", "list", "--json")
	var list struct {
		Snapshots []map[string]json.RawMessage `json:"snapshots"`
	}
	if err != nil || json.Unmarshal([]byte(stdout), &list) != nil || len(list.Snapshots) != 2 ||
		string(list.Snapshots[0]["description"]) != `"Xcode 26 and simulators"` ||
		string(list.Snapshots[1]["description"]) != "null" {
		t.Fatalf("snapshot list --json printed %s: %v", stdout, err)
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
