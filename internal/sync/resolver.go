// Package sync provides version conflict detection and resolution for test synchronization.
//
// This package handles detecting conflicts between local and remote test versions,
// and provides strategies for resolving them.
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	// ErrorMessage contains any error that occurred while determining status.
	ErrorMessage string
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
			// Include error in status - mark as local only but preserve the error message
			statuses = append(statuses, TestSyncStatus{
				Name:         name,
				Status:       StatusLocalOnly,
				ErrorMessage: err.Error(),
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

	// Check for local modifications using checksum-based detection
	hasLocalChanges := hasLocal && localTest.HasLocalChanges()

	// Determine sync status
	if !hasLocal {
		status.Status = StatusRemoteOnly
	} else if hasLocalChanges && remoteTest.Version > localTest.Meta.RemoteVersion {
		// Both local content changed AND remote has newer version = conflict
		status.Status = StatusConflict
	} else if hasLocalChanges {
		// Local content changed (detected via checksum mismatch)
		status.Status = StatusModified
	} else if remoteTest.Version > localTest.Meta.RemoteVersion {
		// Remote has newer version, no local changes
		status.Status = StatusOutdated
	} else {
		// Checksums match and versions are in sync
		status.Status = StatusSynced
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

				// Update config Tests map so subsequent operations use the new ID
				if r.config.Tests == nil {
					r.config.Tests = make(map[string]string)
				}
				r.config.Tests[name] = resp.ID

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

		// Check for local changes using checksum-based detection
		if local, ok := r.localTests[name]; ok && !force {
			if local.HasLocalChanges() {
				// Local content has been modified - don't overwrite without force
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

		// Convert tasks to blocks and strip server-generated IDs
		blocks := convertTasksToBlocks(remoteTest.Tasks)
		blocks = stripBlockIDs(blocks)

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
		if remoteTest.AppID != "" {
			// Fetch app name (gracefully handle errors - don't fail the pull)
			app, err := r.client.GetApp(ctx, remoteTest.AppID)
			if err == nil && app != nil {
				localTest.Test.Build = config.TestBuildConfig{
					Name: app.Name,
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

	// Generate diff by comparing local blocks with remote tasks.
	// Both are marshaled to JSON first to get a canonical representation,
	// since localTest.Test is a structured TestDefinition while
	// remoteTest.Tasks is a raw interface{} from the API.
	localJSON, err := json.MarshalIndent(localTest.Test.Blocks, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal local test blocks: %w", err)
	}
	remoteJSON, err := json.MarshalIndent(remoteTest.Tasks, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal remote test tasks: %w", err)
	}

	return generateSimpleDiff(string(localJSON), string(remoteJSON)), nil
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

// generateSimpleDiff generates a unified diff between local and remote content.
//
// Parameters:
//   - local: The local content
//   - remote: The remote content
//
// Returns:
//   - string: A unified diff format string showing the differences
func generateSimpleDiff(local, remote string) string {
	if local == remote {
		return ""
	}

	localLines := strings.Split(local, "\n")
	remoteLines := strings.Split(remote, "\n")

	// Build the diff output
	var diff strings.Builder
	diff.WriteString("--- local\n")
	diff.WriteString("+++ remote\n")

	// Use a simple line-by-line comparison with context
	// This is a simplified diff that shows changed, added, and removed lines
	maxLen := len(localLines)
	if len(remoteLines) > maxLen {
		maxLen = len(remoteLines)
	}

	// Track hunks of changes
	type hunk struct {
		localStart  int
		localCount  int
		remoteStart int
		remoteCount int
		lines       []string
	}

	var hunks []hunk
	var currentHunk *hunk
	contextLines := 3

	i, j := 0, 0
	for i < len(localLines) || j < len(remoteLines) {
		if i < len(localLines) && j < len(remoteLines) && localLines[i] == remoteLines[j] {
			// Lines match - context line
			if currentHunk != nil {
				currentHunk.lines = append(currentHunk.lines, " "+localLines[i])
				currentHunk.localCount++
				currentHunk.remoteCount++
			}
			i++
			j++
		} else {
			// Lines differ - start or continue a hunk
			if currentHunk == nil {
				// Start new hunk with context
				startLocal := i - contextLines
				if startLocal < 0 {
					startLocal = 0
				}
				startRemote := j - contextLines
				if startRemote < 0 {
					startRemote = 0
				}

				currentHunk = &hunk{
					localStart:  startLocal + 1, // 1-indexed
					remoteStart: startRemote + 1,
				}

				// Add leading context
				for k := startLocal; k < i; k++ {
					currentHunk.lines = append(currentHunk.lines, " "+localLines[k])
					currentHunk.localCount++
					currentHunk.remoteCount++
				}
			}

			// Find the next matching line or end
			if i < len(localLines) && (j >= len(remoteLines) || !containsLine(remoteLines[j:], localLines[i])) {
				// Line removed from local
				currentHunk.lines = append(currentHunk.lines, "-"+localLines[i])
				currentHunk.localCount++
				i++
			} else if j < len(remoteLines) {
				// Line added in remote
				currentHunk.lines = append(currentHunk.lines, "+"+remoteLines[j])
				currentHunk.remoteCount++
				j++
			}
		}

		// Check if we should close the hunk (after enough matching lines)
		if currentHunk != nil && i < len(localLines) && j < len(remoteLines) {
			matchCount := 0
			for k := 0; k < contextLines*2 && i+k < len(localLines) && j+k < len(remoteLines); k++ {
				if localLines[i+k] == remoteLines[j+k] {
					matchCount++
				} else {
					break
				}
			}
			if matchCount >= contextLines*2 {
				// Add trailing context and close hunk
				for k := 0; k < contextLines && i < len(localLines) && j < len(remoteLines); k++ {
					if localLines[i] == remoteLines[j] {
						currentHunk.lines = append(currentHunk.lines, " "+localLines[i])
						currentHunk.localCount++
						currentHunk.remoteCount++
						i++
						j++
					}
				}
				hunks = append(hunks, *currentHunk)
				currentHunk = nil
			}
		}
	}

	// Close any remaining hunk
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	// Format hunks
	for _, h := range hunks {
		diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", h.localStart, h.localCount, h.remoteStart, h.remoteCount))
		for _, line := range h.lines {
			diff.WriteString(line + "\n")
		}
	}

	return diff.String()
}

// containsLine checks if a line exists in the remaining lines.
func containsLine(lines []string, target string) bool {
	// Only look ahead a limited distance to avoid O(n^2) behavior
	lookAhead := 10
	if len(lines) < lookAhead {
		lookAhead = len(lines)
	}
	for i := 0; i < lookAhead; i++ {
		if lines[i] == target {
			return true
		}
	}
	return false
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

// stripBlockIDs removes server-generated IDs from blocks recursively.
//
// Block IDs are computed server-side from the test_id and the block's
// semantic path (position in the hierarchy). They should not be stored
// in local YAML files because:
//   - IDs are noise for users editing tests
//   - They cause merge conflicts across branches
//   - They pollute diffs with irrelevant changes
//
// Parameters:
//   - blocks: The blocks to strip IDs from
//
// Returns:
//   - []config.TestBlock: Blocks with IDs cleared
func stripBlockIDs(blocks []config.TestBlock) []config.TestBlock {
	result := make([]config.TestBlock, len(blocks))
	for i, block := range blocks {
		result[i] = block
		result[i].ID = "" // Clear the server-generated ID

		if len(block.Then) > 0 {
			result[i].Then = stripBlockIDs(block.Then)
		}
		if len(block.Else) > 0 {
			result[i].Else = stripBlockIDs(block.Else)
		}
		if len(block.Body) > 0 {
			result[i].Body = stripBlockIDs(block.Body)
		}
	}
	return result
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
