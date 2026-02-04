// Package sync provides version conflict detection and resolution for test synchronization.
//
// This package handles detecting conflicts between local and remote test versions,
// and provides strategies for resolving them.
package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// SyncStatus represents the sync status of a test.
type SyncStatus int

const (
	// StatusSynced means local and remote are in sync.
	StatusSynced SyncStatus = iota
	// StatusModified means local has changes not pushed.
	StatusModified
	// StatusOutdated means remote has changes not pulled.
	StatusOutdated
	// StatusConflict means both local and remote have changes.
	StatusConflict
	// StatusLocalOnly means test exists only locally.
	StatusLocalOnly
	// StatusRemoteOnly means test exists only on remote.
	StatusRemoteOnly
)

// String returns the string representation of a sync status.
func (s SyncStatus) String() string {
	switch s {
	case StatusSynced:
		return "synced"
	case StatusModified:
		return "modified"
	case StatusOutdated:
		return "outdated"
	case StatusConflict:
		return "conflict"
	case StatusLocalOnly:
		return "local-only"
	case StatusRemoteOnly:
		return "remote-only"
	default:
		return "unknown"
	}
}

// TestSyncStatus contains sync status information for a test.
type TestSyncStatus struct {
	// Name is the test name/alias.
	Name string
	// Status is the sync status.
	Status SyncStatus
	// LocalVersion is the local version number.
	LocalVersion int
	// RemoteVersion is the remote version number.
	RemoteVersion int
	// LastSync is a human-readable last sync time.
	LastSync string
	// RemoteID is the test ID on the server.
	RemoteID string
}

// SyncResult contains the result of a sync operation.
type SyncResult struct {
	// Name is the test name.
	Name string
	// NewVersion is the new version after sync.
	NewVersion int
	// Conflict indicates if there was a conflict.
	Conflict bool
	// Error is any error that occurred.
	Error error
}

// Resolver handles test name resolution and sync operations.
type Resolver struct {
	client     *api.Client
	config     *config.ProjectConfig
	localTests map[string]*config.LocalTest
}

// NewResolver creates a new sync resolver.
//
// Parameters:
//   - client: The API client
//   - cfg: The project configuration
//   - localTests: Map of local test definitions
//
// Returns:
//   - *Resolver: A new resolver instance
func NewResolver(client *api.Client, cfg *config.ProjectConfig, localTests map[string]*config.LocalTest) *Resolver {
	return &Resolver{
		client:     client,
		config:     cfg,
		localTests: localTests,
	}
}

// GetAllStatuses returns sync status for all known tests.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - []TestSyncStatus: List of test sync statuses
//   - error: Any error that occurred
func (r *Resolver) GetAllStatuses(ctx context.Context) ([]TestSyncStatus, error) {
	var statuses []TestSyncStatus

	// Collect all test names from config aliases and local files
	testNames := make(map[string]bool)
	for name := range r.config.Tests {
		testNames[name] = true
	}
	for name := range r.localTests {
		testNames[name] = true
	}

	for name := range testNames {
		status, err := r.getTestStatus(ctx, name)
		if err != nil {
			// Include error in status
			statuses = append(statuses, TestSyncStatus{
				Name:   name,
				Status: StatusLocalOnly,
			})
			continue
		}
		statuses = append(statuses, *status)
	}

	return statuses, nil
}

// getTestStatus gets the sync status for a single test.
func (r *Resolver) getTestStatus(ctx context.Context, name string) (*TestSyncStatus, error) {
	status := &TestSyncStatus{Name: name}

	// Check if we have a local test
	localTest, hasLocal := r.localTests[name]

	// Check if we have a remote ID (from config or local test)
	var remoteID string
	if id, ok := r.config.Tests[name]; ok {
		remoteID = id
	} else if hasLocal && localTest.Meta.RemoteID != "" {
		remoteID = localTest.Meta.RemoteID
	}

	status.RemoteID = remoteID

	if hasLocal {
		status.LocalVersion = localTest.Meta.LocalVersion
		if localTest.Meta.LastSyncedAt != "" {
			status.LastSync = formatTimeAgo(localTest.Meta.LastSyncedAt)
		} else {
			status.LastSync = "never"
		}
	}

	// If no remote ID, it's local-only
	if remoteID == "" {
		status.Status = StatusLocalOnly
		return status, nil
	}

	// Fetch remote version
	remoteTest, err := r.client.GetTest(ctx, remoteID)
	if err != nil {
		// Remote not found - might be deleted
		status.Status = StatusLocalOnly
		return status, nil
	}

	status.RemoteVersion = remoteTest.Version

	// Determine sync status
	if !hasLocal {
		status.Status = StatusRemoteOnly
	} else if localTest.Meta.LocalVersion == localTest.Meta.RemoteVersion &&
		localTest.Meta.RemoteVersion == remoteTest.Version {
		status.Status = StatusSynced
	} else if localTest.Meta.LocalVersion > localTest.Meta.RemoteVersion &&
		localTest.Meta.RemoteVersion == remoteTest.Version {
		status.Status = StatusModified
	} else if localTest.Meta.LocalVersion == localTest.Meta.RemoteVersion &&
		remoteTest.Version > localTest.Meta.RemoteVersion {
		status.Status = StatusOutdated
	} else {
		status.Status = StatusConflict
	}

	return status, nil
}

