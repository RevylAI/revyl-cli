package providers

import (
	"context"
	"strings"
	"testing"
)

func TestFlutterAttachArgs(t *testing.T) {
	srv := NewFlutterAttachDevServer("/work", "http://127.0.0.1:54321/")
	args := srv.attachArgs()
	want := []string{"attach", "--no-version-check", "--debug-url", "http://127.0.0.1:54321/"}
	if len(args) != len(want) {
		t.Fatalf("attachArgs length = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("attachArgs[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestFlutterAttachStartRequiresDebugURL(t *testing.T) {
	srv := NewFlutterAttachDevServer("/work", "")
	err := srv.Start(context.Background())
	if err == nil {
		t.Fatal("Start without a debug URL should error")
	}
	if !strings.Contains(err.Error(), "debug URL") {
		t.Fatalf("error should mention the missing debug URL, got %v", err)
	}
}

func TestFlutterAttachReloadRequiresRunning(t *testing.T) {
	srv := NewFlutterAttachDevServer("/work", "http://127.0.0.1:1/")
	if err := srv.Reload(context.Background()); err == nil {
		t.Fatal("Reload before Start should error")
	}
	if err := srv.HotRestart(context.Background()); err == nil {
		t.Fatal("HotRestart before Start should error")
	}
}

func TestFlutterAttachSetDebugURL(t *testing.T) {
	srv := NewFlutterAttachDevServer("/work", "")
	srv.SetDebugURL("  http://127.0.0.1:9999/  ")
	if got := srv.attachArgs()[3]; got != "http://127.0.0.1:9999/" {
		t.Fatalf("SetDebugURL should trim and store, got %q", got)
	}
}

func TestFlutterAttachStaticContracts(t *testing.T) {
	srv := NewFlutterAttachDevServer("/work", "http://127.0.0.1:1/")
	if srv.Name() != "Flutter" {
		t.Fatalf("Name() = %q, want Flutter", srv.Name())
	}
	if srv.GetPort() != 0 {
		t.Fatalf("GetPort() = %d, want 0", srv.GetPort())
	}
	if srv.GetDeepLinkURL("x") != "" {
		t.Fatal("GetDeepLinkURL should be a no-op")
	}
}
