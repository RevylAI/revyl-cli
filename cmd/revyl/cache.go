// Package main provides cache commands for managing remote-build caches.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage remote build caches",
	Long: `Manage the org's remote build caches (DerivedData, toolchains, codegen).

Caches are keyed disk images restored before each remote build and saved back
on success. Deleting a cache is always safe: the next build runs cold and
re-uploads a fresh one. Use delete instead of renaming cache keys in
.revyl/config.yaml when a cache needs to be discarded.`,
}

var cacheListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored build caches with size and last-use time",
	RunE:  runCacheList,
}

var cacheDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Evict a build cache so the next build runs cold",
	Args:  cobra.ExactArgs(1),
	RunE:  runCacheDelete,
}

// runCacheList handles the cache list command execution.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (none)
//
// Returns:
//   - error: Any error that occurred
func runCacheList(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.ListBuildCaches(ctx)
	if err != nil {
		ui.PrintError("Failed to list build caches: %v", err)
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		output, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(output))
		return nil
	}

	if len(resp.Caches) == 0 {
		ui.PrintInfo("No build caches stored for this org")
		return nil
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "KEY\tPLATFORM\tSIZE\tLAST USED")
	for _, cache := range resp.Caches {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n",
			cache.Key, cache.Platform,
			formatCacheSize(int64(cache.SizeBytes)),
			cache.LastModified.Format(time.RFC3339))
	}
	return writer.Flush()
}

// runCacheDelete handles the cache delete command execution.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (cache key)
//
// Returns:
//   - error: Any error that occurred
func runCacheDelete(cmd *cobra.Command, args []string) error {
	key := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ui.PrintInfo("Evicting build cache %s...", key)
	resp, err := client.DeleteBuildCache(ctx, key)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			ui.PrintError("No cache objects stored for key: %s", key)
			ui.PrintInfo("Run 'revyl cache list' to see stored cache keys")
			return fmt.Errorf("cache not found")
		}
		ui.PrintError("Failed to delete build cache: %v", err)
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		output, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(output))
		return nil
	}

	ui.PrintSuccess("Evicted %s (%d object(s) deleted); the next build runs cold",
		resp.Key, resp.DeletedObjects)
	return nil
}

// formatCacheSize renders a byte count as a human-readable size.
//
// Parameters:
//   - bytes: Object size in bytes
//
// Returns:
//   - string: Formatted size (e.g. "3.2 GB")
func formatCacheSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func init() {
	// The root command registers the persistent --dev flag; subcommands read
	// it via cmd.Flags() like the other command files do.
	cacheCmd.AddCommand(cacheListCmd)
	cacheCmd.AddCommand(cacheDeleteCmd)
}
