package hotreload

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileWatcher_DetectsDartFileChange(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "lib")
	os.MkdirAll(libDir, 0755)
	os.WriteFile(filepath.Join(libDir, "main.dart"), []byte("void main() {}"), 0644)

	cfg := FileWatcherConfig{
		Extensions:  []string{".dart"},
		ExcludeDirs: []string{"build", ".git"},
		Debounce:    200 * time.Millisecond,
	}

	fw, err := NewFileWatcher(dir, cfg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := fw.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer fw.Stop()

	// Give watcher time to register
	time.Sleep(100 * time.Millisecond)

	// Modify a .dart file
	os.WriteFile(filepath.Join(libDir, "main.dart"), []byte("void main() { print('hi'); }"), 0644)

	select {
	case event := <-fw.Changes():
		if len(event.Files) == 0 {
			t.Fatal("expected at least one changed file")
		}
		found := false
		for _, f := range event.Files {
			if filepath.Base(f) == "main.dart" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected main.dart in changed files, got %v", event.Files)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for file change event")
	}
}

func TestFileWatcher_IgnoresExcludedDirs(t *testing.T) {
	dir := t.TempDir()
	buildDir := filepath.Join(dir, "build")
	os.MkdirAll(buildDir, 0755)
	libDir := filepath.Join(dir, "lib")
	os.MkdirAll(libDir, 0755)

	cfg := FileWatcherConfig{
		Extensions:  []string{".dart"},
		ExcludeDirs: []string{"build"},
		Debounce:    100 * time.Millisecond,
	}

	fw, err := NewFileWatcher(dir, cfg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := fw.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer fw.Stop()

	time.Sleep(100 * time.Millisecond)

	// Write to excluded build/ dir -- should NOT trigger
	os.WriteFile(filepath.Join(buildDir, "output.dart"), []byte("// build"), 0644)

	select {
	case event := <-fw.Changes():
		t.Fatalf("should not have received event for excluded dir, got: %v", event.Files)
	case <-time.After(500 * time.Millisecond):
		// Expected: no event
	}
}

func TestFileWatcher_IgnoresNonMatchingExtensions(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0644)

	cfg := FileWatcherConfig{
		Extensions:  []string{".dart"},
		ExcludeDirs: []string{},
		Debounce:    100 * time.Millisecond,
	}

	fw, err := NewFileWatcher(dir, cfg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := fw.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer fw.Stop()

	time.Sleep(100 * time.Millisecond)

	// Write a .md file -- should NOT trigger
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# updated"), 0644)

	select {
	case event := <-fw.Changes():
		t.Fatalf("should not have received event for .md file, got: %v", event.Files)
	case <-time.After(500 * time.Millisecond):
		// Expected: no event
	}
}

func TestFileWatcher_DebouncesBatchedChanges(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "lib")
	os.MkdirAll(libDir, 0755)
	os.WriteFile(filepath.Join(libDir, "a.dart"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(libDir, "b.dart"), []byte("b"), 0644)

	cfg := FileWatcherConfig{
		Extensions:  []string{".dart"},
		ExcludeDirs: []string{},
		Debounce:    300 * time.Millisecond,
	}

	fw, err := NewFileWatcher(dir, cfg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := fw.Start(ctx); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer fw.Stop()

	time.Sleep(100 * time.Millisecond)

	// Rapid-fire edits to two files
	os.WriteFile(filepath.Join(libDir, "a.dart"), []byte("a updated"), 0644)
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(filepath.Join(libDir, "b.dart"), []byte("b updated"), 0644)

	select {
	case event := <-fw.Changes():
		if len(event.Files) < 2 {
			t.Fatalf("expected at least 2 files in debounced batch, got %d: %v", len(event.Files), event.Files)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for debounced change event")
	}
}

func TestFlutterWatchConfig_HasExpectedDefaults(t *testing.T) {
	cfg := FlutterWatchConfig()

	if len(cfg.Extensions) == 0 {
		t.Fatal("expected non-empty extensions")
	}

	hasDart := false
	for _, ext := range cfg.Extensions {
		if ext == ".dart" {
			hasDart = true
		}
	}
	if !hasDart {
		t.Fatalf("expected .dart in extensions, got %v", cfg.Extensions)
	}

	if cfg.Debounce != 800*time.Millisecond {
		t.Fatalf("expected 800ms debounce, got %v", cfg.Debounce)
	}

	hasBuildExclude := false
	for _, dir := range cfg.ExcludeDirs {
		if dir == "build" {
			hasBuildExclude = true
		}
	}
	if !hasBuildExclude {
		t.Fatalf("expected 'build' in excludes, got %v", cfg.ExcludeDirs)
	}
}
