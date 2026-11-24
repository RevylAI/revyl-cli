// Package hotreload provides hot reload functionality for rapid development iteration.
//
// This file implements a recursive file watcher with debounce support for
// triggering automatic rebuilds when source files change. Used by the
// rebuild-based dev loop (Flutter, native iOS/Android).
package hotreload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcherConfig configures which files trigger rebuild notifications.
type FileWatcherConfig struct {
	// Extensions lists file extensions to watch (e.g. ".dart", ".yaml").
	// Matched case-insensitively against changed file paths.
	Extensions []string

	// ExcludeDirs lists directory names to skip during recursive traversal
	// (e.g. "build", ".dart_tool", ".git").
	ExcludeDirs []string

	// Debounce is the quiet period after the last file event before firing
	// the onChange callback. Prevents rapid-fire rebuilds from multi-file saves.
	// Default: 800ms.
	Debounce time.Duration
}

// FileChangeEvent describes one or more files that changed within a debounce window.
type FileChangeEvent struct {
	// Files is the deduplicated list of changed file paths (relative to the watched root).
	Files []string

	// Timestamp is when the debounce window closed.
	Timestamp time.Time
}

// FileWatcher recursively watches a directory tree for file changes matching
// configured extensions, debounces rapid events, and sends change notifications
// on a channel.
type FileWatcher struct {
	rootDir  string
	config   FileWatcherConfig
	watcher  *fsnotify.Watcher
	onChange chan FileChangeEvent
	done     chan struct{}
	mu       sync.Mutex
	running  bool
}

// NewFileWatcher creates a file watcher for the given directory.
//
// Parameters:
//   - rootDir: The root directory to watch recursively
//   - cfg: Configuration for extensions, excludes, and debounce timing
//
// Returns:
//   - *FileWatcher: A new file watcher (call Start to begin watching)
//   - error: Any error creating the underlying fsnotify watcher
func NewFileWatcher(rootDir string, cfg FileWatcherConfig) (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if cfg.Debounce <= 0 {
		cfg.Debounce = 800 * time.Millisecond
	}

	return &FileWatcher{
		rootDir:  rootDir,
		config:   cfg,
		watcher:  watcher,
		onChange: make(chan FileChangeEvent, 1),
		done:     make(chan struct{}),
	}, nil
}

// Changes returns the channel that receives debounced file change events.
// Read from this channel to trigger rebuilds.
//
// Returns:
//   - <-chan FileChangeEvent: Read-only channel of change events
func (fw *FileWatcher) Changes() <-chan FileChangeEvent {
	return fw.onChange
}

// Start begins watching for file changes. It recursively adds all matching
// directories under rootDir and starts the event processing goroutine.
// Blocks until the initial directory walk is complete.
//
// Parameters:
//   - ctx: Context for cancellation; when cancelled, the watcher stops
//
// Returns:
//   - error: Any error during initial directory traversal
func (fw *FileWatcher) Start(ctx context.Context) error {
	fw.mu.Lock()
	if fw.running {
		fw.mu.Unlock()
		return nil
	}
	fw.running = true
	fw.mu.Unlock()

	if err := fw.addDirsRecursive(fw.rootDir); err != nil {
		return err
	}

	go fw.eventLoop(ctx)
	return nil
}

// Stop terminates the file watcher and closes the changes channel.
// Safe to call multiple times.
func (fw *FileWatcher) Stop() {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if !fw.running {
		return
	}
	fw.running = false
	fw.watcher.Close()
	close(fw.done)
}

// addDirsRecursive walks the directory tree and adds each qualifying directory
// to the fsnotify watcher. Skips directories matching ExcludeDirs.
func (fw *FileWatcher) addDirsRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		if fw.isExcludedDir(name) {
			return filepath.SkipDir
		}

		return fw.watcher.Add(path)
	})
}

// isExcludedDir checks if a directory name should be skipped.
func (fw *FileWatcher) isExcludedDir(name string) bool {
	for _, excluded := range fw.config.ExcludeDirs {
		if strings.EqualFold(name, excluded) {
			return true
		}
	}
	return false
}

// matchesExtension checks if a file path has one of the watched extensions.
func (fw *FileWatcher) matchesExtension(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, watchExt := range fw.config.Extensions {
		if strings.EqualFold(ext, watchExt) {
			return true
		}
	}
	return false
}

// eventLoop processes fsnotify events, debounces them, and sends aggregated
// change notifications. Runs until ctx is cancelled or Stop is called.
func (fw *FileWatcher) eventLoop(ctx context.Context) {
	var debounceTimer *time.Timer
	pendingFiles := make(map[string]struct{})

	defer func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		close(fw.onChange)
	}()

	var timerCh <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-fw.done:
			return

		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			if !fw.isRelevantEvent(event) {
				continue
			}

			if event.Has(fsnotify.Create) {
				fw.handleNewDir(event.Name)
			}

			if !fw.matchesExtension(event.Name) {
				continue
			}

			relPath, err := filepath.Rel(fw.rootDir, event.Name)
			if err != nil {
				relPath = event.Name
			}
			pendingFiles[relPath] = struct{}{}

			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.NewTimer(fw.config.Debounce)
			timerCh = debounceTimer.C

		case <-timerCh:
			timerCh = nil
			if len(pendingFiles) == 0 {
				continue
			}

			files := make([]string, 0, len(pendingFiles))
			for f := range pendingFiles {
				files = append(files, f)
			}
			pendingFiles = make(map[string]struct{})

			select {
			case fw.onChange <- FileChangeEvent{Files: files, Timestamp: time.Now()}:
			default:
			}

		case _, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// isRelevantEvent filters fsnotify events to only Create, Write, and Remove.
func (fw *FileWatcher) isRelevantEvent(event fsnotify.Event) bool {
	return event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove)
}

// handleNewDir adds newly created directories to the watcher so new
// subdirectories are tracked without restarting.
func (fw *FileWatcher) handleNewDir(path string) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}
	if fw.isExcludedDir(filepath.Base(path)) {
		return
	}
	_ = fw.watcher.Add(path)
}

// FlutterWatchConfig returns a FileWatcherConfig tuned for Flutter projects.
//
// Watches: .dart, .yaml (pubspec changes)
// Excludes: build, .dart_tool, .git, .idea, .vscode, ios, android
//
// Returns:
//   - FileWatcherConfig: Configuration for Flutter file watching
func FlutterWatchConfig() FileWatcherConfig {
	return FileWatcherConfig{
		Extensions: []string{".dart", ".yaml"},
		ExcludeDirs: []string{
			"build",
			".dart_tool",
			".git",
			".idea",
			".vscode",
			"ios",
			"android",
			"web",
			"linux",
			"macos",
			"windows",
			".revyl",
		},
		Debounce: 800 * time.Millisecond,
	}
}
