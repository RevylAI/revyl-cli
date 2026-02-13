// Package config provides project configuration management.
//
// This file handles reading and parsing .revyl/sessions.json files that use
// the Terminal Keeper format for defining service session profiles.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// SessionsConfig represents the top-level .revyl/sessions.json file.
//
// Uses the same format as the Terminal Keeper VS Code extension (schema v11).
// This enables shared session definitions that work across VS Code (Terminal Keeper),
// Fleet Dashboard, and the Revyl CLI.
type SessionsConfig struct {
	// Active is the name of the session to launch by default.
	Active string `json:"active"`

	// ActivateOnStartup controls whether to activate on IDE startup (Terminal Keeper field).
	ActivateOnStartup bool `json:"activateOnStartup"`

	// KeepExistingTerminals controls whether to keep existing terminals (Terminal Keeper field).
	KeepExistingTerminals bool `json:"keepExistingTerminals"`

	// NoClear controls whether to skip clearing terminals (Terminal Keeper field).
	NoClear bool `json:"noClear"`

	// Sessions maps session names to their terminal definitions.
	// Each session is an array of items, where each item is either a single
	// TerminalDefinition (JSON object) or an array of TerminalDefinitions
	// (JSON array = split-pane group in VS Code).
	Sessions map[string][]json.RawMessage `json:"sessions"`
}

// TerminalDefinition is a single terminal tab definition.
//
// Each definition describes one terminal: its name, visual appearance,
// and the shell commands to execute when the terminal is created.
type TerminalDefinition struct {
	// Name is the display name for the terminal tab.
	Name string `json:"name"`

	// AutoExecuteCommands controls whether commands run automatically.
	// When false, commands are shown but not executed (e.g. setup terminals).
	// Defaults to true when nil/absent.
	AutoExecuteCommands *bool `json:"autoExecuteCommands,omitempty"`

	// Icon is the VS Code codicon name for the tab icon.
	Icon string `json:"icon,omitempty"`

	// Color is the terminal tab color (e.g. "terminal.ansiGreen").
	Color string `json:"color,omitempty"`

	// Focus indicates whether this terminal should receive focus when created.
	Focus bool `json:"focus,omitempty"`

	// Commands are shell commands to execute sequentially in the terminal.
	// Each command is relative to the repository root directory.
	Commands []string `json:"commands"`
}

// ShouldAutoExecute returns whether commands should auto-execute.
//
// Defaults to true when AutoExecuteCommands is nil (not specified in JSON).
//
// Returns:
//   - bool: true if commands should auto-execute
func (t *TerminalDefinition) ShouldAutoExecute() bool {
	if t.AutoExecuteCommands == nil {
		return true
	}
	return *t.AutoExecuteCommands
}

// LoadSessionsConfig loads the sessions configuration from a repository root.
//
// Reads and parses `.revyl/sessions.json` from the given directory.
//
// Parameters:
//   - repoRoot: Path to the repository root directory.
//
// Returns:
//   - *SessionsConfig: The parsed sessions configuration.
//   - error: Error if the file doesn't exist or can't be parsed.
func LoadSessionsConfig(repoRoot string) (*SessionsConfig, error) {
	path := filepath.Join(repoRoot, ".revyl", "sessions.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read .revyl/sessions.json: %w", err)
	}

	var cfg SessionsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse .revyl/sessions.json: %w", err)
	}

	return &cfg, nil
}

// FlattenSession extracts all TerminalDefinition items from a session's raw JSON items.
//
// Each session item can be either a single TerminalDefinition (JSON object) or an
// array of TerminalDefinitions (JSON array = split-pane group). This function
// flattens both into a single slice.
//
// Parameters:
//   - items: Raw JSON messages from a session definition.
//
// Returns:
//   - []TerminalDefinition: All terminal definitions in order.
//   - error: Error if any item cannot be decoded.
func FlattenSession(items []json.RawMessage) ([]TerminalDefinition, error) {
	var result []TerminalDefinition

	for _, raw := range items {
		// Try as array first (split group)
		var group []TerminalDefinition
		if err := json.Unmarshal(raw, &group); err == nil {
			result = append(result, group...)
			continue
		}

		// Fall back to single object
		var single TerminalDefinition
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil, fmt.Errorf("failed to decode session item: %w", err)
		}
		result = append(result, single)
	}

	return result, nil
}

// SessionNames returns all session names sorted alphabetically, with the active
// session first.
//
// Parameters:
//   - cfg: The sessions configuration.
//
// Returns:
//   - []string: Ordered slice of session names.
func SessionNames(cfg *SessionsConfig) []string {
	names := make([]string, 0, len(cfg.Sessions))
	for name := range cfg.Sessions {
		names = append(names, name)
	}
	sort.Strings(names)

	// Move active session to front
	if cfg.Active == "" {
		return names
	}

	sorted := make([]string, 0, len(names))
	sorted = append(sorted, cfg.Active)
	for _, name := range names {
		if name != cfg.Active {
			sorted = append(sorted, name)
		}
	}
	return sorted
}

// FindRepoRoot walks up from the given directory looking for a .revyl/ directory.
//
// Returns the first ancestor (or the directory itself) that contains a .revyl/ subdirectory.
//
// Parameters:
//   - dir: Starting directory to search from.
//
// Returns:
//   - string: The repo root path containing .revyl/.
//   - error: Error if no .revyl/ directory is found before reaching /.
func FindRepoRoot(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	current := absDir
	for {
		revylDir := filepath.Join(current, ".revyl")
		if info, err := os.Stat(revylDir); err == nil && info.IsDir() {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no .revyl/ directory found (searched from %s to /)", absDir)
		}
		current = parent
	}
}
