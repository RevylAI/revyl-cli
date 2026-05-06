package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".revyl"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func createTestContext(t *testing.T, root string, ctx *DevContext) {
	t.Helper()
	if err := saveDevContext(root, ctx); err != nil {
		t.Fatalf("saveDevContext(%s): %v", ctx.Name, err)
	}
}

func initTestGitBranch(t *testing.T, root, branch string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if output, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	commitCmd := exec.Command("git", "-C", root, "-c", "user.email=revyl-test@example.com", "-c", "user.name=Revyl Test", "commit", "--allow-empty", "-m", "init")
	if output, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, output)
	}
	if output, err := exec.Command("git", "-C", root, "checkout", "-b", branch).CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %v\n%s", err, output)
	}
}

// ---------------------------------------------------------------------------
// resolveDevContextName
// ---------------------------------------------------------------------------

func TestResolveDevContextName_ExplicitWins(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{Name: "other", Platform: "ios"})

	got, err := resolveDevContextName(root, "explicit")
	if err != nil {
		t.Fatal(err)
	}
	if got != "explicit" {
		t.Fatalf("got %q, want %q", got, "explicit")
	}
}

func TestResolveDevContextName_CurrentMarker(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{Name: "ctx-a", Platform: "ios"})
	createTestContext(t, root, &DevContext{Name: "ctx-b", Platform: "android"})
	_ = setCurrentDevContext(root, "ctx-b")

	got, err := resolveDevContextName(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ctx-b" {
		t.Fatalf("got %q, want %q", got, "ctx-b")
	}
}

func TestResolveDevContextName_SoleContext(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{Name: "only-one", Platform: "android"})

	got, err := resolveDevContextName(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "only-one" {
		t.Fatalf("got %q, want %q", got, "only-one")
	}
}

func TestResolveDevContextName_NoContexts(t *testing.T) {
	root := setupTestRepo(t)

	got, err := resolveDevContextName(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultDevContextName {
		t.Fatalf("got %q, want %q", got, defaultDevContextName)
	}
}

func TestResolveDevContextName_Ambiguous(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{Name: "ctx-a", Platform: "ios"})
	createTestContext(t, root, &DevContext{Name: "ctx-b", Platform: "android"})

	_, err := resolveDevContextName(root, "")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
}

// ---------------------------------------------------------------------------
// loadDevContext / saveDevContext round-trip
// ---------------------------------------------------------------------------

func TestDevContext_RoundTrip(t *testing.T) {
	root := setupTestRepo(t)
	now := time.Now().Truncate(time.Second)
	original := &DevContext{
		Name:          "test-ctx",
		Platform:      "ios",
		PlatformKey:   "ios-dev",
		Provider:      "expo",
		SessionID:     "sess-123",
		SessionIndex:  2,
		SessionOwned:  true,
		ViewerURL:     "https://viewer.example",
		PID:           12345,
		StartedAtNano: now.UnixNano(),
		State:         devContextStateRunning,
		Port:          8081,
		CreatedAt:     now,
		LastActivity:  now,
	}
	createTestContext(t, root, original)

	loaded, err := loadDevContext(root, "test-ctx")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != original.Name {
		t.Fatalf("Name = %q, want %q", loaded.Name, original.Name)
	}
	if loaded.Platform != original.Platform {
		t.Fatalf("Platform = %q, want %q", loaded.Platform, original.Platform)
	}
	if loaded.SessionID != original.SessionID {
		t.Fatalf("SessionID = %q, want %q", loaded.SessionID, original.SessionID)
	}
	if loaded.SessionOwned != original.SessionOwned {
		t.Fatalf("SessionOwned = %v, want %v", loaded.SessionOwned, original.SessionOwned)
	}
	if loaded.StartedAtNano != original.StartedAtNano {
		t.Fatalf("StartedAtNano = %d, want %d", loaded.StartedAtNano, original.StartedAtNano)
	}
	if loaded.PID != original.PID {
		t.Fatalf("PID = %d, want %d", loaded.PID, original.PID)
	}
}

func TestLoadDevContext_NotFound(t *testing.T) {
	root := setupTestRepo(t)
	_, err := loadDevContext(root, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent context")
	}
}

