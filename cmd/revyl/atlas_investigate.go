package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

type atlasScopedGraph struct {
	Nodes        []map[string]interface{} `json:"nodes"`
	Edges        []map[string]interface{} `json:"edges"`
	Projection   map[string]interface{}   `json:"projection"`
	Truncated    bool                     `json:"truncated"`
	Completeness string                   `json:"completeness"`
}

type atlasReportMembership struct {
	ReportID  string           `json:"report_id"`
	ScreenIDs []string         `json:"screen_ids"`
	Mapping   string           `json:"mapping"`
	Graph     atlasScopedGraph `json:"graph"`
}

func newAtlasReportsCommand() *cobra.Command {
	command := &cobra.Command{Use: "from-reports", Short: "Project up to five selected reports into Atlas, preserving unmapped evidence", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		appID, _ := cmd.Flags().GetString("app")
		reports, _ := cmd.Flags().GetStringSlice("reports")
		limit, _ := cmd.Flags().GetInt("limit")
		if _, err := uuid.Parse(appID); err != nil {
			return fmt.Errorf("app must be a UUID")
		}
		if len(reports) < 1 || len(reports) > 5 || limit < 1 || limit > 100 {
			return fmt.Errorf("provide 1-5 report UUIDs and a limit of 1-100")
		}
		seen := map[string]bool{}
		for _, id := range reports {
			if _, err := uuid.Parse(id); err != nil || seen[id] {
				return fmt.Errorf("reports must contain distinct UUIDs")
			}
			seen[id] = true
		}
		client, err := atlasClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 55*time.Second)
		defer cancel()
		memberships := make([]atlasReportMembership, 0, len(reports))
		for _, id := range reports {
			graph, err := readInvestigationGraph(ctx, client, appID, "", id, limit)
			if err != nil {
				return err
			}
			ids := []string{}
			for _, node := range graph.Nodes {
				ids = append(ids, atlasString(node, "id"))
			}
			mapping := "mapped"
			if len(ids) == 0 {
				mapping = "no_matching_atlas_evidence"
			}
			if graph.Completeness != "complete" {
				mapping = "partial"
			}
			memberships = append(memberships, atlasReportMembership{ReportID: id, ScreenIDs: ids, Mapping: mapping, Graph: graph})
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Contract       string                  `json:"contract"`
			AppID          string                  `json:"app_id"`
			Reports        []atlasReportMembership `json:"reports"`
			Interpretation string                  `json:"interpretation"`
		}{"atlas_report_projection.v1", appID, memberships, "Membership covers each selected report, not just its failing step. Unmapped reports remain in the result. Empty mapping does not prove the report exists or has no failure; inspect its source report."})
	}}
	command.Flags().String("app", "", "App UUID")
	command.Flags().StringSlice("reports", nil, "Selected report UUIDs (1-5); execution IDs are different")
	command.Flags().Int("limit", 50, "Maximum nodes per report (1-100)")
	command.Flags().Bool("json", false, "Output JSON (always enabled for this composition)")
	_ = command.MarkFlagRequired("app")
	_ = command.MarkFlagRequired("reports")
	return command
}

func readInvestigationGraph(ctx context.Context, client *api.Client, appID, buildID, reportID string, limit int) (atlasScopedGraph, error) {
	details, flows, variants := false, false, true
	graph, err := client.GetAtlasGraph(ctx, api.AtlasQuery{AppID: appID, BuildID: buildID, ReportID: reportID, Limit: limit, IncludeDetails: &details, IncludeFlows: &flows, IncludeVariants: &variants, Visibility: "included"})
	if err != nil {
		return atlasScopedGraph{}, err
	}
	rawNodes, ok := graph["nodes"].([]interface{})
	if !ok {
		return atlasScopedGraph{}, fmt.Errorf("Atlas graph omitted its node collection")
	}
	rawEdges, ok := graph["edges"].([]interface{})
	if !ok {
		return atlasScopedGraph{}, fmt.Errorf("Atlas graph omitted its edge collection")
	}
	nodes := atlasMaps(graph["nodes"])
	edges := atlasMaps(graph["edges"])
	if len(nodes) != len(rawNodes) || len(edges) != len(rawEdges) {
		return atlasScopedGraph{}, fmt.Errorf("Atlas graph returned malformed nodes or relationships")
	}
	for _, node := range nodes {
		if atlasString(node, "id") == "" {
			return atlasScopedGraph{}, fmt.Errorf("Atlas graph returned a screen without identity")
		}
	}
	for _, edge := range edges {
		if atlasString(edge, "source_entity_id") == "" || atlasString(edge, "target_entity_id") == "" {
			return atlasScopedGraph{}, fmt.Errorf("Atlas graph returned a relationship without endpoints")
		}
	}
	result := atlasScopedGraph{Nodes: atlasAgentScreens(nodes, len(nodes)), Edges: make([]map[string]interface{}, 0, len(edges)), Projection: atlasProjectionContract(graph), Truncated: atlasBool(atlasMap(graph["projection"])["truncated"]) || atlasBool(graph["truncated"])}
	_, known := atlasMap(graph["projection"])["truncated"].(bool)
	result.Completeness = "unknown"
	if result.Truncated {
		result.Completeness = "truncated"
	} else if known {
		result.Completeness = "complete"
	}
	index := atlasNodeIndex(graph)
	for _, edge := range edges {
		result.Edges = append(result.Edges, atlasAgentEdge(edge, index))
	}
	return result, nil
}

