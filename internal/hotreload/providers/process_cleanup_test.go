package providers

import (
	"net"
	"testing"
	"time"
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