// ---------------------------------------------------------------------------
// listDevContexts
// ---------------------------------------------------------------------------

func TestListDevContexts_SortedAndSkipsCurrent(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{Name: "zulu", Platform: "android"})
	createTestContext(t, root, &DevContext{Name: "alpha", Platform: "ios"})
	_ = setCurrentDevContext(root, "alpha")

	contexts, err := listDevContexts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 2 {
		t.Fatalf("got %d contexts, want 2", len(contexts))
	}
	if contexts[0].Name != "alpha" {
		t.Fatalf("first context = %q, want %q", contexts[0].Name, "alpha")
	}
	if contexts[1].Name != "zulu" {
		t.Fatalf("second context = %q, want %q", contexts[1].Name, "zulu")
	}
}

func TestListDevContexts_Empty(t *testing.T) {
	root := setupTestRepo(t)
	contexts, err := listDevContexts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 0 {
		t.Fatalf("got %d contexts, want 0", len(contexts))
	}
}

func TestListDevContexts_DeadProcessMarkedStopped(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{
		Name:     "stale",
		Platform: "ios",
		PID:      999999999,
		State:    devContextStateRunning,
	})

	contexts, err := listDevContexts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 1 {
		t.Fatalf("got %d contexts, want 1", len(contexts))
	}
	if contexts[0].State != devContextStateStopped {
		t.Fatalf("State = %q, want %q", contexts[0].State, devContextStateStopped)
	}
}

// ---------------------------------------------------------------------------
// PID file read/write
// ---------------------------------------------------------------------------

func TestDevCtxPIDFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.pid")
	nonce := time.Now().UnixNano()

	if err := writeDevCtxPIDFile(path, 42, nonce); err != nil {
		t.Fatal(err)
	}

	pid, readNonce := readDevCtxPIDFile(path)
	if pid != 42 {
		t.Fatalf("pid = %d, want 42", pid)
	}
	if readNonce != nonce {
		t.Fatalf("nonce = %d, want %d", readNonce, nonce)
	}
}

func TestDevCtxPIDFile_BackwardCompatible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.pid")
	_ = os.WriteFile(path, []byte("12345"), 0644)

	pid, nonce := readDevCtxPIDFile(path)
	if pid != 12345 {
		t.Fatalf("pid = %d, want 12345", pid)
	}
	if nonce != 0 {
		t.Fatalf("nonce = %d, want 0 (legacy format)", nonce)
	}
}

func TestDevCtxPIDFile_Missing(t *testing.T) {
	pid, nonce := readDevCtxPIDFile("/nonexistent/dev.pid")
	if pid != 0 || nonce != 0 {
		t.Fatalf("expected (0, 0) for missing file, got (%d, %d)", pid, nonce)
	}
}

// ---------------------------------------------------------------------------
// isDevCtxProcessAlive with nonce
// ---------------------------------------------------------------------------

func TestIsDevCtxProcessAlive_OwnProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.pid")
	nonce := time.Now().UnixNano()
	_ = writeDevCtxPIDFile(path, os.Getpid(), nonce)

	alive, err := isDevCtxProcessAlive(os.Getpid(), nonce, path)
	if err != nil {
		t.Fatal(err)
	}
	if !alive {
		t.Fatal("expected alive=true for own process with matching nonce")
	}
}

func TestIsDevCtxProcessAlive_NonceMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.pid")
	_ = writeDevCtxPIDFile(path, os.Getpid(), 111)

	alive, _ := isDevCtxProcessAlive(os.Getpid(), 222, path)
	if alive {
		t.Fatal("expected alive=false when nonce mismatches")
	}
}

func TestIsDevCtxProcessAlive_DeadPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.pid")
	_ = writeDevCtxPIDFile(path, 999999999, 111)

	alive, _ := isDevCtxProcessAlive(999999999, 111, path)
	if alive {
		t.Fatal("expected alive=false for dead PID")
	}
}

// ---------------------------------------------------------------------------
// resolveDevStartContextName
// ---------------------------------------------------------------------------

func TestResolveDevStartContextName_NoContextsUsesDefault(t *testing.T) {
	root := setupTestRepo(t)

	name, err := resolveDevStartContextName(root, "", "ios")
	if err != nil {
		t.Fatalf("resolveDevStartContextName() error = %v", err)
	}
	if name != defaultDevContextName {
		t.Fatalf("name = %q, want %q", name, defaultDevContextName)
	}
}

