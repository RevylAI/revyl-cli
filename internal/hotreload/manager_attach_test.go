package hotreload

import (
	"context"
	"testing"

	"github.com/revyl/cli/internal/config"
)

// fakeAttachDevServer is a DevServer that records attach wiring for tests.
type fakeAttachDevServer struct {
	debugURL string
	started  bool
	reloads  int
}

func (f *fakeAttachDevServer) Start(ctx context.Context) error  { f.started = true; return nil }
func (f *fakeAttachDevServer) Stop() error                      { return nil }
func (f *fakeAttachDevServer) GetPort() int                     { return 0 }
func (f *fakeAttachDevServer) GetDeepLinkURL(string) string     { return "" }
func (f *fakeAttachDevServer) SetProxyURL(string)               {}
func (f *fakeAttachDevServer) Name() string                     { return "FakeFlutter" }
func (f *fakeAttachDevServer) SetDebugURL(u string)             { f.debugURL = u }
func (f *fakeAttachDevServer) Reload(context.Context) error     { f.reloads++; return nil }
func (f *fakeAttachDevServer) HotRestart(context.Context) error { return nil }

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
