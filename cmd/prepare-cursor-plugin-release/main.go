// Command prepare-cursor-plugin-release generates one immutable plugin runtime pin.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/revyl/cli/internal/cursorpluginrelease"
)

const publicCLIReleaseBaseURL = "https://github.com/RevylAI/revyl-cli/releases/download"

// commandOptions contains the structured command-line inputs.
type commandOptions struct {
	PluginVersion  string
	RuntimeVersion string
	CheckOnly      bool
}

// main validates command input, prepares release files, and reports the mapping.
func main() {
	options := parseFlags()
	client := &http.Client{Timeout: 60 * time.Second}
	result, err := cursorpluginrelease.Prepare(
		context.Background(),
		cursorpluginrelease.Input{
			PluginRoot:      "cursor-plugin",
			MarketplacePath: ".cursor-plugin/marketplace.json",
			PluginVersion:   options.PluginVersion,
			RuntimeVersion:  options.RuntimeVersion,
			ReleaseBaseURL:  publicCLIReleaseBaseURL,
			CheckOnly:       options.CheckOnly,
			HTTPClient:      client,
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare Cursor plugin release: %v\n", err)
		os.Exit(1)
	}

	mode := "prepared"
	if options.CheckOnly {
		mode = "verified"
	}
	fmt.Printf(
		"Cursor plugin %s: plugin %s -> runtime %s\n",
		mode,
		result.PreparedPluginVersion,
		result.PreparedRuntimeVersion,
	)
	if len(result.ChangedFiles) == 0 {
		fmt.Println("Release metadata is current.")
		return
	}
	fmt.Println("Changed files:")
	for _, path := range result.ChangedFiles {
		fmt.Printf("  %s\n", path)
	}
}

// parseFlags reads required versions and optional check-only behavior.
//
// Returns:
//   - commandOptions: Parsed release preparation values.
func parseFlags() commandOptions {
	var options commandOptions
	flag.StringVar(
		&options.PluginVersion,
		"plugin-version",
		"",
		"Cursor plugin semantic version",
	)
	flag.StringVar(
		&options.RuntimeVersion,
		"runtime-version",
		"",
		"published Revyl CLI semantic version",
	)
	flag.BoolVar(
		&options.CheckOnly,
		"check",
		false,
		"verify generated release metadata without writing",
	)
	flag.Parse()
	if options.PluginVersion == "" || options.RuntimeVersion == "" {
		fmt.Fprintln(
			os.Stderr,
			"--plugin-version and --runtime-version are required",
		)
		flag.Usage()
		os.Exit(2)
	}
	return options
}
