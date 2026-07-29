package beforesession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// valuesFileName is the local, mode-0600 cache of before_session values minted
// for a device session. Auth refresh reuses these so the deep link keeps the
// same token the app received as launch environment at boot.
const valuesFileName = "before-session-values.json"

// valuesFile is the on-disk shape of the boot-values cache.
type valuesFile struct {
	// Sessions maps device session ID to the KEY=VALUE pairs minted for it.
	Sessions map[string]map[string]string `json:"sessions"`
}

// SaveSessionValues persists before_session values for one device session.
//
// Parameters:
//   - workDir: Repository / project root that owns `.revyl/`.
//   - sessionID: Backend device session ID the values were minted for.
//   - values: Session-scoped KEY=VALUE pairs. Empty or blank sessionID is a no-op.
//
// Returns:
//   - error: Filesystem or encode failure. Values are never logged.
func SaveSessionValues(workDir, sessionID string, values map[string]string) error {
	sessionID = strings.TrimSpace(sessionID)
	if strings.TrimSpace(workDir) == "" || sessionID == "" || len(values) == 0 {
		return nil
	}

	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}

	path := valuesPath(workDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create before_session values directory: %w", err)
	}

	state, err := readValuesFile(path)
	if err != nil {
		return err
	}
	if state.Sessions == nil {
		state.Sessions = make(map[string]map[string]string)
	}
	state.Sessions[sessionID] = copied
	return writeValuesFile(path, state)
}

// LoadSessionValues returns the before_session values minted for a session.
//
// Parameters:
//   - workDir: Repository / project root that owns `.revyl/`.
//   - sessionID: Backend device session ID to look up.
//
// Returns:
//   - map[string]string: A copy of the stored values, or nil when absent.
//   - error: Filesystem or decode failure. Missing file is not an error.
func LoadSessionValues(workDir, sessionID string) (map[string]string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if strings.TrimSpace(workDir) == "" || sessionID == "" {
		return nil, nil
	}

	state, err := readValuesFile(valuesPath(workDir))
	if err != nil {
		return nil, err
	}
	stored := state.Sessions[sessionID]
	if len(stored) == 0 {
		return nil, nil
	}

	copied := make(map[string]string, len(stored))
	for key, value := range stored {
		copied[key] = value
	}
	return copied, nil
}

// ClearSessionValues removes persisted before_session values for a session.
//
// Parameters:
//   - workDir: Repository / project root that owns `.revyl/`.
//   - sessionID: Backend device session ID to forget.
//
// Returns:
//   - error: Filesystem or encode failure. Missing file is not an error.
func ClearSessionValues(workDir, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if strings.TrimSpace(workDir) == "" || sessionID == "" {
		return nil
	}

	path := valuesPath(workDir)
	state, err := readValuesFile(path)
	if err != nil {
		return err
	}
	if len(state.Sessions) == 0 {
		return nil
	}
	if _, ok := state.Sessions[sessionID]; !ok {
		return nil
	}
	delete(state.Sessions, sessionID)
	if len(state.Sessions) == 0 {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove before_session values file: %w", removeErr)
		}
		return nil
	}
	return writeValuesFile(path, state)
}

// valuesPath returns the absolute path of the boot-values cache.
func valuesPath(workDir string) string {
	return filepath.Join(workDir, ".revyl", valuesFileName)
}

// readValuesFile loads the cache, treating a missing file as empty state.
func readValuesFile(path string) (valuesFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return valuesFile{Sessions: map[string]map[string]string{}}, nil
		}
		return valuesFile{}, fmt.Errorf("read before_session values: %w", err)
	}
	var state valuesFile
	if err := json.Unmarshal(data, &state); err != nil {
		return valuesFile{}, fmt.Errorf("parse before_session values: %w", err)
	}
	if state.Sessions == nil {
		state.Sessions = map[string]map[string]string{}
	}
	return state, nil
}

// writeValuesFile atomically replaces the cache with mode 0600.
func writeValuesFile(path string, state valuesFile) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode before_session values: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write before_session values: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace before_session values: %w", err)
	}
	return nil
}