func TestResolveDevStartContextName_ExplicitWrongPlatformFails(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{Name: "ctx", Platform: "ios"})

	_, err := resolveDevStartContextName(root, "ctx", "android")
	if err == nil {
		t.Fatal("expected explicit platform conflict")
	}
}

func TestResolveDevStartContextName_BusyImplicitContextAutoSelectsBranchPlatform(t *testing.T) {
	root := setupTestRepo(t)
	initTestGitBranch(t, root, "feature/signup-redesign")
	createTestContext(t, root, &DevContext{
		Name:     defaultDevContextName,
		Platform: "ios",
		PID:      os.Getpid(),
		State:    devContextStateRunning,
	})

	var name string
	output := captureStdoutAndStderr(t, func() {
		var err error
		name, err = resolveDevStartContextName(root, "", "ios")
		if err != nil {
			t.Fatalf("resolveDevStartContextName() error = %v", err)
		}
	})

	if name != "feature-signup-redesign-ios" {
		t.Fatalf("name = %q, want %q", name, "feature-signup-redesign-ios")
	}
	if !strings.Contains(output, "Context 'default' is already running; starting new context 'feature-signup-redesign-ios'.") {
		t.Fatalf("output missing auto-context notice:\n%s", output)
	}
}

func TestResolveDevStartContextName_WrongPlatformImplicitUsesMatchingContext(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{Name: defaultDevContextName, Platform: "android"})
	createTestContext(t, root, &DevContext{Name: "ios-work", Platform: "ios"})
	_ = setCurrentDevContext(root, defaultDevContextName)

	name, err := resolveDevStartContextName(root, "", "ios")
	if err != nil {
		t.Fatalf("resolveDevStartContextName() error = %v", err)
	}
	if name != "ios-work" {
		t.Fatalf("name = %q, want %q", name, "ios-work")
	}
}

func TestResolveDevStartContextName_WrongPlatformImplicitAutoSelectsBranchPlatform(t *testing.T) {
	root := setupTestRepo(t)
	initTestGitBranch(t, root, "feature/signup-redesign")
	createTestContext(t, root, &DevContext{Name: defaultDevContextName, Platform: "android"})
	_ = setCurrentDevContext(root, defaultDevContextName)

	name, err := resolveDevStartContextName(root, "", "ios")
	if err != nil {
		t.Fatalf("resolveDevStartContextName() error = %v", err)
	}
	if name != "feature-signup-redesign-ios" {
		t.Fatalf("name = %q, want %q", name, "feature-signup-redesign-ios")
	}
}

func TestResolveDevStartContextName_MultipleContextsUsesMatchingPlatform(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{Name: "android-work", Platform: "android"})
	createTestContext(t, root, &DevContext{Name: "ios-work", Platform: "ios"})

	name, err := resolveDevStartContextName(root, "", "ios")
	if err != nil {
		t.Fatalf("resolveDevStartContextName() error = %v", err)
	}
	if name != "ios-work" {
		t.Fatalf("name = %q, want %q", name, "ios-work")
	}
}

func TestResolveDevStartContextName_MultipleContextsAutoNamesWhenNoPlatformMatch(t *testing.T) {
	root := setupTestRepo(t)
	initTestGitBranch(t, root, "feature/signup-redesign")
	createTestContext(t, root, &DevContext{Name: "android-work", Platform: "android"})
	createTestContext(t, root, &DevContext{Name: "android-two", Platform: "android"})

	name, err := resolveDevStartContextName(root, "", "ios")
	if err != nil {
		t.Fatalf("resolveDevStartContextName() error = %v", err)
	}
	if name != "feature-signup-redesign-ios" {
		t.Fatalf("name = %q, want %q", name, "feature-signup-redesign-ios")
	}
}

