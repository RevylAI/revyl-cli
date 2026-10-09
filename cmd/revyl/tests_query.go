package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func newTestsQueryCommand() *cobra.Command {
	command := &cobra.Command{Use: "query", Short: "Find tests by app/name/platform, with optional local tag-name filtering", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		if limit < 1 || limit > 100 || offset < 0 || offset > 100000 {
			return fmt.Errorf("limit must be 1-100 and offset 0-100000")
		}
		name, _ := cmd.Flags().GetString("search")
		platform, _ := cmd.Flags().GetString("platform")
		tag, _ := cmd.Flags().GetString("tag")
		appID, _ := cmd.Flags().GetString("app")
		if appID != "" {
			if _, err := uuid.Parse(appID); err != nil {
				return fmt.Errorf("app must be a UUID from atlas apps")
			}
		}
		client, err := atlasClient(cmd)
		if err != nil {
			return err
		}
		page, err := client.QueryTests(cmd.Context(), api.TestQuery{Limit: limit, Offset: offset, Search: name, Platform: platform, AppID: appID})
		if err != nil {
			return err
		}
		items := make([]api.TestWithTags, 0, len(page.Tests))
		for _, item := range page.Tests {
			match := tag == ""
			for _, value := range item.Tags {
				if strings.EqualFold(value.Name, tag) {
					match = true
				}
			}
			if match {
				items = append(items, item)
			}
		}
		nextOffset := offset + len(page.Tests)
		hasMore := len(page.Tests) == limit
		if page.TotalCount != nil {
			if len(page.Tests) == 0 && offset < *page.TotalCount {
				return fmt.Errorf("test page was empty before the reported total; refresh the query")
			}
			hasMore = nextOffset < *page.TotalCount
		}
		filterScope := "server"
		if tag != "" {
			filterScope = "server_except_tag_name"
		}
		result := struct {
			Tests        []api.TestWithTags `json:"tests"`
			ScannedCount int                `json:"scanned_count"`
			Offset       int                `json:"offset"`
			NextOffset   int                `json:"next_offset"`
			HasMore      bool               `json:"has_more"`
			Total        *int               `json:"total"`
			FilterScope  string             `json:"filter_scope"`
		}{items, len(page.Tests), offset, nextOffset, hasMore, page.TotalCount, filterScope}
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%d matching tests in %d scanned rows\n", len(items), len(page.Tests))
		for _, item := range items {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s (%s)\n", item.ID, item.Name, item.Platform)
		}
		if hasMore {
			fmt.Fprintf(cmd.OutOrStdout(), "More rows: repeat with --offset %d and the same filters\n", nextOffset)
		}
		return nil
	}}
	command.Flags().Int("limit", 25, "Rows per page before local filtering (1-100)")
	command.Flags().Int("offset", 0, "Rows to skip (0-100000)")
	command.Flags().String("search", "", "Case-insensitive test name or ID search before pagination")
	command.Flags().String("platform", "", "Platform filter before pagination")
	command.Flags().String("app", "", "App UUID filter before pagination")
	command.Flags().String("tag", "", "Tag name within this page")
	command.Flags().Bool("json", false, "Output JSON with explicit scanned-page coverage")
	return command
}
