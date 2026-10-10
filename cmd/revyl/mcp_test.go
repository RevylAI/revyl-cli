package main

import "testing"

func TestMCPWorkspaceFlagIsOptIn(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"mcp", "serve"})
	if err != nil {
		t.Fatal(err)
	}
	flag := cmd.Flags().Lookup("experimental-workspace")
	if flag == nil || flag.DefValue != "false" || flag.Value.Type() != "bool" {
		t.Fatalf("workspace must be an explicit boolean opt-in: %+v", flag)
	}
}