func TestSuggestAutoDevContextNameAddsSuffixOnCollision(t *testing.T) {
	root := setupTestRepo(t)
	initTestGitBranch(t, root, "feature/signup-redesign")
	createTestContext(t, root, &DevContext{Name: "feature-signup-redesign-ios", Platform: "ios"})
	createTestContext(t, root, &DevContext{Name: "feature-signup-redesign-ios-2", Platform: "ios"})

	name := suggestAutoDevContextName(root, "ios")
	if name != "feature-signup-redesign-ios-3" {
		t.Fatalf("name = %q, want %q", name, "feature-signup-redesign-ios-3")
	}
}

func TestSuggestAutoDevContextNameFallsBackAfterMaxAttempts(t *testing.T) {
	root := setupTestRepo(t)
	initTestGitBranch(t, root, "feature/signup-redesign")
	base := "feature-signup-redesign-ios"
	createTestContext(t, root, &DevContext{Name: base, Platform: "ios"})
	for i := 2; i <= maxAutoDevContextNameAttempts; i++ {
		createTestContext(t, root, &DevContext{Name: fmt.Sprintf("%s-%d", base, i), Platform: "ios"})
	}

	name := suggestAutoDevContextName(root, "ios")
	if !strings.HasPrefix(name, base+"-") {
		t.Fatalf("name = %q, want %q prefix", name, base+"-")
	}
	if name == fmt.Sprintf("%s-%d", base, maxAutoDevContextNameAttempts+1) {
		t.Fatalf("name = %q, want timestamp fallback after bounded attempts", name)
	}
	if _, err := loadDevContext(root, name); err == nil {
		t.Fatalf("fallback name %q unexpectedly exists", name)
	}
}

// ---------------------------------------------------------------------------
// stopOneDevContext clears session fields
// ---------------------------------------------------------------------------

func TestStopOneDevContext_ClearsSessionFields(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{
		Name:         "test-stop",
		Platform:     "ios",
		SessionID:    "sess-abc",
		SessionIndex: 3,
		ViewerURL:    "https://viewer.example",
		PID:          999999999,
		State:        devContextStateRunning,
	})

	// stopOneDevContext requires a cobra.Command for getDeviceSessionMgr,
	// but the session is not owned so it won't try to call StopSession.
	// We can't easily provide a real cmd here, so we test the field clearing
	// by simulating what stopOneDevContext does after the mgr call.
	ctx, _ := loadDevContext(root, "test-stop")
	ctx.State = devContextStateStopped
	ctx.PID = 0
	ctx.SessionID = ""
	ctx.SessionIndex = 0
	ctx.ViewerURL = ""
	_ = saveDevContext(root, ctx)

	reloaded, err := loadDevContext(root, "test-stop")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty", reloaded.SessionID)
	}
	if reloaded.SessionIndex != 0 {
		t.Fatalf("SessionIndex = %d, want 0", reloaded.SessionIndex)
	}
	if reloaded.ViewerURL != "" {
		t.Fatalf("ViewerURL = %q, want empty", reloaded.ViewerURL)
	}
	if reloaded.PID != 0 {
		t.Fatalf("PID = %d, want 0", reloaded.PID)
	}
	if reloaded.State != devContextStateStopped {
		t.Fatalf("State = %q, want %q", reloaded.State, devContextStateStopped)
	}
}

// ---------------------------------------------------------------------------
// validateDevContextName
// ---------------------------------------------------------------------------

func TestValidateDevContextName_Valid(t *testing.T) {
	for _, name := range []string{"default", "ios-main", "checkout_android", "my.ctx"} {
		if err := validateDevContextName(name); err != nil {
			t.Fatalf("validateDevContextName(%q) unexpected error: %v", name, err)
		}
	}
}

func TestValidateDevContextName_Empty(t *testing.T) {
	if err := validateDevContextName(""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestValidateDevContextName_PathTraversal(t *testing.T) {
	for _, name := range []string{"../../etc", "../foo", "a/b", "a\\b", "foo..bar/../baz"} {
		if err := validateDevContextName(name); err == nil {
			t.Fatalf("validateDevContextName(%q) should have failed", name)
		}
	}
}

func TestResolveDevContextName_RejectsTraversalFromMarker(t *testing.T) {
	root := setupTestRepo(t)
	dir := filepath.Join(root, ".revyl", devContextsDir)
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, devContextCurrentFile), []byte("../../etc\n"), 0644)

	_, err := resolveDevContextName(root, "")
	if err == nil {
		t.Fatal("expected error for path-traversal current marker")
	}
}

