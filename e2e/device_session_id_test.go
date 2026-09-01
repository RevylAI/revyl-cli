//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// deviceStartResponse is the subset of `device start --json` that durable-ID
// targeting depends on.
type deviceStartResponse struct {
	SessionID     string `json:"session_id"`
	WorkflowRunID string `json:"workflow_run_id"`
	Index         int    `json:"index"`
}

// deviceInfoResponse is the subset of `device info --json` needed to prove a
// command hit the session it was pointed at.
type deviceInfoResponse struct {
	SessionID     string `json:"session_id"`
	WorkflowRunID string `json:"workflow_run_id"`
}

// sessionCacheState is the subset of .revyl/device-sessions.json needed to
// prove concurrent starts both landed in the shared file.
type sessionCacheState struct {
	Sessions []struct {
		SessionID string `json:"session_id"`
		Index     int    `json:"index"`
	} `json:"sessions"`
}

// findSessionCachePath locates the CLI's shared session file by walking up from
// the test working directory, mirroring how the CLI resolves its project root.
//
// Returns an empty string when no cache exists, which is not a failure: the
// caller then simply skips the byte-identity assertion.
func findSessionCachePath(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		candidate := filepath.Join(dir, ".revyl", "device-sessions.json")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// readFileOrEmpty reads a file, treating a missing file as empty content.
func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()

	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestDeviceParallelSessionIDBatch drives two concurrent device sessions using
// only server-issued session IDs, the way a parallel test batch does. No local
// session index is used anywhere after start, and the shared session cache must
// come out of the ID-targeted phase byte-identical.
//
// Gated by REVYL_E2E_DEVICE=true because device sessions are slow and expensive.
func TestDeviceParallelSessionIDBatch(t *testing.T) {
	if os.Getenv("REVYL_E2E_DEVICE") != "true" {
		t.Skip("REVYL_E2E_DEVICE not set; skipping device tests (slow/expensive)")
	}

	platform := os.Getenv("REVYL_E2E_DEVICE_PLATFORM")
	if platform == "" {
		platform = "ios"
	}

	const batchSize = 2
	sessionIDs := make([]string, batchSize)

	step(t, "start_parallel_sessions", func(st *testing.T) {
		var wg sync.WaitGroup
		results := make([]deviceStartResponse, batchSize)
		failures := make([]string, batchSize)

		for i := range batchSize {
			wg.Add(1)
			go func(slot int) {
				defer wg.Done()
				result := runCLI(t, "device", "start", "--platform", platform, "--json")
				if result.ExitCode != 0 {
					failures[slot] = result.Stdout + result.Stderr
					return
				}
				if err := json.Unmarshal([]byte(extractJSON(result.Stdout)), &results[slot]); err != nil {
					failures[slot] = err.Error()
				}
			}(i)
		}
		wg.Wait()

		for slot, failure := range failures {
			if failure != "" {
				st.Fatalf("device start (slot %d) failed: %s", slot, failure)
			}
		}
		for slot, started := range results {
			// A durable ID on every start is what makes the whole ID-targeted
			// path usable; an empty one silently forces callers back to indexes.
			if started.SessionID == "" {
				st.Fatalf("device start (slot %d) returned an empty session_id: %+v", slot, started)
			}
			sessionIDs[slot] = started.SessionID
		}
		if sessionIDs[0] == sessionIDs[1] {
			st.Fatalf("parallel starts returned the same session_id %q", sessionIDs[0])
		}

		t.Cleanup(func() {
			for _, sessionID := range sessionIDs {
				if sessionID != "" {
					_ = runCLI(t, "device", "stop", "-s", sessionID)
				}
			}
		})
	})

	cachePath := findSessionCachePath(t)

	step(t, "parallel_starts_both_reach_the_session_cache", func(st *testing.T) {
		if cachePath == "" {
			st.Skip("no .revyl/device-sessions.json present; nothing to verify")
		}

		// `device start` cannot be ID-targeted, so it is the one phase that still
		// takes the shared-file path. A blind overwrite here loses whichever
		// session finished first, which is invisible until a later `device list`.
		var cached sessionCacheState
		if err := json.Unmarshal([]byte(readFileOrEmpty(t, cachePath)), &cached); err != nil {
			st.Fatalf("parse %s: %v", cachePath, err)
		}

		indexByID := make(map[string]int, len(cached.Sessions))
		for _, session := range cached.Sessions {
			indexByID[session.SessionID] = session.Index
		}
		for slot, sessionID := range sessionIDs {
			if _, present := indexByID[sessionID]; !present {
				st.Fatalf("session %d (%s) missing from %s after concurrent starts: %v",
					slot, sessionID, cachePath, indexByID)
			}
		}
		if indexByID[sessionIDs[0]] == indexByID[sessionIDs[1]] {
			st.Fatalf("concurrent starts share index %d: %v", indexByID[sessionIDs[0]], indexByID)
		}
	})

	cacheBeforeBatch := readFileOrEmpty(t, cachePath)

	step(t, "drive_sessions_by_id_in_parallel", func(st *testing.T) {
		var wg sync.WaitGroup
		mismatches := make([]string, batchSize)

		for i := range batchSize {
			wg.Add(1)
			go func(slot int) {
				defer wg.Done()
				sessionID := sessionIDs[slot]

				// -s carrying an ID, --session-id, and REVYL_SESSION_ID are the
				// three supported spellings; each worker exercises all of them.
				info := runCLI(t, "device", "info", "-s", sessionID, "--json")
				if info.ExitCode != 0 {
					mismatches[slot] = "device info -s <id>: " + info.Stdout + info.Stderr
					return
				}
				var infoResp deviceInfoResponse
				if err := json.Unmarshal([]byte(extractJSON(info.Stdout)), &infoResp); err != nil {
					mismatches[slot] = "parse device info: " + err.Error()
					return
				}
				if infoResp.SessionID != sessionID {
					mismatches[slot] = "device info targeted " + infoResp.SessionID + ", want " + sessionID
					return
				}

				if shot := runCLI(t, "device", "screenshot", "--session-id", sessionID, "--json"); shot.ExitCode != 0 {
					mismatches[slot] = "device screenshot --session-id: " + shot.Stdout + shot.Stderr
					return
				}

				scoped := runCLIWithExtraEnv(t, []string{"REVYL_SESSION_ID=" + sessionID},
					"device", "tap", "--x", "120", "--y", "340", "--json")
				if scoped.ExitCode != 0 {
					mismatches[slot] = "device tap via REVYL_SESSION_ID: " + scoped.Stdout + scoped.Stderr
				}
			}(i)
		}
		wg.Wait()

		for slot, mismatch := range mismatches {
			if mismatch != "" {
				st.Fatalf("session %d: %s", slot, mismatch)
			}
		}
	})

	step(t, "session_cache_untouched_by_id_targeting", func(st *testing.T) {
		if cachePath == "" {
			st.Skip("no .revyl/device-sessions.json present; nothing to protect")
		}
		if after := readFileOrEmpty(t, cachePath); after != cacheBeforeBatch {
			st.Fatalf("ID-targeted batch mutated %s:\n got %s\nwant %s", cachePath, after, cacheBeforeBatch)
		}
	})

	step(t, "stop_sessions_by_id", func(st *testing.T) {
		for slot, sessionID := range sessionIDs {
			result := runCLI(t, "device", "stop", "-s", sessionID)
			if result.ExitCode != 0 {
				st.Fatalf("device stop -s <id> (slot %d) failed: %s\n%s", slot, result.Stdout, result.Stderr)
			}
			sessionIDs[slot] = ""
		}
	})
}
