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
	Bump           string
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
			CLIVersionPath:  "VERSION",
			ReleaseBaseURL:  publicCLIReleaseBaseURL,
			Bump:            options.Bump,
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
	fmt.Println("Generator-owned files:")
	fmt.Println("  cursor-plugin/.cursor-plugin/plugin.json")
	fmt.Println("  .cursor-plugin/marketplace.json")
	fmt.Println("  cursor-plugin/runtime-manifest.json")
	if len(result.ChangedFiles) == 0 {
		fmt.Println("Release metadata is current.")
		return
	}
	fmt.Println("Changed files:")
	for _, path := range result.ChangedFiles {
		fmt.Printf("  %s\n", path)
	}
}

// parseFlags reads optional versions, bump level, and check-only behavior.
//
// Omitted --plugin-version increments plugin.json by --bump (default patch),
// or keeps the current version during --check. Omitted --runtime-version uses
// VERSION.
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
	flag.StringVar(
		&options.Bump,
		"bump",
		"",
		"plugin semver component to increment when --plugin-version is omitted (patch, minor, major)",
	)
	flag.BoolVar(
		&options.CheckOnly,
		"check",
		false,
		"verify generated release metadata without writing",
	)
	flag.Parse()
	return options
}