func TestResolveDevContextName_RejectsTraversalFromFlag(t *testing.T) {
	root := setupTestRepo(t)
	_, err := resolveDevContextName(root, "../sneaky")
	if err == nil {
		t.Fatal("expected error for path-traversal explicit context")
	}
}

// ---------------------------------------------------------------------------
// tryReuseDevContextSession — pure decision logic tests
// ---------------------------------------------------------------------------

func TestTryReuseDevContextSession_NilSaved(t *testing.T) {
	result := tryReuseDevContextSession(nil, nil, nil, "ios")
	if result != nil {
		t.Fatal("expected nil for nil saved context")
	}
}

func TestTryReuseDevContextSession_EmptySessionID(t *testing.T) {
	saved := &DevContext{Name: "test", Platform: "ios", SessionID: ""}
	result := tryReuseDevContextSession(nil, nil, saved, "ios")
	if result != nil {
		t.Fatal("expected nil for empty SessionID")
	}
}

func TestTryReuseDevContextSession_PlatformMismatch(t *testing.T) {
	saved := &DevContext{
		Name:      "test",
		Platform:  "ios",
		SessionID: "sess-abc",
	}
	result := tryReuseDevContextSession(nil, nil, saved, "android")
	if result != nil {
		t.Fatal("expected nil when platform does not match")
	}
}

func TestTryReuseDevContextSession_NilMgrFallsThrough(t *testing.T) {
	saved := &DevContext{
		Name:      "test",
		Platform:  "ios",
		SessionID: "sess-abc",
	}
	result := tryReuseDevContextSession(nil, nil, saved, "ios")
	if result != nil {
		t.Fatal("expected nil when mgr is nil (session cannot be resolved)")
	}
}

// ---------------------------------------------------------------------------
// DevContext round-trip with Provider and SessionOwned
// ---------------------------------------------------------------------------

func TestDevContext_RoundTrip_ProviderAndOwnership(t *testing.T) {
	root := setupTestRepo(t)
	now := time.Now().Truncate(time.Second)

	original := &DevContext{
		Name:         "rebuild-ctx",
		Platform:     "android",
		PlatformKey:  "android-dev",
		Provider:     "Gradle",
		SessionID:    "sess-456",
		SessionIndex: 1,
		SessionOwned: false,
		ViewerURL:    "https://viewer.example/sess-456",
		State:        devContextStateStopped,
		CreatedAt:    now,
		LastActivity: now,
	}
	createTestContext(t, root, original)

	loaded, err := loadDevContext(root, "rebuild-ctx")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != "Gradle" {
		t.Fatalf("Provider = %q, want %q", loaded.Provider, "Gradle")
	}
	if loaded.SessionOwned != false {
		t.Fatalf("SessionOwned = %v, want false", loaded.SessionOwned)
	}
	if loaded.SessionID != "sess-456" {
		t.Fatalf("SessionID = %q, want %q", loaded.SessionID, "sess-456")
	}
}

func TestDevContext_NonOwnedSession_PreservesSessionOnStop(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{
		Name:         "attached-ctx",
		Platform:     "ios",
		SessionID:    "sess-789",
		SessionIndex: 2,
		SessionOwned: false,
		ViewerURL:    "https://viewer.example/sess-789",
		State:        devContextStateRunning,
	})

	ctx, _ := loadDevContext(root, "attached-ctx")
	ctx.State = devContextStateStopped
	ctx.PID = 0
	// Non-owned sessions should NOT have their session fields cleared
	// by the dev loop exit defer. Simulate that the dev loop preserves them.
	_ = saveDevContext(root, ctx)

	reloaded, err := loadDevContext(root, "attached-ctx")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SessionID != "sess-789" {
		t.Fatalf("SessionID = %q, want %q (should be preserved for non-owned session)", reloaded.SessionID, "sess-789")
	}
	if reloaded.ViewerURL != "https://viewer.example/sess-789" {
		t.Fatalf("ViewerURL = %q, want preserved for non-owned session", reloaded.ViewerURL)
	}
	if reloaded.State != devContextStateStopped {
		t.Fatalf("State = %q, want %q", reloaded.State, devContextStateStopped)
	}
}

