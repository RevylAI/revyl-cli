package main

import (
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestRemoteBuildNextStepUsesDeviceStartFlags(t *testing.T) {
	for _, name := range []string{"platform", "app-id", "build-version-id"} {
		flag := deviceStartCmd.Flags().Lookup(name)
		value, changed := flag.Value.String(), flag.Changed
		t.Cleanup(func() {
			if err := flag.Value.Set(value); err != nil {
				t.Error(err)
			}
			flag.Changed = changed
		})
	}
	buildID := "22222222-2222-4222-8222-222222222222"
	appID := "11111111-1111-4111-8111-111111111111"
	suggestion := remoteBuildStartDeviceCommand(
		remoteBuildPlatformConfig{Platform: "ios", AppID: appID},
		&api.RemoteBuildStatusResponse{VersionId: &buildID},
	)
	command := &cobra.Command{Use: "start"}
	command.Flags().AddFlagSet(deviceStartCmd.Flags())
	if err := command.ParseFlags(strings.Fields(suggestion)[3:]); err != nil {
		t.Fatalf("suggested device command cannot be parsed: %v", err)
	}
	if got, _ := command.Flags().GetString("build-version-id"); got != buildID {
		t.Fatalf("suggested command lost the completed build: %q", got)
	}
	if got, _ := command.Flags().GetString("app-id"); got != appID {
		t.Fatalf("suggested command changed the app: %q", got)
	}
}
