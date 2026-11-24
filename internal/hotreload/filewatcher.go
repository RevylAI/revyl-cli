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

type FileWatcherConfig struct {
	Extensions  []string
	ExcludeDirs []string
	// Debounce is the quiet period after the last event before emitting a batch (default 800ms).
	Debounce time.Duration
}

type FileChangeEvent struct {
	Files     []string
	Timestamp time.Time
}

type FileWatcher struct {
	rootDir  string
	config   FileWatcherConfig
	watcher  *fsnotify.Watcher
	onChange chan FileChangeEvent
	done     chan struct{}
	mu       sync.Mutex
	running  bool
}

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

func (fw *FileWatcher) Changes() <-chan FileChangeEvent {
	return fw.onChange
}

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

func (fw *FileWatcher) isExcludedDir(name string) bool {
	for _, excluded := range fw.config.ExcludeDirs {
		if strings.EqualFold(name, excluded) {
			return true
		}
	}
	return false
}

func (fw *FileWatcher) matchesExtension(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, watchExt := range fw.config.Extensions {
		if strings.EqualFold(ext, watchExt) {
			return true
		}
	}
	return false
}

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

func (fw *FileWatcher) isRelevantEvent(event fsnotify.Event) bool {
	return event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove)
}

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