// SyncToRemote pushes local changes to remote.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testName: Specific test name (empty for all)
//   - testsDir: Directory where tests are stored (for saving updated metadata)
//   - force: Force overwrite remote
//
// Returns:
//   - []SyncResult: Results for each synced test
//   - error: Any error that occurred
func (r *Resolver) SyncToRemote(ctx context.Context, testName, testsDir string, force bool) ([]SyncResult, error) {
	var results []SyncResult

	testsToSync := make(map[string]*config.LocalTest)
	if testName != "" {
		if test, ok := r.localTests[testName]; ok {
			testsToSync[testName] = test
		} else {
			return nil, fmt.Errorf("test not found: %s", testName)
		}
	} else {
		testsToSync = r.localTests
	}

	for name, localTest := range testsToSync {
		result := SyncResult{Name: name}

		// Get remote ID
		remoteID := localTest.Meta.RemoteID
		if remoteID == "" {
			if id, ok := r.config.Tests[name]; ok {
				remoteID = id
			}
		}

		if remoteID == "" {
			// Create new test on remote
			resp, err := r.client.CreateTest(ctx, &api.CreateTestRequest{
				Name:     localTest.Test.Metadata.Name,
				Platform: localTest.Test.Metadata.Platform,
				Tasks:    localTest.Test.Blocks,
			})
			if err != nil {
				result.Error = err
			} else {
				result.NewVersion = resp.Version
				localTest.Meta.RemoteID = resp.ID
				localTest.Meta.RemoteVersion = resp.Version
				localTest.Meta.LocalVersion = resp.Version
				localTest.Meta.LastSyncedAt = time.Now().Format(time.RFC3339)

				// Save updated local test file
				path := filepath.Join(testsDir, name+".yaml")
				if saveErr := config.SaveLocalTest(path, localTest); saveErr != nil {
					// Log but don't fail - the remote sync succeeded
					result.Error = fmt.Errorf("synced but failed to save local file: %w", saveErr)
				}
			}
		} else {
			// Update existing test
			expectedVersion := 0
			if !force {
				expectedVersion = localTest.Meta.RemoteVersion
			}

			resp, err := r.client.UpdateTest(ctx, &api.UpdateTestRequest{
				TestID:          remoteID,
				Tasks:           localTest.Test.Blocks,
				ExpectedVersion: expectedVersion,
				Force:           force,
			})
			if err != nil {
				// Check if it's a version conflict
				if apiErr, ok := err.(*api.APIError); ok && apiErr.StatusCode == 409 {
					result.Conflict = true
				} else {
					result.Error = err
				}
			} else {
				result.NewVersion = resp.Version
				localTest.Meta.RemoteVersion = resp.Version
				localTest.Meta.LocalVersion = resp.Version
				localTest.Meta.LastSyncedAt = time.Now().Format(time.RFC3339)

				// Save updated local test file
				path := filepath.Join(testsDir, name+".yaml")
				if saveErr := config.SaveLocalTest(path, localTest); saveErr != nil {
					// Log but don't fail - the remote sync succeeded
					result.Error = fmt.Errorf("synced but failed to save local file: %w", saveErr)
				}
			}
		}

		results = append(results, result)
	}

	return results, nil
}