type atlasEvidenceDifference struct {
	Both     []string `json:"observed_in_both"`
	BaseOnly []string `json:"observed_only_in_base"`
	HeadOnly []string `json:"observed_only_in_head"`
}

func compareAtlasIDs(base, head []map[string]interface{}, key string) atlasEvidenceDifference {
	left, right := map[string]bool{}, map[string]bool{}
	for _, item := range base {
		left[atlasString(item, key)] = true
	}
	for _, item := range head {
		right[atlasString(item, key)] = true
	}
	result := atlasEvidenceDifference{Both: []string{}, BaseOnly: []string{}, HeadOnly: []string{}}
	for id := range left {
		if right[id] {
			result.Both = append(result.Both, id)
		} else {
			result.BaseOnly = append(result.BaseOnly, id)
		}
	}
	for id := range right {
		if !left[id] {
			result.HeadOnly = append(result.HeadOnly, id)
		}
	}
	sort.Strings(result.Both)
	sort.Strings(result.BaseOnly)
	sort.Strings(result.HeadOnly)
	return result
}

func newAtlasCompareCommand() *cobra.Command {
	command := &cobra.Command{Use: "compare", Short: "Compare observed Atlas screen and transition identities across two builds", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		appID, _ := cmd.Flags().GetString("app")
		baseID, _ := cmd.Flags().GetString("base-build")
		headID, _ := cmd.Flags().GetString("head-build")
		limit, _ := cmd.Flags().GetInt("limit")
		for _, id := range []string{appID, baseID, headID} {
			if _, err := uuid.Parse(id); err != nil {
				return fmt.Errorf("app and build references must be UUIDs")
			}
		}
		if baseID == headID || limit < 1 || limit > 100 {
			return fmt.Errorf("select different builds and a limit of 1-100")
		}
		client, err := atlasClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 55*time.Second)
		defer cancel()
		base, err := readInvestigationGraph(ctx, client, appID, baseID, "", limit)
		if err != nil {
			return err
		}
		head, err := readInvestigationGraph(ctx, client, appID, headID, "", limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Contract       string                  `json:"contract"`
			AppID          string                  `json:"app_id"`
			BaseBuildID    string                  `json:"base_build_id"`
			HeadBuildID    string                  `json:"head_build_id"`
			Base           atlasScopedGraph        `json:"base"`
			Head           atlasScopedGraph        `json:"head"`
			Screens        atlasEvidenceDifference `json:"screens"`
			Transitions    atlasEvidenceDifference `json:"transitions"`
			Partial        bool                    `json:"partial"`
			Interpretation string                  `json:"interpretation"`
		}{"atlas_build_comparison.v1", appID, baseID, headID, base, head, compareAtlasIDs(base.Nodes, head.Nodes, "id"), compareAtlasIDs(base.Edges, head.Edges, "edge_key"), base.Completeness != "complete" || head.Completeness != "complete", "This compares current Atlas identities supported by each build's captured evidence. Shared identities are candidates, not verified semantic or pixel matches; inspect exact captures for possible over-grouping. Unobserved does not mean removed. Incomplete sides cannot establish absence."})
	}}
	command.Flags().String("app", "", "App UUID")
	command.Flags().String("base-build", "", "Baseline build UUID")
	command.Flags().String("head-build", "", "Comparison build UUID")
	command.Flags().Int("limit", 100, "Maximum nodes per build (1-100)")
	command.Flags().Bool("json", false, "Output JSON (always enabled for this composition)")
	for _, name := range []string{"app", "base-build", "head-build"} {
		_ = command.MarkFlagRequired(name)
	}
	return command
}
