package tui

import (
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestChooseAliasForTestRenameTUI(t *testing.T) {
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
				"tracked-name": "id-1",
			},
			oldNameOrID: "tracked-name",
			remoteName:  "Tracked Name",
			testID:      "id-1",
			wantAlias:   "tracked-name",
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
			gotAlias, gotAmbig := chooseAliasForTestRenameTUI(tt.aliases, tt.oldNameOrID, tt.remoteName, tt.testID)
			if gotAlias != tt.wantAlias {
				t.Fatalf("alias=%q want %q", gotAlias, tt.wantAlias)
			}
			if gotAmbig != tt.wantAmbig {
				t.Fatalf("ambiguous=%v want %v", gotAmbig, tt.wantAmbig)
			}
		})
	}
}

func TestChooseLocalFileForTestRenameTUI(t *testing.T) {
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
				"tracked": {Meta: config.TestMeta{RemoteID: ""}},
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
				"other": {Meta: config.TestMeta{RemoteID: "id-x"}},
				"mine":  {Meta: config.TestMeta{RemoteID: "id-2"}},
			},
			oldNameOrID: "id-2",
			remoteName:  "Remote",
			testID:      "id-2",
			wantAlias:   "mine",
		},
		{
			name: "ambiguous remote id matches",
			local: map[string]*config.LocalTest{
				"a": {Meta: config.TestMeta{RemoteID: "id-3"}},
				"b": {Meta: config.TestMeta{RemoteID: "id-3"}},
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
			gotAlias, gotAmbig := chooseLocalFileForTestRenameTUI(tt.local, tt.aliasToRename, tt.oldNameOrID, tt.remoteName, tt.testID)
			if gotAlias != tt.wantAlias {
				t.Fatalf("alias=%q want %q", gotAlias, tt.wantAlias)
			}
			if gotAmbig != tt.wantAmbig {
				t.Fatalf("ambiguous=%v want %v", gotAmbig, tt.wantAmbig)
			}
		})
	}
}

func TestValidateTUIResourceName(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantError bool
	}{
		{name: "valid", input: "Checkout iOS", wantError: false},
		{name: "empty", input: "", wantError: true},
		{name: "path separator", input: "a/b", wantError: true},
		{name: "file extension", input: "test.yaml", wantError: true},
		{name: "reserved", input: "rename", wantError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTUIResourceName(tc.input, "test")
			if tc.wantError && err == nil {
				t.Fatalf("expected error for %q", tc.input)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("expected no error for %q, got %v", tc.input, err)
			}
		})
	}
}
