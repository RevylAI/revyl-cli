package beforesession

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Auth refresh reuses the script's minted values from this local cache
// instead of rerunning the script.
const valuesFileName = "before-session-values.json"

type valuesFile struct {
	// Sessions maps device session ID to the KEY=VALUE pairs minted for it.
	Sessions map[string]map[string]string `json:"sessions"`
}

// SaveSessionValues stores a copy under the backend device session ID in the
// project's local cache. Blank paths or IDs and empty values are no-ops.
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

// LoadSessionValues returns a copy of the session's stored values, or nil
// without an error when the file or session entry is absent.
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

// ClearSessionValues removes only the given session's entry and tolerates
// an absent file or entry.
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

func valuesPath(workDir string) string {
	return filepath.Join(workDir, ".revyl", valuesFileName)
}

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

// The cache contains secret values: request mode 0600 when creating the
// temporary file. The rename does not serialize concurrent updates.
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
