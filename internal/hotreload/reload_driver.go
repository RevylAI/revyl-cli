package hotreload

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// ReloadAction is the response a set of changed files requires from an
// attach-based dev loop.
type ReloadAction int

const (
	// ReloadActionNone means the change does not require any action.
	ReloadActionNone ReloadAction = iota

	// ReloadActionReload means a hot reload is sufficient (most .dart edits).
	ReloadActionReload

	// ReloadActionRestart means a hot restart is required (state is lost).
	ReloadActionRestart

	// ReloadActionRebuild means a full rebuild + reinstall is required
	// (pubspec / native changes); the attach session cannot apply it.
	ReloadActionRebuild
)

// String returns a human-readable label for the action.
func (a ReloadAction) String() string {
	switch a {
	case ReloadActionReload:
		return "reload"
	case ReloadActionRestart:
		return "restart"
	case ReloadActionRebuild:
		return "rebuild"
	default:
		return "none"
	}
}

// ClassifyFlutterChange decides what a set of changed files requires for a
// Flutter attach dev loop.
//
//   - pubspec.yaml or native directories (ios/android/macos/windows/linux) ->
//     rebuild, because the attach session cannot apply native/dependency changes.
//   - any .dart file -> hot reload.
//   - otherwise -> none.
//
// Hot restart is not auto-classified from filenames (the cases that need it —
// changes to main()/global state — are not reliably detectable from a path); it
// is offered as a manual action instead.
func ClassifyFlutterChange(files []string) ReloadAction {
	action := ReloadActionNone
	for _, f := range files {
		lower := strings.ToLower(filepath.ToSlash(f))
		base := strings.ToLower(filepath.Base(f))

		if base == "pubspec.yaml" || base == "pubspec.lock" {
			return ReloadActionRebuild
		}
		if isNativeDirPath(lower) {
			return ReloadActionRebuild
		}
		if strings.HasSuffix(base, ".dart") {
			action = ReloadActionReload
		}
	}
	return action
}

func isNativeDirPath(slashPath string) bool {
	for _, dir := range []string{"ios/", "android/", "macos/", "windows/", "linux/"} {
		if strings.HasPrefix(slashPath, dir) || strings.Contains(slashPath, "/"+dir) {
			return true
		}
	}
	return false
}

// ReloadDriver consumes file-change events and drives an attach-based dev server
// (Reloadable) accordingly. It is decoupled from FileWatcher so it can be unit
// tested with a plain channel; use NewFlutterReloadDriver to wire a real watcher.
type ReloadDriver struct {
	changes   <-chan FileChangeEvent
	target    Reloadable
	classify  func([]string) ReloadAction
	onRebuild func(files []string)
	onLog     func(string)
}

// NewReloadDriver creates a driver that reads from changes and drives target.
// The classifier defaults to ClassifyFlutterChange.
func NewReloadDriver(changes <-chan FileChangeEvent, target Reloadable) *ReloadDriver {
	return &ReloadDriver{
		changes:  changes,
		target:   target,
		classify: ClassifyFlutterChange,
	}
}

// SetClassifier overrides the change classifier.
func (d *ReloadDriver) SetClassifier(fn func([]string) ReloadAction) {
	if fn != nil {
		d.classify = fn
	}
}

// SetRebuildHandler registers a callback invoked when a change requires a full
// rebuild (the attach session cannot apply it). If unset, rebuild-class changes
// are logged and skipped.
func (d *ReloadDriver) SetRebuildHandler(fn func(files []string)) {
	d.onRebuild = fn
}

// SetLogCallback registers a log callback.
func (d *ReloadDriver) SetLogCallback(fn func(string)) {
	d.onLog = fn
}

func (d *ReloadDriver) log(format string, args ...interface{}) {
	if d.onLog != nil {
		d.onLog(fmt.Sprintf(format, args...))
	}
}

// Run consumes change events until ctx is cancelled or the changes channel is
// closed. Events are handled one at a time, in order.
func (d *ReloadDriver) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-d.changes:
			if !ok {
				return nil
			}
			d.handle(ctx, ev)
		}
	}
}

func (d *ReloadDriver) handle(ctx context.Context, ev FileChangeEvent) {
	switch d.classify(ev.Files) {
	case ReloadActionReload:
		d.log("Hot reloading (%d file(s) changed)...", len(ev.Files))
		if err := d.target.Reload(ctx); err != nil {
			d.log("Hot reload failed: %v", err)
		}
	case ReloadActionRestart:
		d.log("Hot restarting...")
		if err := d.target.HotRestart(ctx); err != nil {
			d.log("Hot restart failed: %v", err)
		}
	case ReloadActionRebuild:
		if d.onRebuild != nil {
			d.log("Change requires a rebuild; delegating...")
			d.onRebuild(ev.Files)
		} else {
			d.log("Change requires a rebuild (pubspec/native); not applied by hot reload.")
		}
	}
}

// NewFlutterReloadDriver builds a ReloadDriver wired to a Flutter file watcher
// rooted at rootDir. The caller is responsible for starting the returned watcher
// (watcher.Start) before calling driver.Run, and stopping it afterward.
func NewFlutterReloadDriver(rootDir string, target Reloadable) (*ReloadDriver, *FileWatcher, error) {
	watcher, err := NewFileWatcher(rootDir, FlutterWatchConfig())
	if err != nil {
		return nil, nil, err
	}
	return NewReloadDriver(watcher.Changes(), target), watcher, nil
}
