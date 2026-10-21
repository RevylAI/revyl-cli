package main

import (
	"strings"
	"testing"
)

func TestComputerSurfacesAreHiddenUntilEnabled(t *testing.T) {
	computer, _, err := rootCmd.Find([]string{"computer"})
	if err != nil || computer.Name() != "computer" {
		t.Fatalf("revyl computer must stay registered: %v", err)
	}
	if computer.IsAvailableCommand() {
		t.Fatal("revyl computer must stay out of revyl --help until computers are enabled in production")
	}
	if flag := deviceStartCmd.Flags().Lookup("computer"); flag == nil || !flag.Hidden {
		t.Fatal("device start --computer must stay registered and out of its help until computers are enabled in production")
	}
	if strings.Contains(deviceStartCmd.Example, "--computer") {
		t.Fatal("device start examples must not show --computer until computers are enabled in production")
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
