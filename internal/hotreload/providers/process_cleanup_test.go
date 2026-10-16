package providers

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/revyl/cli/internal/hotreload"
)

func TestExpoDevServerStopDoesNotKillUnownedListenerOnPort(t *testing.T) {
	ln, port := listenOnProviderTestPort(t)
	defer ln.Close()

	server := NewExpoDevServer(t.TempDir(), "myapp", port, false)
	if err := server.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	assertListenerStillAccepts(t, ln)
}

func TestBareRNDevServerStopDoesNotKillUnownedListenerOnPort(t *testing.T) {
	ln, port := listenOnProviderTestPort(t)
	defer ln.Close()

	server := NewBareRNDevServer(t.TempDir(), port)
	if err := server.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	assertListenerStillAccepts(t, ln)
}

func TestExpoDevServerPortAvailabilityDetectsWildcardListener(t *testing.T) {
	ln, port := listenOnWildcardProviderTestPort(t)
	defer ln.Close()

	server := NewExpoDevServer(t.TempDir(), "myapp", port, false)
	if server.isPortAvailable() {
		t.Fatalf("isPortAvailable() = true, want false for wildcard listener on port %d", port)
	}
}

func TestBareRNDevServerPortAvailabilityDetectsWildcardListener(t *testing.T) {
	ln, port := listenOnWildcardProviderTestPort(t)
	defer ln.Close()

	server := NewBareRNDevServer(t.TempDir(), port)
	if server.isPortAvailable() {
		t.Fatalf("isPortAvailable() = true, want false for wildcard listener on port %d", port)
	}
}

func TestExpoDevServerReportsUnexpectedProcessExit(t *testing.T) {
	server := NewExpoDevServer(t.TempDir(), "myapp", freeProviderTestPort(t), false)
	cmd := exec.Command("sh", "-c", "exit 7")
	setSysProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	server.mu.Lock()
	server.cmd = cmd
	server.processDone = make(chan error, 1)
	server.ready = true
	server.stopping = false
	server.mu.Unlock()
	go server.watchProcess(context.Background(), cmd, server.processDone)

	failure := readProviderRuntimeFailure(t, server.Failures())
	if failure.Kind != hotreload.RuntimeFailureLocalDevServerDown {
		t.Fatalf("failure kind = %q, want %q", failure.Kind, hotreload.RuntimeFailureLocalDevServerDown)
	}
	if !failure.Fatal {
		t.Fatal("expected process exit to be fatal")
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("Stop() after process exit error = %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestBareRNDevServerReportsUnexpectedProcessExit(t *testing.T) {
	server := NewBareRNDevServer(t.TempDir(), freeProviderTestPort(t))
	cmd := exec.Command("sh", "-c", "exit 7")
	setSysProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	server.mu.Lock()
	server.cmd = cmd
	server.processDone = make(chan error, 1)
	server.ready = true
	server.stopping = false
	server.mu.Unlock()
	go server.watchProcess(context.Background(), cmd, server.processDone)

	failure := readProviderRuntimeFailure(t, server.Failures())
	if failure.Kind != hotreload.RuntimeFailureLocalDevServerDown {
		t.Fatalf("failure kind = %q, want %q", failure.Kind, hotreload.RuntimeFailureLocalDevServerDown)
	}
	if !failure.Fatal {
		t.Fatal("expected process exit to be fatal")
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("Stop() after process exit error = %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestExpoDevServerCanStartAfterStop(t *testing.T) {
	withFakeNpxReadyProcess(t)

	server := NewExpoDevServer(t.TempDir(), "myapp", freeProviderTestPort(t), false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if err := server.Start(ctx); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestBareRNDevServerCanStartAfterStop(t *testing.T) {
	withFakeNpxReadyProcess(t)

	server := NewBareRNDevServer(t.TempDir(), freeProviderTestPort(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if err := server.Start(ctx); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if err := server.Stop(); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func listenOnProviderTestPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		t.Fatalf("listener addr %T is not *net.TCPAddr", ln.Addr())
	}
	return ln, addr.Port
}

func listenOnWildcardProviderTestPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		t.Fatalf("listener addr %T is not *net.TCPAddr", ln.Addr())
	}
	return ln, addr.Port
}

func freeProviderTestPort(t *testing.T) int {
	t.Helper()
	ln, port := listenOnProviderTestPort(t)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close listener: %v", err)
	}
	return port
}

func readProviderRuntimeFailure(t *testing.T, failures <-chan hotreload.RuntimeFailure) hotreload.RuntimeFailure {
	t.Helper()
	select {
	case failure := <-failures:
		return failure
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for runtime failure")
	}
	return hotreload.RuntimeFailure{}
}

func withFakeNpxReadyProcess(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	npxName := "npx"
	script := `#!/bin/sh
echo "Metro waiting on exp://127.0.0.1:8081"
trap 'exit 0' TERM INT
while true; do sleep 1; done
`
	if runtime.GOOS == "windows" {
		npxName = "npx.cmd"
		script = "@echo off\r\n" +
			"echo Metro waiting on exp://127.0.0.1:8081\r\n" +
			":loop\r\n" +
			"timeout /t 1 /nobreak >nul\r\n" +
			"goto loop\r\n"
	}
	npxPath := filepath.Join(binDir, npxName)
	if err := os.WriteFile(npxPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake npx: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func assertListenerStillAccepts(t *testing.T, ln net.Listener) {
	t.Helper()
	tcpListener, ok := ln.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener %T is not *net.TCPListener", ln)
	}
	if err := tcpListener.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	defer tcpListener.SetDeadline(time.Time{})

	accepted := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
		accepted <- err
	}()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("listener no longer accepts connections: %v", err)
	}
	_ = conn.Close()

	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("listener accept failed after Stop(): %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not accept after Stop()")
	}
}