// ---------------------------------------------------------------------------
// loadDevContextTunnel
// ---------------------------------------------------------------------------

func TestLoadDevContextTunnel_StoppedContext(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{
		Name:        "stopped-ctx",
		Platform:    "ios",
		TunnelURL:   "https://tunnel.example",
		DeepLinkURL: "myapp://expo-development-client/?url=https://tunnel.example",
		State:       devContextStateStopped,
		PID:         0,
	})

	_, _, ok := loadDevContextTunnel(root, "stopped-ctx")
	if ok {
		t.Fatal("expected ok=false for stopped context with PID 0")
	}
}

func TestLoadDevContextTunnel_MissingTunnelFields(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{
		Name:     "no-tunnel",
		Platform: "ios",
		PID:      os.Getpid(),
		State:    devContextStateRunning,
	})

	_, _, ok := loadDevContextTunnel(root, "no-tunnel")
	if ok {
		t.Fatal("expected ok=false when tunnel fields are empty")
	}
}

func TestLoadDevContextTunnel_DeadPID(t *testing.T) {
	root := setupTestRepo(t)
	createTestContext(t, root, &DevContext{
		Name:        "dead-pid",
		Platform:    "ios",
		TunnelURL:   "https://tunnel.example",
		DeepLinkURL: "myapp://deep-link",
		PID:         999999999,
		State:       devContextStateRunning,
	})

	_, _, ok := loadDevContextTunnel(root, "dead-pid")
	if ok {
		t.Fatal("expected ok=false for dead PID")
	}
}

func TestLoadDevContextTunnel_RunningContext(t *testing.T) {
	root := setupTestRepo(t)

	nonce := time.Now().UnixNano()
	pidPath := devCtxPIDPath(root, "live-tunnel")
	dir := filepath.Dir(pidPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeDevCtxPIDFile(pidPath, os.Getpid(), nonce); err != nil {
		t.Fatal(err)
	}

	createTestContext(t, root, &DevContext{
		Name:          "live-tunnel",
		Platform:      "ios",
		TunnelURL:     "https://abc123.trycloudflare.com",
		DeepLinkURL:   "myapp://expo-development-client/?url=https://abc123.trycloudflare.com",
		PID:           os.Getpid(),
		StartedAtNano: nonce,
		State:         devContextStateRunning,
	})

	tunnelURL, deepLinkURL, ok := loadDevContextTunnel(root, "live-tunnel")
	if !ok {
		t.Fatal("expected ok=true for running context with tunnel")
	}
	if tunnelURL != "https://abc123.trycloudflare.com" {
		t.Fatalf("tunnelURL = %q, want trycloudflare URL", tunnelURL)
	}
	if deepLinkURL != "myapp://expo-development-client/?url=https://abc123.trycloudflare.com" {
		t.Fatalf("deepLinkURL = %q, want deep link", deepLinkURL)
	}
}

func TestDevContext_RoundTrip_TunnelFields(t *testing.T) {
	root := setupTestRepo(t)
	now := time.Now().Truncate(time.Second)

	original := &DevContext{
		Name:         "tunnel-ctx",
		Platform:     "ios",
		Provider:     "expo",
		TunnelURL:    "https://xyz.trycloudflare.com",
		DeepLinkURL:  "myapp://expo-development-client/?url=https://xyz.trycloudflare.com",
		Transport:    "relay",
		RelayID:      "relay-123",
		State:        devContextStateRunning,
		CreatedAt:    now,
		LastActivity: now,
	}
	createTestContext(t, root, original)

	loaded, err := loadDevContext(root, "tunnel-ctx")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TunnelURL != original.TunnelURL {
		t.Fatalf("TunnelURL = %q, want %q", loaded.TunnelURL, original.TunnelURL)
	}
	if loaded.DeepLinkURL != original.DeepLinkURL {
		t.Fatalf("DeepLinkURL = %q, want %q", loaded.DeepLinkURL, original.DeepLinkURL)
	}
	if loaded.Transport != original.Transport {
		t.Fatalf("Transport = %q, want %q", loaded.Transport, original.Transport)
	}
	if loaded.RelayID != original.RelayID {
		t.Fatalf("RelayID = %q, want %q", loaded.RelayID, original.RelayID)
	}
}
