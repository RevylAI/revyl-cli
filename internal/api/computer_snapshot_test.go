package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const computerSnapshotFixtureID = "55555555-5555-4555-8555-555555555555"

func computerSnapshotFixture(name string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"computer_id":%q,"saved_at":"2026-10-11T08:00:00Z","expires_at":"2026-11-10T08:00:00Z"}`, computerSnapshotFixtureID, name, computerValidationID)
}

func TestComputerSnapshotReadsBindToTheRequestedSnapshot(t *testing.T) {
	for name, test := range map[string]struct {
		reference string
		body      string
		ok        bool
	}{
		"by_name":        {"golden-1", computerSnapshotFixture("golden-1"), true},
		"by_id":          {computerSnapshotFixtureID, computerSnapshotFixture("golden-1"), true},
		"other_snapshot": {"golden-2", computerSnapshotFixture("golden-1"), false},
		"missing_saved":  {"golden-1", fmt.Sprintf(`{"id":%q,"name":"golden-1","computer_id":%q}`, computerSnapshotFixtureID, computerValidationID), false},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			snapshot, err := NewClientWithBaseURL("fixture-key", server.URL).GetComputerSnapshot(context.Background(), test.reference)
			if test.ok != (err == nil) || test.ok && (snapshot.ExpiresAt == nil || snapshot.ComputerID != computerValidationID) {
				t.Fatalf("get %s returned %v, %v", test.reference, snapshot, err)
			}
		})
	}
}

func TestComputerSnapshotListRejectsAMalformedSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"snapshots":[%s],"total":1,"limit":5,"offset":0}`, computerSnapshotFixture("has space"))
	}))
	defer server.Close()
	if _, err := NewClientWithBaseURL("fixture-key", server.URL).ListComputerSnapshots(context.Background(), 5, 0); err == nil {
		t.Fatal("list accepted a snapshot whose name no reference could match")
	}
}

func TestComputerSnapshotRefusalsKeepTheirCodes(t *testing.T) {
	for _, code := range []string{"computer_not_stopped", "saved_disk_not_durable", "snapshot_name_taken", "snapshot_expired", "snapshot_limit"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			fmt.Fprintf(w, `{"detail":%q}`, code)
		}))
		_, err := NewClientWithBaseURL("fixture-key", server.URL).SaveComputerSnapshot(context.Background(), computerValidationID, "golden-1", "")
		server.Close()
		var refusal *APIError
		if !errors.As(err, &refusal) || refusal.Code != code {
			t.Fatalf("%s refusal returned %v", code, err)
		}
	}
}
