// Package main provides the session command group.
package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var (
	sessionShareOutputJSON bool
	sessionShareOpen       bool
	sessionShareExpires    string
)

// sessionCmd is the parent command for device-session operations.
var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Work with device sessions",
	Long: `Work with device sessions.

COMMANDS:
  share - Generate a public shareable link for a session report`,
}

// sessionShareCmd generates a public shareable link for a device session report.
var sessionShareCmd = &cobra.Command{
	Use:   "share <sessionId>",
	Short: "Generate shareable session report link",
	Long: `Generate a public shareable link for a device session report.

The recipient sees the session recording, step breakdown, and results without
logging in. This is the public report view, not the internal /sessions/<id>
page (which requires being signed in to the same organization).

Examples:
  revyl session share d437c539-8e4d-45cb-aad9-5f88dca32cc7
  revyl session share <sessionId> --expires 30d --open
  revyl session share <sessionId> --json`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionShare,
}

func init() {
	sessionShareCmd.Flags().BoolVar(&sessionShareOutputJSON, "json", false, "Output results as JSON")
	sessionShareCmd.Flags().BoolVar(&sessionShareOpen, "open", false, "Open shareable link in browser")
	sessionShareCmd.Flags().StringVar(&sessionShareExpires, "expires", "", "Link expiry, e.g. 24h, 30d, 4w (default: never)")

	sessionCmd.AddCommand(sessionShareCmd)
}

// runSessionShare generates a shareable link for a device session report.
func runSessionShare(cmd *cobra.Command, args []string) error {
	jsonOutput := sessionShareOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	sessionID := strings.TrimSpace(args[0])
	if !looksLikeUUID(sessionID) {
		ui.PrintError("Invalid session id %q (expected a UUID, e.g. from app.revyl.ai/sessions/<id>)", sessionID)
		return fmt.Errorf("invalid session id")
	}

	expirationHours, err := parseExpirationFlag(sessionShareExpires)
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	} else {
		ui.StartSpinner("Generating shareable link...")
	}

	shareResp, err := client.GenerateShareableSessionLink(cmd.Context(), sessionID, config.GetAppURL(devMode), expirationHours)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to generate shareable link: %v", err)
		return err
	}

	if jsonOutput {
		output := map[string]interface{}{
			"session_id":     sessionID,
			"shareable_link": shareResp.ShareableLink,
		}
		data, _ := marshalPrettyJSON(output)
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Shareable link generated")
	ui.Println()
	ui.PrintLink("Link", shareResp.ShareableLink)

	if sessionShareOpen {
		ui.OpenBrowser(shareResp.ShareableLink)
	}

	return nil
}
