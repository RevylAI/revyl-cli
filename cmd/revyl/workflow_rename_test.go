package main

import "testing"

func TestWorkflowRenameSubcommandRegistered(t *testing.T) {
	var workflowCmdFound bool
	var renameFound bool

	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() != "workflow" {
			continue
		}
		workflowCmdFound = true
		for _, child := range cmd.Commands() {
			if child.Name() == "rename" {
				renameFound = true
				break
			}
		}
	}

	if !workflowCmdFound {
		t.Fatal("expected 'workflow' command to exist")
	}
	if !renameFound {
		t.Fatal("expected 'workflow rename' command to exist")
	}
}

func TestParseRenameArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantOld string
		wantNew string
	}{
		{name: "none", args: nil, wantOld: "", wantNew: ""},
		{name: "old only", args: []string{"old"}, wantOld: "old", wantNew: ""},
		{name: "full", args: []string{"old", "new"}, wantOld: "old", wantNew: "new"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOld, gotNew := parseRenameArgs(tt.args)
			if gotOld != tt.wantOld || gotNew != tt.wantNew {
				t.Fatalf("parseRenameArgs(%v) = (%q,%q), want (%q,%q)", tt.args, gotOld, gotNew, tt.wantOld, tt.wantNew)
			}
		})
	}
}
