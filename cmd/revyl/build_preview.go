package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// urlUploadJSON is the machine-readable payload for `revyl build upload --url --json`.
type urlUploadJSON struct {
	Platform    string `json:"platform"`
	AppID       string `json:"app_id"`
	Version     string `json:"version"`
	VersionID   string `json:"version_id"`
	PackageName string `json:"package_name"`
	WasReused   bool   `json:"was_reused"`
	SourceURL   string `json:"source_url"`
	PreviewURL  string `json:"preview_url,omitempty"`
}

// buildPreviewURL constructs the click-to-launch preview URL for a build.
//
// Parameters:
//   - devMode: When true, resolve the app URL with the CLI --dev routing.
//   - buildID: Uploaded build version UUID.
//
// Returns:
//   - string: Absolute /sessions/launch?buildId= URL, or empty when buildID is blank.
func buildPreviewURL(devMode bool, buildID string) string {
	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return ""
	}
	query := url.Values{}
	query.Set("buildId", buildID)
	return strings.TrimRight(config.GetAppURL(devMode), "/") + "/sessions/launch?" + query.Encode()
}

// applyUploadPreview prints a preview URL when --preview is set.
//
// Parameters:
//   - cmd: Cobra command used for --dev routing.
//   - buildID: Uploaded build version UUID.
//
// Returns:
//   - string: Preview URL when --preview produced one, otherwise empty.
func applyUploadPreview(cmd *cobra.Command, buildID string) string {
	if !uploadPreviewFlag {
		return ""
	}
	devMode := false
	if cmd != nil {
		devMode, _ = cmd.Flags().GetBool("dev")
	}
	previewURL := buildPreviewURL(devMode, buildID)
	if previewURL == "" {
		return ""
	}
	if !buildUploadJSON {
		ui.PrintLink("Preview", previewURL)
	}
	return previewURL
}

// outputURLUploadJSON prints the original flat URL-upload JSON contract.
//
// Parameters:
//   - payload: URL-upload fields plus optional preview_url.
func outputURLUploadJSON(payload urlUploadJSON) {
	data, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Println(string(data))
}
