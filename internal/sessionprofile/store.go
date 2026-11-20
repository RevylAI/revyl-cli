package sessionprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revyl/cli/internal/filelock"
)

// appliedProfilesFileName is the local, mode-0600 record of the profile each
// device session started with, which reuse and auth refresh read so they never
// rerun the script.
const appliedProfilesFileName = "applied-session-profiles.json"

// appliedProfilesLockFileName guards the record's read-modify-write across the
// CLI and MCP processes that start and stop sessions in one project.
const appliedProfilesLockFileName = "applied-session-profiles.lock"

// appliedProfilesLockTimeout bounds the wait for another process's update,
// which holds the lock only across one small read-modify-write.
const appliedProfilesLockTimeout = 5 * time.Second

// AppliedProfile is the profile a device session started with.
type AppliedProfile struct {
	Name string `json:"name"`
	// MobileDeepLink is the deep link template the CLI opens after each launch,
	// with ${NAME} references the device proxy fills.
	MobileDeepLink *string `json:"mobile_deep_link,omitempty"`
}

type appliedProfilesFile struct {
	Sessions map[string]AppliedProfile `json:"sessions"`
}

// SaveAppliedProfile records the profile a device session started with.
func SaveAppliedProfile(projectRoot, sessionID string, profile AppliedProfile) error {
	sessionID = strings.TrimSpace(sessionID)
	if projectRoot == "" || sessionID == "" {
		return nil
	}
	path := appliedProfilesPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create applied session profiles directory: %w", err)
	}
	release, err := lockAppliedProfiles(path)
	if err != nil {
		return err
	}
	defer release()
	state, err := readAppliedProfiles(path)
	if err != nil {
		return err
	}
	state.Sessions[sessionID] = profile
	return writeAppliedProfiles(path, state)
}

// LoadAppliedProfile returns the profile a device session started with, or nil
// when it started without one.
func LoadAppliedProfile(projectRoot, sessionID string) (*AppliedProfile, error) {
	sessionID = strings.TrimSpace(sessionID)
	if projectRoot == "" || sessionID == "" {
		return nil, nil
	}
	state, err := readAppliedProfiles(appliedProfilesPath(projectRoot))
	if err != nil {
		return nil, err
	}
	profile, ok := state.Sessions[sessionID]
	if !ok {
		return nil, nil
	}
	return &profile, nil
}

// ClearAppliedProfile forgets a device session's profile once it ends. A record
// it can't parse is removed whole, since it may still hold this session's deep
// link and the values the script minted into it.
func ClearAppliedProfile(projectRoot, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if projectRoot == "" || sessionID == "" {
		return nil
	}
	path := appliedProfilesPath(projectRoot)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	release, err := lockAppliedProfiles(path)
	if err != nil {
		return err
	}
	defer release()
	state, err := readAppliedProfiles(path)
	if errors.Is(err, errAppliedProfilesUnparseable) {
		if removeErr := removeAppliedProfiles(path); removeErr != nil {
			return removeErr
		}
		return fmt.Errorf("removed the unreadable %s while clearing session %s: %w", appliedProfilesFileName, sessionID, err)
	}
	if err != nil {
		return err
	}
	if _, ok := state.Sessions[sessionID]; !ok {
		return nil
	}
	delete(state.Sessions, sessionID)
	if len(state.Sessions) == 0 {
		return removeAppliedProfiles(path)
	}
	return writeAppliedProfiles(path, state)
}

func appliedProfilesPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".revyl", appliedProfilesFileName)
}

func lockAppliedProfiles(path string) (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), appliedProfilesLockTimeout)
	defer cancel()
	release, err := filelock.Acquire(ctx, filepath.Join(filepath.Dir(path), appliedProfilesLockFileName))
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("timed out after %s waiting for another revyl process to update %s", appliedProfilesLockTimeout, appliedProfilesFileName)
	}
	if err != nil {
		return nil, fmt.Errorf("lock applied session profiles: %w", err)
	}
	return release, nil
}

// errAppliedProfilesUnparseable marks a record file that exists but isn't
// valid JSON of the record's shape.
var errAppliedProfilesUnparseable = errors.New("parse applied session profiles")

func readAppliedProfiles(path string) (appliedProfilesFile, error) {
	state := appliedProfilesFile{Sessions: map[string]AppliedProfile{}}
	data, err := os.ReadFile(path) // #nosec G304 -- this project's own applied-profile record under .revyl/
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read applied session profiles: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("%w: %w", errAppliedProfilesUnparseable, err)
	}
	if state.Sessions == nil {
		state.Sessions = map[string]AppliedProfile{}
	}
	return state, nil
}

// writeAppliedProfiles replaces the record through a private temporary file
// and a rename, so readers never see a partial record and every copy is mode
// 0600, since a deep link template may carry values the script minted.
func writeAppliedProfiles(path string, state appliedProfilesFile) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode applied session profiles: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".applied-session-profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("write applied session profiles: %w", err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write applied session profiles: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write applied session profiles: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("replace applied session profiles: %w", err)
	}
	return nil
}

func removeAppliedProfiles(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove applied session profiles: %w", err)
	}
	return nil
}
