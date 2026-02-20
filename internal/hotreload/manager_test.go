package hotreload

import (
	"context"
	"testing"
)

type fakeDevServer struct{}

func (f *fakeDevServer) Start(ctx context.Context) error { return nil }
func (f *fakeDevServer) Stop() error                     { return nil }
func (f *fakeDevServer) GetPort() int                    { return 8081 }
func (f *fakeDevServer) GetDeepLinkURL(tunnelURL string) string {
	return tunnelURL
}
func (f *fakeDevServer) Name() string                 { return "fake" }
func (f *fakeDevServer) SetProxyURL(tunnelURL string) {}

type fakeOutputDevServer struct {
	fakeDevServer
	callback DevServerOutputCallback
}

func (f *fakeOutputDevServer) SetOutputCallback(callback DevServerOutputCallback) {
	f.callback = callback
}

type fakeDebugDevServer struct {
	fakeDevServer
	debugMode bool
}

func (f *fakeDebugDevServer) SetDebugMode(enabled bool) {
	f.debugMode = enabled
}

func TestAttachDevServerOutputCallback_AttachesWhenSupported(t *testing.T) {
	m := &Manager{}
	devServer := &fakeOutputDevServer{}

	var received DevServerOutput
	m.SetDevServerOutputCallback(func(output DevServerOutput) {
		received = output
	})

	m.attachDevServerOutputCallback(devServer)

	if devServer.callback == nil {
		t.Fatal("expected output callback to be attached")
	}

	devServer.callback(DevServerOutput{
		Stream: DevServerOutputStdout,
		Line:   "Metro ready",
	})

	if received.Stream != DevServerOutputStdout {
		t.Fatalf("received stream = %q, want %q", received.Stream, DevServerOutputStdout)
	}
	if received.Line != "Metro ready" {
		t.Fatalf("received line = %q, want %q", received.Line, "Metro ready")
	}
}

func TestAttachDevServerOutputCallback_NoConfiguredCallback(t *testing.T) {
	m := &Manager{}
	devServer := &fakeOutputDevServer{}

	m.attachDevServerOutputCallback(devServer)

	if devServer.callback != nil {
		t.Fatal("expected callback to remain nil when manager callback is unset")
	}
}

func TestAttachDevServerOutputCallback_IgnoresUnsupportedServer(t *testing.T) {
	m := &Manager{}
	m.SetDevServerOutputCallback(func(output DevServerOutput) {})

	unsupported := &fakeDevServer{}
	m.attachDevServerOutputCallback(unsupported)
}

func TestAttachDevServerDebugMode_AttachesWhenSupported(t *testing.T) {
	m := &Manager{}
	m.SetDebugMode(true)

	devServer := &fakeDebugDevServer{}
	m.attachDevServerDebugMode(devServer)

	if !devServer.debugMode {
		t.Fatal("expected debug mode to be enabled on dev server")
	}
}

func TestAttachDevServerDebugMode_IgnoresUnsupportedServer(t *testing.T) {
	m := &Manager{}
	m.SetDebugMode(true)

	unsupported := &fakeDevServer{}
	m.attachDevServerDebugMode(unsupported)
}
