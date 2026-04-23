package main

import "testing"

func TestDeviceLogsCmdWiring(t *testing.T) {
	t.Parallel()

	if got := deviceLogsCmd.Use; got != "logs" {
		t.Fatalf("deviceLogsCmd.Use = %q, want %q", got, "logs")
	}
	if deviceLogsCmd.Short == "" {
		t.Fatal("deviceLogsCmd.Short is empty")
	}
	if deviceLogsCmd.RunE == nil {
		t.Fatal("deviceLogsCmd.RunE is nil")
	}

	for _, name := range []string{"follow", "no-follow", "interval", "json", "s"} {
		if deviceLogsCmd.Flags().Lookup(name) == nil {
			t.Errorf("deviceLogsCmd missing --%s flag", name)
		}
	}

	if got := deviceLogsCmd.Flags().ShorthandLookup("f"); got == nil {
		t.Error("deviceLogsCmd missing -f shorthand for --follow")
	}
}
