package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	mcppkg "github.com/revyl/cli/internal/mcp"
)

func TestResolveTargetOrCoordsRefusesAPointOffTheRecordedScreen(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("target", "", "")
	cmd.Flags().Int("x", 0, "")
	cmd.Flags().Int("y", 0, "")
	if err := cmd.Flags().Parse([]string{"--x", "600", "--y", "1950"}); err != nil {
		t.Fatal(err)
	}
	session := &mcppkg.DeviceSession{Index: mcppkg.UnattachedSessionIndex, Platform: "ios", ScreenWidth: 402, ScreenHeight: 874}

	_, _, _, err := resolveTargetOrCoords(cmd, mcppkg.NewDeviceSessionManager(nil, ""), session)

	if err == nil || !strings.Contains(err.Error(), "(600, 1950) is outside the 402x874 iOS screen; iOS coordinates are points") {
		t.Fatalf("resolveTargetOrCoords() error = %v, want the off-screen refusal", err)
	}
}
