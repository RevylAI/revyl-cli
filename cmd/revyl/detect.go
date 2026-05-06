package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/detect"
)

// detectCmd inspects the working directory and reports what the Revyl
// templater would render for it. Mirrors the backend detector exactly, so
// users can troubleshoot "why didn't the templater PR show up?" without
// having to install the GitHub App or read backend logs.
var detectCmd = &cobra.Command{
	Use:   "detect [path]",
	Short: "Detect the mobile-app stack of a repo (what the templater would do)",
	Long: `Run the same stack-detection logic the Revyl templater uses on the
GitHub App side, but locally against the directory you point it at (defaults
to the current directory).

Use this when:
  * The templater opened no PR and you want to know why
  * You're curious what configuration Revyl would generate for your repo
  * You want to commit the output as a reference for your team

If the result is "unknown", the templater would not open a PR. You can still
upload builds yourself with 'revyl build'.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDetect,
}

func init() {
	detectCmd.Flags().Bool("yaml", false, "Output as YAML instead of JSON")
	rootCmd.AddCommand(detectCmd)
}

func runDetect(cmd *cobra.Command, args []string) error {
	root := "."
	if len(args) > 0 {
		root = args[0]
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", root, err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return fmt.Errorf("path %q is not a directory", abs)
	}

	result := detect.Detect(abs)

	asJSON, _ := cmd.Flags().GetBool("json")
	asYAML, _ := cmd.Flags().GetBool("yaml")

	if asJSON || asYAML {
		b, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("Stack:             %s\n", result.Stack)
	fmt.Printf("Working directory: %s\n", result.WorkingDirectory)
	fmt.Printf("Package manager:   %s\n", result.PackageManager)
	if result.IOSWorkspace != "" {
		fmt.Printf("iOS workspace:     %s\n", result.IOSWorkspace)
	}
	if result.IOSScheme != "" {
		fmt.Printf("iOS scheme:        %s\n", result.IOSScheme)
	}
	fmt.Printf("iOS pods:          %t\n", result.IOSPods)
	fmt.Printf("Android module:    %s\n", result.AndroidModule)
	if len(result.Notes) > 0 {
		fmt.Println("\nNotes:")
		for _, n := range result.Notes {
			fmt.Printf("  - %s\n", n)
		}
	}

	if !result.Actionable() {
		fmt.Fprintln(os.Stderr,
			"\nWe couldn't find a default configuration for this repo, but you "+
				"can still upload any builds you have via `revyl build`.")
		os.Exit(1)
	}
	return nil
}