// PullFromRemote pulls remote changes to local.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testName: Specific test name (empty for all)
//   - testsDir: Directory to save tests
//   - force: Force overwrite local
//
// Returns:
//   - []SyncResult: Results for each pulled test
//   - error: Any error that occurred
func (r *Resolver) PullFromRemote(ctx context.Context, testName, testsDir string, force bool) ([]SyncResult, error) {
	var results []SyncResult

	// Ensure tests directory exists
	if err := ensureDir(testsDir); err != nil {
		return nil, fmt.Errorf("failed to create tests directory: %w", err)
	}

	testsToPull := make(map[string]string) // name -> remoteID
	if testName != "" {
		if id, ok := r.config.Tests[testName]; ok {
			testsToPull[testName] = id
		} else if local, ok := r.localTests[testName]; ok && local.Meta.RemoteID != "" {
			testsToPull[testName] = local.Meta.RemoteID
		} else {
			return nil, fmt.Errorf("test not found or has no remote ID: %s", testName)
		}
	} else {
		// Include tests from config
		for name, id := range r.config.Tests {
			testsToPull[name] = id
		}
		// Also include local tests that have a remote ID but aren't in config
		for name, local := range r.localTests {
			if local.Meta.RemoteID != "" {
				if _, exists := testsToPull[name]; !exists {
					testsToPull[name] = local.Meta.RemoteID
				}
			}
		}
	}

	for name, remoteID := range testsToPull {
		result := SyncResult{Name: name}

		// Check for local changes
		if local, ok := r.localTests[name]; ok && !force {
			if local.Meta.LocalVersion > local.Meta.RemoteVersion {
				result.Conflict = true
				results = append(results, result)
				continue
			}
		}

		// Fetch remote test
		remoteTest, err := r.client.GetTest(ctx, remoteID)
		if err != nil {
			result.Error = err
			results = append(results, result)
			continue
		}

		// Convert tasks to blocks
		blocks := convertTasksToBlocks(remoteTest.Tasks)

		// Convert to local format
		localTest := &config.LocalTest{
			Meta: config.TestMeta{
				RemoteID:      remoteID,
				RemoteVersion: remoteTest.Version,
				LocalVersion:  remoteTest.Version,
				LastSyncedAt:  time.Now().Format(time.RFC3339),
			},
			Test: config.TestDefinition{
				Metadata: config.TestMetadata{
					Name:     remoteTest.Name,
					Platform: strings.ToLower(remoteTest.Platform), // Normalize platform case
				},
				Blocks: blocks,
			},
		}

		// Add build info if available
		if remoteTest.BuildVarID != "" {
			// Fetch build var name (gracefully handle errors - don't fail the pull)
			buildVar, err := r.client.GetBuildVar(ctx, remoteTest.BuildVarID)
			if err == nil && buildVar != nil {
				localTest.Test.Build = config.TestBuildConfig{
					Name: buildVar.Name,
				}
			}
		}
		// Add pinned version if set
		if remoteTest.PinnedVersion != "" {
			localTest.Test.Build.PinnedVersion = remoteTest.PinnedVersion
		}

		// Save to file
		path := filepath.Join(testsDir, name+".yaml")
		if err := config.SaveLocalTest(path, localTest); err != nil {
			result.Error = err
		} else {
			result.NewVersion = remoteTest.Version
		}

		results = append(results, result)
	}

	return results, nil
}

// GetDiff returns a diff between local and remote versions.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testName: The test name
//
// Returns:
//   - string: The diff output
//   - error: Any error that occurred
func (r *Resolver) GetDiff(ctx context.Context, testName string) (string, error) {
	localTest, hasLocal := r.localTests[testName]
	if !hasLocal {
		return "", fmt.Errorf("local test not found: %s", testName)
	}

	remoteID := localTest.Meta.RemoteID
	if remoteID == "" {
		if id, ok := r.config.Tests[testName]; ok {
			remoteID = id
		}
	}

	if remoteID == "" {
		return "", fmt.Errorf("no remote ID for test: %s", testName)
	}

	remoteTest, err := r.client.GetTest(ctx, remoteID)
	if err != nil {
		return "", err
	}

	// Generate simple diff
	localYAML, _ := yaml.Marshal(localTest.Test)
	remoteYAML, _ := yaml.Marshal(remoteTest.Tasks)

	return generateSimpleDiff(string(localYAML), string(remoteYAML)), nil
}

// ComputeChecksum computes a SHA256 checksum of test content.
//
// Parameters:
//   - test: The test definition
//
// Returns:
//   - string: The checksum as a hex string
func ComputeChecksum(test *config.TestDefinition) string {
	data, _ := yaml.Marshal(test)
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

// formatTimeAgo formats a timestamp as a human-readable "time ago" string.
func formatTimeAgo(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return timestamp
	}

	duration := time.Since(t)

	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		mins := int(duration.Minutes())
		return fmt.Sprintf("%dm ago", mins)
	case duration < 24*time.Hour:
		hours := int(duration.Hours())
		return fmt.Sprintf("%dh ago", hours)
	default:
		days := int(duration.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}

// generateSimpleDiff generates a simple line-by-line diff.
func generateSimpleDiff(local, remote string) string {
	// Simple implementation - in production, use a proper diff library
	if local == remote {
		return ""
	}

	return fmt.Sprintf("--- local\n+++ remote\n@@ changes @@\n-%s\n+%s", local, remote)
}

// convertTasksToBlocks converts the API tasks (interface{}) to []config.TestBlock.
//
// Parameters:
//   - tasks: The tasks from the API response (can be []interface{}, []map[string]interface{}, etc.)
//
// Returns:
//   - []config.TestBlock: The converted blocks, or nil if conversion fails
func convertTasksToBlocks(tasks interface{}) []config.TestBlock {
	if tasks == nil {
		return nil
	}

	// Marshal to JSON then unmarshal to []TestBlock
	// This handles the type conversion from interface{} to the concrete struct
	data, err := json.Marshal(tasks)
	if err != nil {
		return nil
	}

	var blocks []config.TestBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil
	}

	return blocks
}

// ensureDir creates a directory if it doesn't exist.
//
// Parameters:
//   - path: The directory path to create
//
// Returns:
//   - error: Any error that occurred during creation
func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}
