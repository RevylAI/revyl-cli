package main

import (
	"context"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/build"
	"github.com/spf13/cobra"
)

func TestBuildUploadSCMFlags(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, testCase := range []struct {
		name      string
		args      []string
		wantError bool
	}{
		{"explicit PR", []string{"--repo", "acme/mobile", "--commit", sha, "--pr", "42"}, false},
		{"SHA only", []string{"--repo", "acme/mobile", "--commit", sha}, false},
		{"missing repo", []string{"--commit", sha}, true},
		{"missing commit", []string{"--repo", "acme/mobile"}, true},
		{"PR only", []string{"--pr", "42"}, true},
		{"empty commit", []string{"--repo", "acme/mobile", "--commit", ""}, true},
		{"short commit", []string{"--repo", "acme/mobile", "--commit", "abc123"}, true},
		{"invalid hex", []string{"--repo", "acme/mobile", "--commit", strings.Repeat("z", 40)}, true},
		{"invalid repo", []string{"--repo", "acme/mobile/extra", "--commit", sha}, true},
		{"zero PR", []string{"--repo", "acme/mobile", "--commit", sha, "--pr", "0"}, true},
		{"negative PR", []string{"--repo", "acme/mobile", "--commit", sha, "--pr", "-1"}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", "")
			t.Setenv("BUILDKITE", "")
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			registerBuildUploadSCMFlags(cmd)
			if err := cmd.ParseFlags(testCase.args); err != nil {
				t.Fatal(err)
			}
			err := applyBuildUploadSCMFlags(cmd)
			if (err != nil) != testCase.wantError {
				t.Fatalf("error = %v, wantError = %t", err, testCase.wantError)
			}
			if err != nil {
				return
			}
			ci, ok := build.CIContextFromContext(cmd.Context())
			if !ok || ci.Repository != "acme/mobile" || ci.CommitSHA != sha {
				t.Fatalf("CI context = %#v", ci)
			}
			if _, ok := build.DetectCIContext(); ok {
				t.Fatal("explicit flags changed ambient runner detection")
			}
		})
	}
}

func TestBuildUploadSCMFlagsRegistered(t *testing.T) {
	for _, name := range []string{"repo", "commit", "pr"} {
		flag := buildUploadCmd.Flags().Lookup(name)
		if flag == nil || flag.Hidden {
			t.Fatalf("missing public flag --%s", name)
		}
	}
}
