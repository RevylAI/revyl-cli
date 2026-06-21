package hotreload

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/revyl/cli/internal/config"
)

// fakeAttachDevServer is a DevServer that records attach wiring for tests.
type fakeAttachDevServer struct {
	debugURL string
	started  bool

	mu      sync.Mutex
	reloads int
}

func (f *fakeAttachDevServer) Start(ctx context.Context) error  { f.started = true; return nil }
func (f *fakeAttachDevServer) Stop() error                      { return nil }
func (f *fakeAttachDevServer) GetPort() int                     { return 0 }
func (f *fakeAttachDevServer) GetDeepLinkURL(string) string     { return "" }
func (f *fakeAttachDevServer) SetProxyURL(string)               {}
func (f *fakeAttachDevServer) Name() string                     { return "FakeFlutter" }
func (f *fakeAttachDevServer) SetDebugURL(u string)             { f.debugURL = u }
func (f *fakeAttachDevServer) HotRestart(context.Context) error { return nil }

func (f *fakeAttachDevServer) Reload(context.Context) error {
	f.mu.Lock()
	f.reloads++
	f.mu.Unlock()
	return nil
}

func (f *fakeAttachDevServer) reloadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reloads
}

// fakeReverseTunnel records the reverse-forward request for tests.
type fakeReverseTunnel struct {
	startedPort int
	localAddr   string
	stopped     bool
}

func (f *fakeReverseTunnel) StartReverse(ctx context.Context, devicePort int) (string, error) {
	f.startedPort = devicePort
	f.localAddr = "127.0.0.1:55555"
	return f.localAddr, nil
}
func (f *fakeReverseTunnel) StopReverse() error { f.stopped = true; return nil }
func (f *fakeReverseTunnel) LocalAddr() string  { return f.localAddr }

func newAttachManager(t *testing.T, ds DevServer) *Manager {
	t.Helper()
	RegisterFlutterAttachDevServerFactory(func(workDir string) DevServer { return ds })
	t.Cleanup(func() { RegisterFlutterAttachDevServerFactory(nil) })

	m := NewManager("flutter", &config.ProviderConfig{}, "/work")
	m.SetDevLoopStyle(DevLoopStyleAttach)
	return m
}

func TestStartAttachUsesReverseTunnel(t *testing.T) {
	ds := &fakeAttachDevServer{}
	m := newAttachManager(t, ds)
	ft := &fakeReverseTunnel{}
	m.SetReverseTunnelBackendFactory(func() ReverseTunnelBackend { return ft })
	m.SetDeviceVMServicePort(8181)

	res, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer m.Stop()

	if ft.startedPort != 8181 {
		t.Fatalf("reverse tunnel started for port %d, want 8181", ft.startedPort)
	}
	if ds.debugURL != "http://127.0.0.1:55555/" {
		t.Fatalf("dev server debug URL = %q, want http://127.0.0.1:55555/", ds.debugURL)
	}
	if !ds.started {
		t.Fatal("attach dev server was not started")
	}
	if res.Transport != "reverse-relay" {
		t.Fatalf("transport = %q, want reverse-relay", res.Transport)
	}
}

func TestStartAttachExternalDebugURLSkipsReverseTunnel(t *testing.T) {
	ds := &fakeAttachDevServer{}
	m := newAttachManager(t, ds)
	ft := &fakeReverseTunnel{}
	m.SetReverseTunnelBackendFactory(func() ReverseTunnelBackend { return ft })
	m.SetExternalDebugURL("http://127.0.0.1:12345/abc=/")

	res, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer m.Stop()

	if ft.startedPort != 0 {
		t.Fatal("reverse tunnel should not be used when an external debug URL is set")
	}
	if ds.debugURL != "http://127.0.0.1:12345/abc=/" {
		t.Fatalf("dev server debug URL = %q, want the external URL", ds.debugURL)
	}
	if res.Transport != "external-attach" {
		t.Fatalf("transport = %q, want external-attach", res.Transport)
	}
}

func TestStartAttachRequiresDevicePort(t *testing.T) {
	ds := &fakeAttachDevServer{}
	m := newAttachManager(t, ds)
	// No device port and no external debug URL -> error.
	if _, err := m.Start(context.Background()); err == nil {
		t.Fatal("Start should error when neither device port nor external debug URL is set")
	}
}

func TestStartAttachDrivesReloadOnFileChange(t *testing.T) {
	// Use a real temp dir so the file watcher can start, and a .dart file so a
	// change classifies as a hot reload.
	dir := t.TempDir()
	libDir := filepath.Join(dir, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mainDart := filepath.Join(libDir, "main.dart")
	if err := os.WriteFile(mainDart, []byte("void main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.dart: %v", err)
	}

	ds := &fakeAttachDevServer{}
	RegisterFlutterAttachDevServerFactory(func(workDir string) DevServer { return ds })
	t.Cleanup(func() { RegisterFlutterAttachDevServerFactory(nil) })

	m := NewManager("flutter", &config.ProviderConfig{}, dir)
	m.SetDevLoopStyle(DevLoopStyleAttach)
	m.SetExternalDebugURL("http://127.0.0.1:1/")

	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer m.Stop()

	// Edit the .dart file; the watcher (800ms debounce) -> driver -> Reload().
	if err := os.WriteFile(mainDart, []byte("void main() {}\n// changed\n"), 0o644); err != nil {
		t.Fatalf("edit main.dart: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ds.reloadCount() > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("file change did not drive a hot reload through the Manager attach loop")
}

func TestStartAttachStopsReverseTunnel(t *testing.T) {
	ds := &fakeAttachDevServer{}
	m := newAttachManager(t, ds)
	ft := &fakeReverseTunnel{}
	m.SetReverseTunnelBackendFactory(func() ReverseTunnelBackend { return ft })
	m.SetDeviceVMServicePort(9000)

	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	m.Stop()
	if !ft.stopped {
		t.Fatal("reverse tunnel was not stopped on Manager.Stop")
	}
}
