package hotreload

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClassifyFlutterChange(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  ReloadAction
	}{
		{"dart edit", []string{"lib/main.dart"}, ReloadActionReload},
		{"nested dart", []string{"lib/widgets/button.dart"}, ReloadActionReload},
		{"pubspec", []string{"pubspec.yaml"}, ReloadActionRebuild},
		{"pubspec lock", []string{"pubspec.lock"}, ReloadActionRebuild},
		{"ios native", []string{"ios/Runner/AppDelegate.swift"}, ReloadActionRebuild},
		{"android native", []string{"android/app/build.gradle"}, ReloadActionRebuild},
		{"dart wins over none", []string{"README.md", "lib/main.dart"}, ReloadActionReload},
		{"rebuild wins over dart", []string{"lib/main.dart", "pubspec.yaml"}, ReloadActionRebuild},
		{"unrelated", []string{"README.md"}, ReloadActionNone},
		{"empty", nil, ReloadActionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyFlutterChange(tc.files); got != tc.want {
				t.Fatalf("ClassifyFlutterChange(%v) = %v, want %v", tc.files, got, tc.want)
			}
		})
	}
}

// fakeReloadTarget records reload/restart calls for the driver tests.
type fakeReloadTarget struct {
	mu        sync.Mutex
	reloads   int
	restarts  int
	reloadErr error
}

func (f *fakeReloadTarget) Reload(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloads++
	return f.reloadErr
}

func (f *fakeReloadTarget) HotRestart(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return nil
}

func (f *fakeReloadTarget) reloadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reloads
}

func TestReloadDriverTriggersReloadOnDartChange(t *testing.T) {
	changes := make(chan FileChangeEvent, 1)
	target := &fakeReloadTarget{}
	driver := NewReloadDriver(changes, target)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { _ = driver.Run(ctx); close(done) }()

	changes <- FileChangeEvent{Files: []string{"lib/main.dart"}, Timestamp: time.Now()}

	waitFor(t, func() bool { return target.reloadCount() == 1 }, "reload not triggered")
	cancel()
	<-done
}

func TestReloadDriverDelegatesRebuild(t *testing.T) {
	changes := make(chan FileChangeEvent, 1)
	target := &fakeReloadTarget{}
	driver := NewReloadDriver(changes, target)

	var rebuiltMu sync.Mutex
	rebuilt := 0
	driver.SetRebuildHandler(func(files []string) {
		rebuiltMu.Lock()
		rebuilt++
		rebuiltMu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = driver.Run(ctx); close(done) }()

	changes <- FileChangeEvent{Files: []string{"pubspec.yaml"}, Timestamp: time.Now()}

	waitFor(t, func() bool {
		rebuiltMu.Lock()
		defer rebuiltMu.Unlock()
		return rebuilt == 1
	}, "rebuild handler not invoked")

	if target.reloadCount() != 0 {
		t.Fatal("rebuild-class change must not trigger a hot reload")
	}
	cancel()
	<-done
}

func TestReloadDriverStopsWhenChannelClosed(t *testing.T) {
	changes := make(chan FileChangeEvent)
	driver := NewReloadDriver(changes, &fakeReloadTarget{})

	done := make(chan error, 1)
	go func() { done <- driver.Run(context.Background()) }()

	close(changes)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on channel close", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after channel close")
	}
}

func TestReloadDriverLogsReloadError(t *testing.T) {
	changes := make(chan FileChangeEvent, 1)
	target := &fakeReloadTarget{reloadErr: errors.New("boom")}
	driver := NewReloadDriver(changes, target)

	var logged []string
	var logMu sync.Mutex
	driver.SetLogCallback(func(s string) {
		logMu.Lock()
		logged = append(logged, s)
		logMu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = driver.Run(ctx); close(done) }()

	changes <- FileChangeEvent{Files: []string{"lib/main.dart"}, Timestamp: time.Now()}

	waitFor(t, func() bool {
		logMu.Lock()
		defer logMu.Unlock()
		for _, l := range logged {
			if strings.Contains(l, "failed") {
				return true
			}
		}
		return false
	}, "reload error was not logged")
	cancel()
	<-done
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}
