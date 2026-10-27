package main

import (
	"testing"
)

func TestComputerSurfacesAreListed(t *testing.T) {
	computer, _, err := rootCmd.Find([]string{"computer"})
	if err != nil || computer.Name() != "computer" {
		t.Fatalf("revyl computer must stay registered: %v", err)
	}
	if !computer.IsAvailableCommand() {
		t.Fatal("revyl computer must be listed in revyl --help")
	}
	if flag := deviceStartCmd.Flags().Lookup("computer"); flag == nil || flag.Hidden {
		t.Fatal("device start --computer must be listed in its help")
	}
}

func TestDeviceStartComputerFlagStillParses(t *testing.T) {
	flags := deviceStartCmd.Flags()
	flag := flags.Lookup("computer")
	t.Cleanup(func() {
		_ = flag.Value.Set("")
		flag.Changed = false
	})
	const computerSessionID = "11111111-1111-4111-8111-111111111111"
	if err := flags.Parse([]string{"--computer", computerSessionID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := flags.GetString("computer"); got != computerSessionID {
		t.Fatalf("--computer = %q, want %q", got, computerSessionID)
	}
}

func TestComputerShellIsNotRegisteredInRevyl(t *testing.T) {
	if _, _, err := rootCmd.Find([]string{"ssh"}); err == nil {
		t.Fatal("machine shell must be exposed by revyl-computer, not revyl")
	}
}
