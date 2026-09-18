package main

import "testing"

func TestComputerShellIsNotRegisteredInRevyl(t *testing.T) {
	if _, _, err := rootCmd.Find([]string{"ssh"}); err == nil {
		t.Fatal("machine shell must be exposed by revyl-computer, not revyl")
	}
}
