package main

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// proofCommentInvocation records what `revyl proof comment` parsed before it
// would have published, so a test can assert the flag contract without a
// proof-run credential or a network call.
type proofCommentInvocation struct {
	ran     bool
	args    []string
	problem string
	blocked string
}

// stubProofCommentRun replaces the publish step with a recorder and restores
// the command tree afterwards. The proof command is a package-level singleton
// wired into rootCmd, so parsed flag values and their Changed markers survive
// an Execute and must be cleared between table cases.
func stubProofCommentRun(t *testing.T) *proofCommentInvocation {
	t.Helper()

	recorded := &proofCommentInvocation{}
	originalRunE := proofCommentCmd.RunE
	proofCommentCmd.RunE = func(cmd *cobra.Command, args []string) error {
		recorded.ran = true
		recorded.args = args
		recorded.problem = proofCommentProblem
		recorded.blocked = proofCommentBlocked
		return nil
	}

	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	t.Cleanup(func() {
		proofCommentCmd.RunE = originalRunE
		proofCommentProblem = ""
		proofCommentBlocked = ""
		for _, name := range []string{"problem", "blocked"} {
			flag := proofCommentCmd.Flags().Lookup(name)
			if flag == nil {
				continue
			}
			_ = flag.Value.Set("")
			flag.Changed = false
		}
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	return recorded
}

func TestProofCommentRegistersOutcomeFlags(t *testing.T) {
	tests := []struct {
		name      string
		flagName  string
		wantUsage string
	}{
		{
			name:      "problem reports a broken pull request",
			flagName:  "problem",
			wantUsage: "broken",
		},
		{
			name:      "blocked reports that nothing was observed",
			flagName:  "blocked",
			wantUsage: "observ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := proofCommentCmd.Flags().Lookup(tt.flagName)
			if flag == nil {
				t.Fatalf("--%s is not registered on `revyl proof comment`", tt.flagName)
			}
			if flag.DefValue != "" {
				t.Fatalf("--%s default = %q, want empty so an unset flag claims nothing", tt.flagName, flag.DefValue)
			}
			if !strings.Contains(flag.Usage, tt.wantUsage) {
				t.Fatalf("--%s usage = %q, want it to mention %q", tt.flagName, flag.Usage, tt.wantUsage)
			}
		})
	}
}

func TestProofCommentBindsOutcomeFlagsToRequestValues(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantProblem string
		wantBlocked string
	}{
		{
			name:        "neither flag claims a pass",
			args:        []string{"--json", "proof", "comment", "write-up.md"},
			wantProblem: "",
			wantBlocked: "",
		},
		{
			name:        "problem alone reports a broken pull request",
			args:        []string{"--json", "proof", "comment", "write-up.md", "--problem", "Saving a note clears the title field"},
			wantProblem: "Saving a note clears the title field",
			wantBlocked: "",
		},
		{
			name:        "blocked alone reports that nothing was observed",
			args:        []string{"--json", "proof", "comment", "write-up.md", "--blocked", "No device could be started"},
			wantProblem: "",
			wantBlocked: "No device could be started",
		},
		{
			name:        "whitespace-only blocked sends nothing once trimmed",
			args:        []string{"--json", "proof", "comment", "write-up.md", "--blocked", "   "},
			wantProblem: "",
			wantBlocked: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorded := stubProofCommentRun(t)

			rootCmd.SetArgs(tt.args)
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("rootCmd.Execute() error = %v, want the command to run", err)
			}

			if !recorded.ran {
				t.Fatal("`revyl proof comment` did not run")
			}
			if len(recorded.args) != 1 || recorded.args[0] != "write-up.md" {
				t.Fatalf("write-up argument = %v, want [write-up.md]", recorded.args)
			}
			// runProofComment trims before handing each sentence to the
			// request, so trimmed values are what reach the pull request.
			if got := strings.TrimSpace(recorded.problem); got != tt.wantProblem {
				t.Fatalf("problem sent = %q, want %q", got, tt.wantProblem)
			}
			if got := strings.TrimSpace(recorded.blocked); got != tt.wantBlocked {
				t.Fatalf("blocked sent = %q, want %q", got, tt.wantBlocked)
			}
		})
	}
}

func TestProofCommentRejectsBothOutcomeFlags(t *testing.T) {
	recorded := stubProofCommentRun(t)

	rootCmd.SetArgs([]string{
		"--json", "proof", "comment", "write-up.md",
		"--problem", "Saving a note clears the title field",
		"--blocked", "No device could be started",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("rootCmd.Execute() error = nil, want --problem and --blocked to be mutually exclusive")
	}
	// An agent that never observed the app cannot also have found a bug in
	// it, so the run must be refused rather than settled on a guessed verdict.
	if recorded.ran {
		t.Fatal("`revyl proof comment` published despite contradictory outcomes")
	}
	for _, flagName := range []string{"problem", "blocked"} {
		if !strings.Contains(err.Error(), flagName) {
			t.Fatalf("error = %q, want it to name --%s", err.Error(), flagName)
		}
	}
}

func TestProofCommentHelpTellsCursorToPostOnce(t *testing.T) {
	help := proofCommentCmd.Long
	if strings.Contains(help, "Post early with what you have and post again") {
		t.Fatal("`revyl proof comment --help` still tells Cursor to post incrementally")
	}
	if !strings.Contains(strings.ToLower(help), "one comment per pull request") {
		t.Fatalf("help = %q, want it to name one comment per pull request", help)
	}
	if strings.Contains(help, "one proof comment per platform") {
		t.Fatal("`revyl proof comment --help` still claims one proof comment per platform")
	}
	if !strings.Contains(help, "posts once") {
		t.Fatalf("help = %q, want it to tell Cursor to post once", help)
	}
	if !strings.Contains(help, "refuses the") {
		t.Fatalf("help = %q, want it to refuse unpublished images", help)
	}
}
