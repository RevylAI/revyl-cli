package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

// devComputerTarget is the computer a dev loop's device runs inside; an empty
// SessionID means a standalone cloud device.
type devComputerTarget struct {
	SessionID string
	// Local reports that the dev loop itself runs inside that computer. Only
	// an iOS simulator shares the computer's loopback; Android runs on its own
	// network and keeps the relay.
	Local bool
}

func resolveDevComputerTarget(cmd *cobra.Command) devComputerTarget {
	sessionID := computerSessionIDForStart(cmd)
	return devComputerTarget{
		SessionID: sessionID,
		Local:     sessionID != "" && strings.EqualFold(sessionID, strings.TrimSpace(os.Getenv("REVYL_COMPUTER_ID"))),
	}
}

// startOptions places a dev-loop start inside the computer. The computer
// installs the selected build and launches it with its defaults, so the
// standalone artifact URL, boot link and install skip do not apply.
func (t devComputerTarget) startOptions(opts mcppkg.StartSessionOptions) mcppkg.StartSessionOptions {
	if t.SessionID == "" {
		return opts
	}
	opts.ComputerSessionID = t.SessionID
	opts.AppURL, opts.AppPackage, opts.AppLink, opts.SkipAppInstall = "", "", "", false
	return opts
}

// requireDevComputerHosts refuses a dev loop whose computer cannot host the
// platform's device right now, before any build, dev server or session starts.
func requireDevComputerHosts(ctx context.Context, client *api.Client, computerSessionID, platform string) error {
	id, err := uuid.Parse(computerSessionID)
	if err != nil {
		return fmt.Errorf("--computer must be a computer ID or none")
	}
	hosting, err := client.GetComputerHosting(ctx, id)
	if err != nil {
		return fmt.Errorf("read what computer %s can host: %w; check it with `revyl computer get %s`, or pass --computer=none for a standalone device", computerSessionID, err, computerSessionID)
	}
	if len(hosting.Platforms) == 0 {
		return fmt.Errorf("computer %s isn't running, so it can't host a device; start one with `revyl computer create --os macos --wait`, or pass --computer=none for a standalone device", computerSessionID)
	}
	if slices.Contains(hosting.Platforms, api.ComputerHostingCapabilityPlatforms(platform)) {
		return nil
	}
	hosted := make([]string, 0, len(hosting.Platforms))
	for _, hostedPlatform := range hosting.Platforms {
		hosted = append(hosted, string(hostedPlatform))
	}
	return fmt.Errorf("computer %s can't host %s devices (it hosts %s); pass --computer=none for a standalone device", computerSessionID, platform, strings.Join(hosted, ", "))
}

func printDevDeviceStart(computer devComputerTarget) {
	if computer.SessionID == "" {
		ui.PrintInfo("Starting cloud device session...")
		return
	}
	ui.PrintInfo("Starting device inside computer %s (no standalone device)...", computer.SessionID)
}
