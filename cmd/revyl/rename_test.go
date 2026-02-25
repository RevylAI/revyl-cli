package main

import (
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestChooseAliasForTestRename(t *testing.T) {
	tests := []struct {
		name        string
		aliases     map[string]string
		oldNameOrID string
		remoteName  string
		testID      string
		wantAlias   string
		wantAmbig   bool
	}{
		{
			name: "old arg alias wins",
			aliases: map[string]string{
				"CLI-0-onboard-a": "id-1",
			},
			oldNameOrID: "CLI-0-onboard-a",
			remoteName:  "CLI-0-onboard-a",
			testID:      "id-1",
			wantAlias:   "CLI-0-onboard-a",
		},
		{
			name: "single alias inferred",
			aliases: map[string]string{
				"tracked-name": "id-2",
			},
			oldNameOrID: "id-2",
			remoteName:  "Remote Name",
			testID:      "id-2",
			wantAlias:   "tracked-name",
		},
		{
			name: "ambiguous aliases",
			aliases: map[string]string{
				"one": "id-3",
				"two": "id-3",
			},
			oldNameOrID: "id-3",
			remoteName:  "Remote Name",
			testID:      "id-3",
			wantAlias:   "",
			wantAmbig:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAlias, gotAmbig := chooseAliasForTestRename(tt.aliases, tt.oldNameOrID, tt.remoteName, tt.testID)
			if gotAlias != tt.wantAlias {
				t.Fatalf("alias=%q want %q", gotAlias, tt.wantAlias)
			}
			if gotAmbig != tt.wantAmbig {
				t.Fatalf("ambiguous=%v want %v", gotAmbig, tt.wantAmbig)
			}
		})
	}
}

func TestChooseLocalFileForTestRename(t *testing.T) {
	tests := []struct {
		name          string
		local         map[string]*config.LocalTest
		aliasToRename string
		oldNameOrID   string
		remoteName    string
		testID        string
		wantAlias     string
		wantAmbig     bool
	}{
		{
			name: "uses alias file even with empty remote id",
			local: map[string]*config.LocalTest{
				"tracked": &config.LocalTest{Meta: config.TestMeta{RemoteID: ""}},
			},
			aliasToRename: "tracked",
			oldNameOrID:   "id-1",
			remoteName:    "Remote",
			testID:        "id-1",
			wantAlias:     "tracked",
		},
		{
			name: "falls back to remote id match",
			local: map[string]*config.LocalTest{
				"other": &config.LocalTest{Meta: config.TestMeta{RemoteID: "id-x"}},
				"mine":  &config.LocalTest{Meta: config.TestMeta{RemoteID: "id-2"}},
			},
			oldNameOrID: "id-2",
			remoteName:  "Remote",
			testID:      "id-2",
			wantAlias:   "mine",
		},
		{
			name: "ambiguous remote id matches",
			local: map[string]*config.LocalTest{
				"a": &config.LocalTest{Meta: config.TestMeta{RemoteID: "id-3"}},
				"b": &config.LocalTest{Meta: config.TestMeta{RemoteID: "id-3"}},
			},
			oldNameOrID: "id-3",
			remoteName:  "Remote",
			testID:      "id-3",
			wantAlias:   "a",
			wantAmbig:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAlias, gotAmbig := chooseLocalFileForTestRename(tt.local, tt.aliasToRename, tt.oldNameOrID, tt.remoteName, tt.testID)
			if gotAlias != tt.wantAlias {
				t.Fatalf("alias=%q want %q", gotAlias, tt.wantAlias)
			}
			if gotAmbig != tt.wantAmbig {
				t.Fatalf("ambiguous=%v want %v", gotAmbig, tt.wantAmbig)
			}
		})
	}
}

func TestTestRenameSubcommandRegistered(t *testing.T) {
	var testCmdFound bool
	var renameFound bool

	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() != "test" {
			continue
		}
		testCmdFound = true
		for _, child := range cmd.Commands() {
			if child.Name() == "rename" {
				renameFound = true
				break
			}
		}
	}

	if !testCmdFound {
		t.Fatal("expected 'test' command to exist")
	}
	if !renameFound {
		t.Fatal("expected 'test rename' command to exist")
	}
}
