package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var atlasCmd = &cobra.Command{
	Use:   "atlas",
	Short: "Inspect app Atlases",
	Long: `Inspect Revyl Atlas maps from the CLI.

Start with:
  revyl atlas apps
  revyl atlas map --app "My App"
  revyl atlas graph --app "My App" --json
  revyl atlas overview --app "My App"
  revyl atlas search "checkout error" --app "My App"
  revyl atlas screen <screen-id> --app "My App" --screenshots --screenshot-dir /tmp/atlas-shots`,
	Args: cobra.NoArgs,
	RunE: runAtlasGuide,
}

var atlasAppsCmd = &cobra.Command{
	Use:   "apps",
	Short: "List apps that can have Atlases",
	RunE:  runAtlasApps,
}

var atlasOverviewCmd = &cobra.Command{
	Use:   "overview",
	Short: "Show an Atlas overview for an app",
	RunE:  runAtlasOverview,
}

var atlasMapCmd = &cobra.Command{
	Use:   "map",
	Short: "Show a top-down Atlas structure summary",
	RunE:  runAtlasMap,
}

var atlasGraphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Show the exact Atlas graph payload used by the frontend",
	RunE:  runAtlasGraph,
}

var atlasOpenCmd = &cobra.Command{
	Use:   "open",
	Short: "Open an app Atlas in the browser",
	RunE:  runAtlasOpen,
}

var atlasScreenCmd = &cobra.Command{
	Use:   "screen <screen-id>",
	Short: "Inspect one Atlas screen",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasScreen,
}

var atlasObservationsCmd = &cobra.Command{
	Use:   "observations <screen-id>",
	Short: "List grouped screenshots for a screen",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasObservations,
}

var atlasObservationCmd = &cobra.Command{
	Use:   "observation <observation-id>",
	Short: "Inspect one Atlas observation screenshot",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasObservation,
}

var atlasNeighborsCmd = &cobra.Command{
	Use:   "neighbors <screen-id>",
	Short: "Show neighboring Atlas screens",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasNeighbors,
}

var atlasSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search Atlas screens",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasSearch,
}

var atlasCandidatesCmd = &cobra.Command{
	Use:   "candidates <screen-id>",
	Short: "Show Atlas candidates and match decisions",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasCandidates,
}

var (
	atlasApp                 string
	atlasBuild               string
	atlasFrom                string
	atlasTo                  string
	atlasSince               string
	atlasReportID            string
	atlasTestID              string
	atlasWorkflowExecutionID string
	atlasSourceKind          string
	atlasSurfaceScope        string
	atlasVisibility          string
	atlasIncludeVariants     bool
	atlasLimit               int
	atlasJSON                bool
	atlasOpenBrowser         bool
	atlasDirection           string
	atlasAppsSearch          string
	atlasScreenshots         bool
	atlasScreenshotDir       string
)

const atlasStructureFetchLimit = 100

func init() {
	atlasCmd.AddCommand(
		atlasAppsCmd,
		atlasOverviewCmd,
		atlasMapCmd,
		atlasGraphCmd,
		atlasOpenCmd,
		atlasScreenCmd,
		atlasObservationsCmd,
		atlasObservationCmd,
		atlasNeighborsCmd,
		atlasSearchCmd,
		atlasCandidatesCmd,
	)
	for _, cmd := range []*cobra.Command{
		atlasOverviewCmd,
		atlasMapCmd,
		atlasGraphCmd,
		atlasOpenCmd,
		atlasScreenCmd,
		atlasObservationsCmd,
		atlasObservationCmd,
		atlasNeighborsCmd,
		atlasSearchCmd,
		atlasCandidatesCmd,
	} {
		cmd.Flags().StringVar(&atlasApp, "app", "", "App name or app id")
		cmd.Flags().StringVar(&atlasBuild, "build", "all", "Build id, build version, latest, or all")
		cmd.Flags().StringVar(&atlasFrom, "from", "", "Start time filter (ISO timestamp)")
		cmd.Flags().StringVar(&atlasTo, "to", "", "End time filter (ISO timestamp)")
		cmd.Flags().StringVar(&atlasSince, "since", "", "Relative start time hint, such as 7d (sent as-is if backend supports it)")
		cmd.Flags().StringVar(&atlasReportID, "report-id", "", "Filter to one report")
		cmd.Flags().StringVar(&atlasTestID, "test-id", "", "Filter to one test")
		cmd.Flags().StringVar(&atlasWorkflowExecutionID, "workflow-execution-id", "", "Filter to one workflow execution")
		cmd.Flags().StringVar(&atlasSourceKind, "source-kind", "", "Filter by Atlas source kind")
		cmd.Flags().StringVar(&atlasSurfaceScope, "surface-scope", "app", "Surface scope: app, app+system, app+external, all")
		cmd.Flags().StringVar(&atlasVisibility, "visibility", "included", "Visibility: included or included+excluded_debug")
		cmd.Flags().BoolVar(&atlasIncludeVariants, "include-variants", false, "Include variant nodes where supported")
		cmd.Flags().IntVar(&atlasLimit, "limit", 20, "Maximum results to return")
		cmd.Flags().BoolVar(&atlasJSON, "json", false, "Output raw JSON")
		cmd.Flags().BoolVar(&atlasOpenBrowser, "open", false, "Open the focused Atlas viewer URL in the browser")
		cmd.Flags().BoolVar(&atlasScreenshots, "screenshots", false, "Include presigned screenshot URLs for visual inspection")
		cmd.Flags().StringVar(&atlasScreenshotDir, "screenshot-dir", "", "Download Atlas screenshots into this directory and add local_screenshot_path fields")
	}
	atlasAppsCmd.Flags().StringVar(&appListPlatform, "platform", "", "Filter by platform (android, ios)")
	atlasAppsCmd.Flags().StringVar(&atlasAppsSearch, "search", "", "Search by app name")
	atlasAppsCmd.Flags().BoolVar(&atlasJSON, "json", false, "Output raw JSON")
	atlasNeighborsCmd.Flags().StringVar(&atlasDirection, "direction", "both", "Neighbor direction: both, in, or out")
	_ = atlasOverviewCmd.MarkFlagRequired("app")
	_ = atlasMapCmd.MarkFlagRequired("app")
	_ = atlasGraphCmd.MarkFlagRequired("app")
	_ = atlasOpenCmd.MarkFlagRequired("app")
	_ = atlasScreenCmd.MarkFlagRequired("app")
	_ = atlasObservationsCmd.MarkFlagRequired("app")
	_ = atlasObservationCmd.MarkFlagRequired("app")
	_ = atlasNeighborsCmd.MarkFlagRequired("app")
	_ = atlasSearchCmd.MarkFlagRequired("app")
	_ = atlasCandidatesCmd.MarkFlagRequired("app")
}

func runAtlasGuide(cmd *cobra.Command, args []string) error {
	ui.PrintInfo("Start with one of these:")
	ui.PrintDim("  revyl atlas apps")
	ui.PrintDim("  revyl atlas overview --app \"My App\"")
	ui.PrintDim("  revyl atlas map --app \"My App\"")
	ui.PrintDim("  revyl atlas graph --app \"My App\" --json")
	ui.PrintDim("  revyl atlas search \"checkout error\" --app \"My App\"")
	ui.Println()
	ui.PrintInfo("Then inspect and traverse:")
	ui.PrintDim("  revyl atlas screen <screen-id> --app \"My App\" --open")
	ui.PrintDim("  revyl atlas screen <screen-id> --app \"My App\" --screenshots --screenshot-dir /tmp/atlas-shots")
	ui.PrintDim("  revyl atlas observations <screen-id> --app \"My App\"")
	ui.PrintDim("  revyl atlas neighbors <screen-id> --app \"My App\"")
	return nil
}

func atlasClient(cmd *cobra.Command) (*api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	return api.NewClientWithDevMode(apiKey, devMode), nil
}

func resolveAtlasApp(cmd *cobra.Command, client *api.Client, app string) (*api.App, error) {
	if app == "" {
		return nil, fmt.Errorf("--app is required")
	}
	if parsed, err := uuid.Parse(app); err == nil && strings.EqualFold(app, parsed.String()) {
		return &api.App{ID: parsed.String(), Name: parsed.String()}, nil
	}
	result, err := client.SearchApps(cmd.Context(), app, "", 10)
	if err != nil {
		return nil, err
	}
	apps := result.Items
	lower := strings.ToLower(app)
	var exact []api.App
	var fuzzy []api.App
	for _, item := range apps {
		if item.ID == app || strings.EqualFold(item.Name, app) {
			exact = append(exact, item)
			continue
		}
		if strings.Contains(strings.ToLower(item.Name), lower) {
			fuzzy = append(fuzzy, item)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = fuzzy
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	if len(matches) > 1 {
		ui.PrintError("App %q is ambiguous. Use one of these exact app ids:", app)
		for _, match := range matches {
			ui.PrintDim("  revyl atlas overview --app %s    # %s (%s)", match.ID, match.Name, match.Platform)
		}
		return nil, fmt.Errorf("ambiguous app")
	}
	ui.PrintError("App %q not found", app)
	ui.PrintInfo("Run 'revyl atlas apps' to list apps.")
	return nil, fmt.Errorf("app not found")
}

func resolveAtlasBuild(cmd *cobra.Command, client *api.Client, appID string, build string) (string, error) {
	if build == "" || build == "latest" {
		latest, err := client.GetLatestBuildVersion(cmd.Context(), appID)
		if err != nil {
			return "", err
		}
		if latest == nil {
			return "", nil
		}
		return latest.ID, nil
	}
	if build == "all" {
		return "", nil
	}
	versions, err := client.ListBuildVersions(cmd.Context(), appID)
	if err != nil {
		return "", err
	}
	for _, version := range versions {
		if version.ID == build || version.Version == build {
			return version.ID, nil
		}
	}
	return build, nil
}

func atlasQueryFor(cmd *cobra.Command, client *api.Client) (api.AtlasQuery, *api.App, error) {
	app, err := resolveAtlasApp(cmd, client, atlasApp)
	if err != nil {
		return api.AtlasQuery{}, nil, err
	}
	buildID, err := resolveAtlasBuild(cmd, client, app.ID, atlasBuild)
	if err != nil {
		return api.AtlasQuery{}, nil, err
	}
	fromTime := atlasFrom
	if fromTime == "" && atlasSince != "" {
		fromTime = atlasSinceToTime(atlasSince)
	}
	return api.AtlasQuery{
		AppID:               app.ID,
		BuildID:             buildID,
		ReportID:            atlasReportID,
		TestID:              atlasTestID,
		WorkflowExecutionID: atlasWorkflowExecutionID,
		SourceKind:          atlasSourceKind,
		FromTime:            fromTime,
		ToTime:              atlasTo,
		SurfaceScope:        atlasSurfaceScope,
		Visibility:          atlasVisibility,
		IncludeVariants:     atlasIncludeVariants,
		Limit:               atlasLimit,
		Direction:           atlasDirection,
		IncludeScreenshots:  atlasScreenshots || atlasScreenshotDir != "",
	}, app, nil
}

func atlasSinceToTime(value string) string {
	text := strings.TrimSpace(strings.ToLower(value))
	if len(text) < 2 {
		return value
	}
	unit := text[len(text)-1]
	countText := text[:len(text)-1]
	var count int
	if _, err := fmt.Sscanf(countText, "%d", &count); err != nil || count <= 0 {
		return value
	}
	var duration time.Duration
	switch unit {
	case 'h':
		duration = time.Duration(count) * time.Hour
	case 'd':
		duration = time.Duration(count) * 24 * time.Hour
	default:
		return value
	}
	return time.Now().Add(-duration).UTC().Format(time.RFC3339)
}

func atlasJSONOutput(cmd *cobra.Command) bool {
	globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
	return atlasJSON || globalJSON
}

func printAtlasResponse(cmd *cobra.Command, title string, response api.AtlasResponse) error {
	if err := materializeAtlasScreenshots(response); err != nil {
		return err
	}
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(response, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	if summary, _ := response["summary"].(string); summary != "" {
		ui.PrintInfo("%s", summary)
	} else {
		ui.PrintInfo("%s", title)
	}
	printAtlasURL("Viewer", response["viewer_url"])
	printAtlasScreens(response["top_screens"])
	printAtlasScreens(response["results"])
	if screen, ok := response["screen"].(map[string]interface{}); ok {
		printAtlasScreen(screen)
	}
	printAtlasGroups(response["groups"])
	printAtlasGroups(response["observation_groups"])
	printAtlasNeighbors(response["neighbors"])
	printAtlasFlows(response["flows"])
	printAtlasCandidates(response["candidates"])
	printAtlasNext(response["next_actions"])
	return nil
}

func materializeAtlasScreenshots(value interface{}) error {
	if strings.TrimSpace(atlasScreenshotDir) == "" {
		return nil
	}
	if err := os.MkdirAll(atlasScreenshotDir, 0o755); err != nil {
		return err
	}
	seen := map[string]string{}
	return materializeAtlasScreenshotsValue(value, seen)
}

func materializeAtlasScreenshotsValue(value interface{}, seen map[string]string) error {
	switch typed := value.(type) {
	case api.AtlasResponse:
		return materializeAtlasScreenshotsValue(map[string]interface{}(typed), seen)
	case map[string]interface{}:
		if rawURL := atlasString(typed, "screenshot_url"); rawURL != "" {
			path, err := downloadAtlasScreenshot(rawURL, seen)
			if err != nil {
				return err
			}
			typed["local_screenshot_path"] = path
		}
		for _, child := range typed {
			if err := materializeAtlasScreenshotsValue(child, seen); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range typed {
			if err := materializeAtlasScreenshotsValue(child, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func downloadAtlasScreenshot(rawURL string, seen map[string]string) (string, error) {
	if path, ok := seen[rawURL]; ok {
		return path, nil
	}
	sum := sha1.Sum([]byte(rawURL))
	initialExt := atlasScreenshotExtension(rawURL, "")
	filename := fmt.Sprintf("atlas-%x%s", sum[:8], initialExt)
	path := filepath.Join(atlasScreenshotDir, filename)
	if _, err := os.Stat(path); err == nil {
		seen[rawURL] = path
		return path, nil
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download screenshot: %s", resp.Status)
	}
	finalExt := atlasScreenshotExtension(rawURL, resp.Header.Get("Content-Type"))
	if finalExt != initialExt {
		filename = fmt.Sprintf("atlas-%x%s", sum[:8], finalExt)
		path = filepath.Join(atlasScreenshotDir, filename)
		if _, err := os.Stat(path); err == nil {
			seen[rawURL] = path
			return path, nil
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := io.Copy(file, io.LimitReader(resp.Body, 20<<20)); err != nil {
		return "", err
	}
	seen[rawURL] = path
	return path, nil
}

func atlasScreenshotExtension(rawURL string, contentType string) string {
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
			switch exts[0] {
			case ".jpg", ".jpeg", ".png", ".webp", ".gif":
				return exts[0]
			}
		}
	}
	if parsed, err := url.Parse(rawURL); err == nil {
		switch ext := strings.ToLower(filepath.Ext(parsed.Path)); ext {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif":
			return ext
		}
	}
	return ".img"
}

func printAtlasURL(label string, value interface{}) {
	if url, ok := value.(string); ok && url != "" {
		ui.PrintLink(label, url)
	}
}

func printAtlasScreens(value interface{}) {
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Screens:")
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		printAtlasScreen(item)
	}
}

func printAtlasScreen(item map[string]interface{}) {
	id := atlasString(item, "id")
	label := atlasString(item, "label")
	if label == "" {
		label = atlasString(item, "semantic_name")
	}
	if label == "" {
		label = id
	}
	ui.PrintDim("  %s  %s", id, label)
	printAtlasURL("    screenshot", item["screenshot_url"])
	printAtlasURL("    viewer", item["viewer_url"])
}

func printAtlasGroups(value interface{}) {
	groups, ok := value.(map[string]interface{})
	if !ok || len(groups) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Screenshot groups:")
	for name, raw := range groups {
		items, ok := raw.([]interface{})
		if !ok || len(items) == 0 {
			continue
		}
		ui.PrintDim("  %s:", name)
		for i, itemRaw := range items {
			if i >= 4 {
				ui.PrintDim("    ... %d more", len(items)-i)
				break
			}
			item, _ := itemRaw.(map[string]interface{})
			observationID := atlasString(item, "observation_id")
			if observationID == "" {
				observationID = atlasString(item, "id")
			}
			ui.PrintDim("    %s", observationID)
			printAtlasURL("      screenshot", item["screenshot_url"])
			printAtlasURL("      viewer", item["viewer_url"])
		}
	}
}

func printAtlasNeighbors(value interface{}) {
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Neighbors:")
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		screen, _ := item["screen"].(map[string]interface{})
		ui.PrintDim("  %s via %s", atlasString(item, "direction"), atlasEdgeLabel(item["edge"]))
		if screen != nil {
			printAtlasScreen(screen)
		}
	}
}

func printAtlasFlows(value interface{}) {
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Flows:")
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		ui.PrintDim("  %s  %s  support=%v", atlasString(item, "id"), atlasString(item, "label"), item["support"])
		printAtlasURL("    viewer", item["viewer_url"])
	}
}

func printAtlasCandidates(value interface{}) {
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Candidates:")
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		ui.PrintDim("  %s", atlasString(item, "candidate_entity_id"))
		if screen, _ := item["screen"].(map[string]interface{}); screen != nil {
			printAtlasScreen(screen)
		}
	}
}

func printAtlasNext(value interface{}) {
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Next:")
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			ui.PrintDim("  %s", text)
		}
	}
}

func atlasString(item map[string]interface{}, key string) string {
	if item == nil {
		return ""
	}
	if value, ok := item[key].(string); ok {
		return value
	}
	return ""
}

func atlasEdgeLabel(value interface{}) string {
	edge, _ := value.(map[string]interface{})
	if edge == nil {
		return "transition"
	}
	if label := atlasString(edge, "action_label"); label != "" {
		return label
	}
	if label := atlasString(edge, "action_type"); label != "" {
		return label
	}
	return "transition"
}

func maybeOpenAtlas(cmd *cobra.Command, response api.AtlasResponse) {
	if !atlasOpenBrowser {
		return
	}
	url, _ := response["viewer_url"].(string)
	if url == "" {
		if screen, ok := response["screen"].(map[string]interface{}); ok {
			url, _ = screen["viewer_url"].(string)
		}
	}
	if url == "" {
		return
	}
	ui.PrintInfo("Opening Atlas...")
	ui.PrintLink("Atlas", url)
	if err := ui.OpenBrowser(url); err != nil {
		ui.PrintWarning("Could not open browser: %v", err)
	}
}

func runAtlasApps(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	result, err := client.SearchApps(cmd.Context(), atlasAppsSearch, appListPlatform, 50)
	if err != nil {
		return err
	}
	apps := result.Items
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"apps":     apps,
			"count":    len(apps),
			"total":    result.Total,
			"page":     result.Page,
			"has_next": result.HasNext,
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	ui.PrintInfo("Apps (%d shown, %d total):", len(apps), result.Total)
	for _, app := range apps {
		ui.PrintDim("  %s  %s (%s)", app.ID, app.Name, app.Platform)
		ui.PrintDim("    revyl atlas overview --app %s", app.ID)
	}
	if result.HasNext {
		ui.PrintDim("  Use revyl app list --search <name> to narrow results.")
	}
	return nil
}

func runAtlasOverview(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	resp, err := client.GetAtlasOverview(cmd.Context(), query)
	if err != nil {
		return err
	}
	maybeOpenAtlas(cmd, resp)
	return printAtlasResponse(cmd, "Atlas overview", resp)
}

func runAtlasMap(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, app, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	query = atlasStructureQuery(query)
	structure, err := client.GetAtlasStructure(cmd.Context(), query)
	if err != nil {
		return err
	}
	result := buildAtlasStructureMapSummary(app, structure)
	if err := materializeAtlasScreenshots(result); err != nil {
		return err
	}
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	printAtlasMapSummary(result)
	return nil
}

func runAtlasGraph(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	query = atlasFrontendGraphQuery(query)
	graph, err := client.GetAtlasGraph(cmd.Context(), query)
	if err != nil {
		return err
	}
	return printAtlasResponse(cmd, "Atlas graph", graph)
}

func runAtlasAudit(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, app, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	query = atlasStructureQuery(query)
	structure, err := client.GetAtlasStructure(cmd.Context(), query)
	if err != nil {
		return err
	}
	result := buildAtlasStructureAuditSummary(app, structure)
	if err := materializeAtlasScreenshots(result); err != nil {
		return err
	}
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	printAtlasAuditSummary(result)
	return nil
}

func runAtlasArea(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, app, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	query = atlasStructureQuery(query)
	structure, err := client.GetAtlasStructure(cmd.Context(), query)
	if err != nil {
		return err
	}
	result := buildAtlasStructureAreaSummary(app, structure, args[0])
	if err := materializeAtlasScreenshots(result); err != nil {
		return err
	}
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	printAtlasAreaSummary(result)
	return nil
}

func atlasStructureQuery(query api.AtlasQuery) api.AtlasQuery {
	if query.Limit < atlasStructureFetchLimit {
		query.Limit = atlasStructureFetchLimit
	}
	return atlasFrontendGraphQuery(query)
}

func atlasFrontendGraphQuery(query api.AtlasQuery) api.AtlasQuery {
	includeDetails := false
	includeFlows := true
	query.IncludeDetails = &includeDetails
	query.IncludeFlows = &includeFlows
	return query
}

func runAtlasOpen(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	base := strings.TrimRight(config.GetAppURL(devMode), "/")
	url := fmt.Sprintf("%s/apps/%s/atlas", base, query.AppID)
	if query.BuildID != "" {
		url += "?buildId=" + query.BuildID
	}
	ui.PrintLink("Atlas", url)
	return ui.OpenBrowser(url)
}

func runAtlasScreen(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	resp, err := client.GetAtlasEntity(cmd.Context(), query, args[0])
	if err != nil {
		return err
	}
	maybeOpenAtlas(cmd, resp)
	return printAtlasResponse(cmd, "Atlas screen", resp)
}

func runAtlasObservations(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	resp, err := client.GetAtlasEntityObservations(cmd.Context(), query, args[0])
	if err != nil {
		return err
	}
	return printAtlasResponse(cmd, "Atlas observations", resp)
}

func runAtlasVariants(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	screen, err := client.GetAtlasEntity(cmd.Context(), query, args[0])
	if err != nil {
		return err
	}
	result := buildAtlasVariantSummary(screen, screen)
	if err := materializeAtlasScreenshots(result); err != nil {
		return err
	}
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	printAtlasVariantSummary(result)
	return nil
}

func runAtlasObservation(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	resp, err := client.GetAtlasObservation(cmd.Context(), query, args[0])
	if err != nil {
		return err
	}
	maybeOpenAtlas(cmd, resp)
	return printAtlasResponse(cmd, "Atlas observation", resp)
}

func runAtlasNeighbors(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	resp, err := client.GetAtlasEntityNeighbors(cmd.Context(), query, args[0])
	if err != nil {
		return err
	}
	return printAtlasResponse(cmd, "Atlas neighbors", resp)
}

func runAtlasSearch(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	query.Query = args[0]
	resp, err := client.SearchAtlas(cmd.Context(), query)
	if err != nil {
		return err
	}
	return printAtlasResponse(cmd, "Atlas search", resp)
}

func runAtlasCompare(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	query.LeftEntityID = args[0]
	query.RightEntityID = args[1]
	resp, err := client.CompareAtlasEntities(cmd.Context(), query)
	if err != nil {
		return err
	}
	return printAtlasResponse(cmd, "Atlas compare", resp)
}

func runAtlasCandidates(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, _, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	resp, err := client.GetAtlasEntityCandidates(cmd.Context(), query, args[0])
	if err != nil {
		return err
	}
	return printAtlasResponse(cmd, "Atlas candidates", resp)
}

func runAtlasCoverage(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(atlasReportID) == "" && len(args) == 1 {
		atlasReportID = strings.TrimSpace(args[0])
	}
	if strings.TrimSpace(atlasReportID) == "" {
		return fmt.Errorf("report id is required as coverage <report-id> or --report-id")
	}
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, app, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	overview, err := client.GetAtlasOverview(cmd.Context(), query)
	if err != nil {
		return err
	}
	flows, err := client.GetAtlasFlows(cmd.Context(), query)
	if err != nil {
		return err
	}
	result := buildAtlasCoverageSummary(app, overview, flows, atlasReportID)
	if err := materializeAtlasScreenshots(result); err != nil {
		return err
	}
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	printAtlasCoverageSummary(result)
	return nil
}

type atlasIssue struct {
	Severity string                   `json:"severity"`
	Title    string                   `json:"title"`
	Detail   string                   `json:"detail"`
	Evidence []map[string]interface{} `json:"evidence,omitempty"`
	Command  string                   `json:"command,omitempty"`
}

func atlasStructurePayload(response api.AtlasResponse) map[string]interface{} {
	return atlasMap(response["structure"])
}

func buildAtlasStructureMapSummary(app *api.App, response api.AtlasResponse) map[string]interface{} {
	structure := atlasStructurePayload(response)
	nodes := atlasMaps(structure["nodes"])
	edges := atlasMaps(structure["edges"])
	primaryEdges := make([]map[string]interface{}, 0)
	for _, edge := range edges {
		if atlasString(edge, "role") == "primary_access" {
			primaryEdges = append(primaryEdges, edge)
		}
	}
	return map[string]interface{}{
		"app":             atlasAppSummary(app, response),
		"stats":           atlasMap(response["stats"]),
		"algorithm":       structure["algorithm"],
		"roots":           structure["roots"],
		"spine":           structure["spine"],
		"structure_nodes": atlasStructureNodes(nodes, atlasLimitOr(len(nodes))),
		"primary_edges":   atlasStructureEdges(primaryEdges, atlasLimitOr(18)),
		"signals":         structure["issues"],
		"metadata":        structure["metadata"],
		"viewer_url":      response["viewer_url"],
	}
}

func buildAtlasStructureAuditSummary(app *api.App, response api.AtlasResponse) map[string]interface{} {
	structure := atlasStructurePayload(response)
	issues := atlasSlice(structure["issues"])
	return map[string]interface{}{
		"app":          atlasAppSummary(app, response),
		"summary":      fmt.Sprintf("%d semantic structure signals found", len(issues)),
		"issues":       issues,
		"algorithm":    structure["algorithm"],
		"viewer_url":   response["viewer_url"],
		"next_actions": []string{fmt.Sprintf("revyl atlas map --app %s", atlasApp)},
	}
}

func buildAtlasStructureAreaSummary(app *api.App, response api.AtlasResponse, area string) map[string]interface{} {
	structure := atlasStructurePayload(response)
	nodes := atlasMaps(structure["nodes"])
	edges := atlasMaps(structure["edges"])
	want := strings.ToLower(strings.TrimSpace(area))
	matches := make([]map[string]interface{}, 0)
	screenIDs := map[string]bool{}
	for _, node := range nodes {
		lane := strings.ToLower(atlasString(node, "lane"))
		label := strings.ToLower(atlasString(node, "label"))
		if lane == want || strings.Contains(lane, want) || strings.Contains(label, want) {
			matches = append(matches, node)
			screenIDs[atlasString(node, "id")] = true
		}
	}
	areaEdges := make([]map[string]interface{}, 0)
	for _, edge := range edges {
		if atlasString(edge, "role") != "primary_access" {
			continue
		}
		if screenIDs[atlasString(edge, "source")] || screenIDs[atlasString(edge, "target")] {
			areaEdges = append(areaEdges, edge)
		}
	}
	return map[string]interface{}{
		"app":          atlasAppSummary(app, response),
		"area":         area,
		"screens":      atlasStructureNodes(matches, atlasLimitOr(20)),
		"edges":        atlasStructureEdges(areaEdges, atlasLimitOr(20)),
		"screen_count": len(matches),
		"edge_count":   len(areaEdges),
		"algorithm":    structure["algorithm"],
		"next_actions": atlasAreaNextActions(matches),
	}
}

func buildAtlasMapSummary(app *api.App, overview, flows api.AtlasResponse) map[string]interface{} {
	screens := atlasScreensFrom(overview["top_screens"])
	stats := atlasMap(overview["stats"])
	productAreas := atlasProductAreas(stats)
	flowItems := atlasSlice(flows["flows"])
	issues := atlasStructuralIssues(screens, stats, flowItems, 5)
	return map[string]interface{}{
		"app":           atlasAppSummary(app, overview),
		"stats":         stats,
		"product_areas": productAreas,
		"top_screens":   atlasTopScreens(screens, 10),
		"top_flows":     atlasTopFlows(flowItems, 8),
		"signals":       issues,
		"next_actions":  atlasMapNextActions(issues),
		"viewer_url":    overview["viewer_url"],
	}
}

func buildAtlasAuditSummary(app *api.App, overview, flows api.AtlasResponse) map[string]interface{} {
	screens := atlasScreensFrom(overview["top_screens"])
	stats := atlasMap(overview["stats"])
	flowItems := atlasSlice(flows["flows"])
	issues := atlasStructuralIssues(screens, stats, flowItems, 20)
	return map[string]interface{}{
		"app":          atlasAppSummary(app, overview),
		"summary":      fmt.Sprintf("%d potential structure signals found", len(issues)),
		"issues":       issues,
		"next_actions": atlasMapNextActions(issues),
		"viewer_url":   overview["viewer_url"],
	}
}

func buildAtlasAreaSummary(app *api.App, overview, flows api.AtlasResponse, area string) map[string]interface{} {
	screens := atlasScreensFrom(overview["top_screens"])
	want := strings.ToLower(strings.TrimSpace(area))
	var matches []map[string]interface{}
	screenIDs := map[string]bool{}
	for _, screen := range screens {
		productArea := strings.ToLower(atlasString(screen, "product_area"))
		label := strings.ToLower(atlasScreenLabel(screen))
		if productArea == want || strings.Contains(productArea, want) || strings.Contains(label, want) {
			matches = append(matches, screen)
			screenIDs[atlasString(screen, "id")] = true
		}
	}
	flowItems := atlasSlice(flows["flows"])
	var areaFlows []interface{}
	for _, rawFlow := range flowItems {
		flow := atlasMap(rawFlow)
		label := strings.ToLower(atlasString(flow, "label"))
		if strings.Contains(label, want) || atlasFlowTouchesScreens(flow, screenIDs) {
			areaFlows = append(areaFlows, flow)
		}
	}
	return map[string]interface{}{
		"app":          atlasAppSummary(app, overview),
		"area":         area,
		"screens":      atlasTopScreens(matches, atlasLimitOr(20)),
		"flows":        atlasTopFlows(areaFlows, atlasLimitOr(12)),
		"screen_count": len(matches),
		"flow_count":   len(areaFlows),
		"next_actions": atlasAreaNextActions(matches),
	}
}

func buildAtlasVariantSummary(screenResp, observations api.AtlasResponse) map[string]interface{} {
	screen := atlasMap(screenResp["screen"])
	if len(screen) == 0 {
		screen = atlasMap(screenResp["entity"])
	}
	groups := atlasMap(observations["groups"])
	if len(groups) == 0 {
		groups = atlasMap(observations["observation_groups"])
	}
	distinct := atlasSlice(groups["distinct"])
	latest := atlasSlice(groups["latest"])
	mostCommon := atlasSlice(groups["most_common"])
	overlays := atlasSlice(groups["overlays"])
	errors := atlasSlice(groups["errors"])
	singletons := 0
	byRelation := map[string]int{}
	byRole := map[string]int{}
	for _, rawObs := range distinct {
		obs := atlasMap(rawObs)
		relation := atlasString(obs, "relation")
		if relation == "" {
			relation = atlasString(obs, "source")
		}
		if relation == "" {
			relation = "unknown"
		}
		byRelation[relation]++
		role := atlasString(obs, "screenshot_role")
		if role == "" {
			role = "unknown"
		}
		byRole[role]++
		if atlasInt(obs["observation_count"]) <= 1 {
			singletons++
		}
	}
	var signals []string
	variantCount := atlasInt(screen["variant_count"])
	observationCount := atlasInt(screen["observation_count"])
	if variantCount >= 8 && variantCount >= observationCount/2 {
		signals = append(signals, "This screen has many variants relative to observations; it may represent several user-facing states or dynamic content split too aggressively.")
	}
	if len(overlays) > 0 {
		signals = append(signals, "Overlay-like observations are present; check whether they should be contextual states instead of full variants.")
	}
	if len(errors) > 0 {
		signals = append(signals, "Error-state observations are present; verify they remain separate from success/default states.")
	}
	return map[string]interface{}{
		"screen":             screen,
		"observation_count":  observationCount,
		"variant_count":      variantCount,
		"distinct_samples":   atlasObservationSamples(distinct, 8),
		"latest_samples":     atlasObservationSamples(latest, 5),
		"most_common":        atlasObservationSamples(mostCommon, 5),
		"overlays_count":     len(overlays),
		"errors_count":       len(errors),
		"singleton_samples":  singletons,
		"by_relation":        byRelation,
		"by_screenshot_role": byRole,
		"signals":            signals,
		"next_actions": []string{
			fmt.Sprintf("revyl atlas observations %s --app %s", atlasString(screen, "id"), atlasApp),
			fmt.Sprintf("revyl atlas neighbors %s --app %s", atlasString(screen, "id"), atlasApp),
		},
	}
}

func buildAtlasCoverageSummary(app *api.App, overview, flows api.AtlasResponse, reportID string) map[string]interface{} {
	screens := atlasScreensFrom(overview["top_screens"])
	stats := atlasMap(overview["stats"])
	flowItems := atlasSlice(flows["flows"])
	var lowSupport []map[string]interface{}
	for _, screen := range screens {
		if atlasInt(screen["observation_count"]) <= 1 {
			lowSupport = append(lowSupport, screen)
		}
	}
	return map[string]interface{}{
		"app":                 atlasAppSummary(app, overview),
		"report_id":           reportID,
		"stats":               stats,
		"screens_from_report": atlasTopScreens(screens, atlasLimitOr(20)),
		"flows_from_report":   atlasTopFlows(flowItems, atlasLimitOr(12)),
		"low_support_states":  atlasTopScreens(lowSupport, 8),
		"next_actions": []string{
			fmt.Sprintf("revyl atlas map --app %s --report-id %s", atlasApp, reportID),
			fmt.Sprintf("revyl atlas search <query> --app %s --report-id %s", atlasApp, reportID),
		},
	}
}

func atlasStructuralIssues(screens []map[string]interface{}, stats map[string]interface{}, flows []interface{}, limit int) []atlasIssue {
	var issues []atlasIssue
	labelGroups := map[string][]map[string]interface{}{}
	for _, screen := range screens {
		key := strings.ToLower(strings.TrimSpace(atlasScreenLabel(screen)))
		if key != "" {
			labelGroups[key] = append(labelGroups[key], screen)
		}
	}
	for label, group := range labelGroups {
		if len(group) > 1 {
			issues = append(issues, atlasIssue{
				Severity: "review",
				Title:    fmt.Sprintf("Potential duplicate app screen: %s", label),
				Detail:   "Multiple Atlas screens share the same user-facing label. If these are only scroll/content differences, the app map may be overstating distinct screens.",
				Evidence: atlasTopScreens(group, 4),
				Command:  atlasScreenCommand(group),
			})
		}
	}
	for _, screen := range screens {
		obs := atlasInt(screen["observation_count"])
		variants := atlasInt(screen["variant_count"])
		if variants >= 8 && (obs == 0 || variants >= obs/2) {
			issues = append(issues, atlasIssue{
				Severity: "review",
				Title:    fmt.Sprintf("High state variation: %s", atlasScreenLabel(screen)),
				Detail:   "This user-facing area has many variants relative to observations. It may be a complex stateful flow, or similar content states may be split too finely.",
				Evidence: []map[string]interface{}{atlasScreenBrief(screen)},
				Command:  fmt.Sprintf("revyl atlas observations %s --app %s", atlasString(screen, "id"), atlasApp),
			})
		}
		if isSystemLikeScreen(screen) {
			issues = append(issues, atlasIssue{
				Severity: "info",
				Title:    fmt.Sprintf("System or device surface observed: %s", atlasScreenLabel(screen)),
				Detail:   "This was encountered during exploration. It is useful for debugging but may not belong in the primary app journey.",
				Evidence: []map[string]interface{}{atlasScreenBrief(screen)},
				Command:  fmt.Sprintf("revyl atlas screen %s --app %s", atlasString(screen, "id"), atlasApp),
			})
		}
	}
	thinFlows := 0
	for _, raw := range flows {
		flow := atlasMap(raw)
		if atlasInt(flow["support"]) <= 1 {
			thinFlows++
		}
	}
	if thinFlows > 0 {
		issues = append(issues, atlasIssue{
			Severity: "info",
			Title:    "Low-confidence paths",
			Detail:   fmt.Sprintf("%d flows have support of one observation path. They are useful, but should be confirmed with more exploration before treating them as stable journeys.", thinFlows),
			Command:  fmt.Sprintf("revyl atlas map --app %s", atlasApp),
		})
	}
	nodes := atlasInt(stats["nodes"])
	edges := atlasInt(stats["edges"])
	if nodes > 3 && edges < nodes-1 {
		issues = append(issues, atlasIssue{
			Severity: "review",
			Title:    "Sparse transition structure",
			Detail:   fmt.Sprintf("Atlas has %d screens but only %d transitions. The app may be under-explored, or report sequencing may not have produced enough edges.", nodes, edges),
			Command:  fmt.Sprintf("revyl atlas map --app %s", atlasApp),
		})
	}
	sort.SliceStable(issues, func(i, j int) bool {
		return issueRank(issues[i].Severity) < issueRank(issues[j].Severity)
	})
	if limit > 0 && len(issues) > limit {
		return issues[:limit]
	}
	return issues
}

func printAtlasMapSummary(result map[string]interface{}) {
	app := atlasMap(result["app"])
	stats := atlasMap(result["stats"])
	ui.PrintInfo("%s Atlas", atlasString(app, "name"))
	ui.PrintDim("  %d screens, %d edges, %d observations, %d flows",
		atlasInt(stats["nodes"]), atlasInt(stats["edges"]), atlasInt(stats["observations"]), atlasInt(stats["flows"]))
	if algorithm := atlasString(result, "algorithm"); algorithm != "" {
		ui.PrintDim("  structure: %s", algorithm)
	}
	printAtlasURL("Viewer", result["viewer_url"])
	if _, ok := result["structure_nodes"]; ok {
		printAtlasStructureTree(result)
	} else {
		printAtlasProductAreas(result["product_areas"])
		printAtlasNamedList("Key screens", result["top_screens"])
		printAtlasNamedList("Major flows", result["top_flows"])
		printAtlasIssues("Structure signals", result["signals"])
	}
	if _, ok := result["structure_nodes"]; ok {
		ui.Println()
		ui.PrintDim("Use --json for raw node ids, edge ids, screenshots, confidence scores, and full structure metadata.")
		if !atlasIncludeVariants {
			ui.PrintDim("Use --include-variants to include variant/state nodes in the map.")
		}
	}
	printAtlasNext(result["next_actions"])
}

func printAtlasAuditSummary(result map[string]interface{}) {
	app := atlasMap(result["app"])
	ui.PrintInfo("%s Atlas audit", atlasString(app, "name"))
	ui.PrintDim("  %s", result["summary"])
	printAtlasURL("Viewer", result["viewer_url"])
	printAtlasIssues("Potential app structure issues", result["issues"])
	printAtlasNext(result["next_actions"])
}

func printAtlasAreaSummary(result map[string]interface{}) {
	ui.PrintInfo("Atlas area: %s", result["area"])
	if _, ok := result["edges"]; ok {
		ui.PrintDim("  %d screens, %d edges", atlasInt(result["screen_count"]), atlasInt(result["edge_count"]))
	} else {
		ui.PrintDim("  %d screens, %d flows", atlasInt(result["screen_count"]), atlasInt(result["flow_count"]))
	}
	if _, ok := result["edges"]; ok {
		printAtlasStructureTree(map[string]interface{}{
			"structure_nodes": result["screens"],
			"primary_edges":   result["edges"],
		})
	} else {
		printAtlasNamedList("Screens", result["screens"])
		printAtlasNamedList("Flows", result["flows"])
	}
	printAtlasNext(result["next_actions"])
}

func printAtlasVariantSummary(result map[string]interface{}) {
	screen := atlasMap(result["screen"])
	ui.PrintInfo("Variants: %s", atlasScreenLabel(screen))
	ui.PrintDim("  %d observations, %d variants", atlasInt(result["observation_count"]), atlasInt(result["variant_count"]))
	printAtlasStringList("Signals", result["signals"])
	printAtlasCountMap("By relation", result["by_relation"])
	printAtlasCountMap("By screenshot role", result["by_screenshot_role"])
	printAtlasNamedList("Representative samples", result["distinct_samples"])
	printAtlasNext(result["next_actions"])
}

func printAtlasCoverageSummary(result map[string]interface{}) {
	ui.PrintInfo("Atlas report coverage")
	ui.PrintDim("  report_id=%s", result["report_id"])
	stats := atlasMap(result["stats"])
	ui.PrintDim("  %d screens, %d edges, %d observations, %d flows in this filtered view",
		atlasInt(stats["nodes"]), atlasInt(stats["edges"]), atlasInt(stats["observations"]), atlasInt(stats["flows"]))
	printAtlasNamedList("Screens mapped from report", result["screens_from_report"])
	printAtlasNamedList("Flows mapped from report", result["flows_from_report"])
	printAtlasNamedList("Low-support states", result["low_support_states"])
	printAtlasNext(result["next_actions"])
}

func atlasAppSummary(app *api.App, overview api.AtlasResponse) map[string]interface{} {
	result := map[string]interface{}{}
	if app != nil {
		result["id"] = app.ID
		result["name"] = app.Name
		result["platform"] = app.Platform
	}
	if result["id"] == nil {
		result["id"] = overview["app_id"]
	}
	return result
}

func atlasScreensFrom(value interface{}) []map[string]interface{} {
	items := atlasSlice(value)
	screens := make([]map[string]interface{}, 0, len(items))
	for _, raw := range items {
		screen := atlasMap(raw)
		if len(screen) > 0 {
			screens = append(screens, screen)
		}
	}
	sort.SliceStable(screens, func(i, j int) bool {
		return atlasInt(screens[i]["observation_count"]) > atlasInt(screens[j]["observation_count"])
	})
	return screens
}

func atlasMaps(value interface{}) []map[string]interface{} {
	items := atlasSlice(value)
	out := make([]map[string]interface{}, 0, len(items))
	for _, raw := range items {
		item := atlasMap(raw)
		if len(item) > 0 {
			out = append(out, item)
		}
	}
	return out
}

func atlasStructureNodes(nodes []map[string]interface{}, limit int) []map[string]interface{} {
	if limit <= 0 || limit > len(nodes) {
		limit = len(nodes)
	}
	out := make([]map[string]interface{}, 0, limit)
	for i := 0; i < limit; i++ {
		node := nodes[i]
		out = append(out, map[string]interface{}{
			"id":         atlasString(node, "id"),
			"label":      atlasString(node, "label"),
			"role":       atlasString(node, "role"),
			"parent_id":  atlasString(node, "parent_id"),
			"rank":       atlasInt(node["rank"]),
			"lane":       atlasString(node, "lane"),
			"confidence": node["confidence"],
			"support":    atlasInt(node["support"]),
			"flags":      node["issue_flags"],
		})
	}
	return out
}

func atlasStructureEdges(edges []map[string]interface{}, limit int) []map[string]interface{} {
	if limit <= 0 || limit > len(edges) {
		limit = len(edges)
	}
	out := make([]map[string]interface{}, 0, limit)
	for i := 0; i < limit; i++ {
		edge := edges[i]
		out = append(out, map[string]interface{}{
			"source":     atlasString(edge, "source"),
			"target":     atlasString(edge, "target"),
			"role":       atlasString(edge, "role"),
			"label":      atlasString(edge, "label"),
			"support":    edge["support"],
			"confidence": edge["confidence"],
		})
	}
	return out
}

func atlasTopScreens(screens []map[string]interface{}, limit int) []map[string]interface{} {
	if limit <= 0 || limit > len(screens) {
		limit = len(screens)
	}
	out := make([]map[string]interface{}, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, atlasScreenBrief(screens[i]))
	}
	return out
}

func atlasScreenBrief(screen map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":                atlasString(screen, "id"),
		"label":             atlasScreenLabel(screen),
		"product_area":      atlasString(screen, "product_area"),
		"screen_kind":       atlasString(screen, "screen_kind"),
		"observation_count": atlasInt(screen["observation_count"]),
		"variant_count":     atlasInt(screen["variant_count"]),
		"viewer_url":        atlasString(screen, "viewer_url"),
	}
}

func atlasTopFlows(flows []interface{}, limit int) []map[string]interface{} {
	if limit <= 0 || limit > len(flows) {
		limit = len(flows)
	}
	out := make([]map[string]interface{}, 0, limit)
	for i := 0; i < limit; i++ {
		flow := atlasMap(flows[i])
		out = append(out, map[string]interface{}{
			"id":         atlasString(flow, "id"),
			"label":      atlasString(flow, "label"),
			"support":    atlasInt(flow["support"]),
			"viewer_url": atlasString(flow, "viewer_url"),
		})
	}
	return out
}

func atlasObservationSamples(observations []interface{}, limit int) []map[string]interface{} {
	if limit <= 0 || limit > len(observations) {
		limit = len(observations)
	}
	out := make([]map[string]interface{}, 0, limit)
	for i := 0; i < limit; i++ {
		obs := atlasMap(observations[i])
		out = append(out, map[string]interface{}{
			"observation_id":  atlasString(obs, "observation_id"),
			"report_id":       atlasString(obs, "report_id"),
			"relation":        atlasString(obs, "relation"),
			"screenshot_role": atlasString(obs, "screenshot_role"),
			"confidence":      obs["confidence"],
			"viewer_url":      atlasString(obs, "viewer_url"),
		})
	}
	return out
}

func atlasProductAreas(stats map[string]interface{}) []map[string]interface{} {
	areas := atlasMap(stats["product_areas"])
	keys := make([]string, 0, len(areas))
	for key := range areas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]map[string]interface{}, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]interface{}{"area": key, "screens": atlasInt(areas[key])})
	}
	return out
}

func printAtlasStructureTree(result map[string]interface{}) {
	nodes := atlasMaps(result["structure_nodes"])
	if len(nodes) == 0 {
		return
	}
	edges := atlasMaps(result["primary_edges"])
	nodeByID := make(map[string]map[string]interface{}, len(nodes))
	childrenByParent := map[string][]map[string]interface{}{}
	roots := make([]map[string]interface{}, 0)
	for _, node := range nodes {
		id := atlasString(node, "id")
		if id == "" {
			continue
		}
		nodeByID[id] = node
	}
	for _, node := range nodes {
		parentID := atlasString(node, "parent_id")
		if parentID == "" || nodeByID[parentID] == nil {
			if atlasString(node, "role") != "system" {
				roots = append(roots, node)
			}
			continue
		}
		childrenByParent[parentID] = append(childrenByParent[parentID], node)
	}
	sortAtlasStructureNodes(roots)
	for parentID := range childrenByParent {
		sortAtlasStructureNodes(childrenByParent[parentID])
	}
	edgeByPair := map[string]map[string]interface{}{}
	for _, edge := range edges {
		key := atlasString(edge, "source") + "->" + atlasString(edge, "target")
		if key != "->" {
			edgeByPair[key] = edge
		}
	}

	ui.Println()
	ui.PrintInfo("Structure:")
	printed := 0
	limit := atlasLimitOr(len(nodes))
	seen := map[string]bool{}
	printableCount := 0
	for _, node := range nodes {
		if atlasString(node, "role") != "system" {
			printableCount++
		}
	}
	for _, root := range roots {
		if printed >= limit {
			break
		}
		printed += printAtlasStructureNode(root, "", true, childrenByParent, edgeByPair, seen, limit-printed)
	}
	if printed < printableCount {
		ui.PrintDim("  ... %d more nodes. Re-run with --limit %d or --json.", printableCount-printed, printableCount)
	}
	printAtlasStructureLegend()
}

func sortAtlasStructureNodes(nodes []map[string]interface{}) {
	sort.SliceStable(nodes, func(i, j int) bool {
		leftRank := atlasInt(nodes[i]["rank"])
		rightRank := atlasInt(nodes[j]["rank"])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		leftRole := atlasStructureRoleRank(atlasString(nodes[i], "role"))
		rightRole := atlasStructureRoleRank(atlasString(nodes[j], "role"))
		if leftRole != rightRole {
			return leftRole < rightRole
		}
		leftLane := atlasString(nodes[i], "lane")
		rightLane := atlasString(nodes[j], "lane")
		if leftLane != rightLane {
			return leftLane < rightLane
		}
		return atlasString(nodes[i], "label") < atlasString(nodes[j], "label")
	})
}

func atlasStructureRoleRank(role string) int {
	switch strings.ToLower(role) {
	case "root":
		return 0
	case "entry":
		return 1
	case "primary":
		return 2
	case "branch":
		return 3
	case "modal", "utility":
		return 4
	case "terminal":
		return 5
	case "variant":
		return 6
	case "system":
		return 7
	default:
		return 8
	}
}

func printAtlasStructureNode(
	node map[string]interface{},
	prefix string,
	last bool,
	childrenByParent map[string][]map[string]interface{},
	edgeByPair map[string]map[string]interface{},
	seen map[string]bool,
	remaining int,
) int {
	if remaining < 0 {
		return 0
	}
	id := atlasString(node, "id")
	if id == "" || seen[id] {
		return 0
	}
	seen[id] = true
	connector := "- "
	childPrefix := "  "
	if prefix != "" {
		if last {
			connector = "`- "
			childPrefix = "   "
		} else {
			connector = "|- "
			childPrefix = "|  "
		}
	}
	ui.PrintDim("%s%s%s%s", prefix, connector, atlasStructureNodeTitle(node), atlasStructureNodeMeta(node))
	if parentID := atlasString(node, "parent_id"); parentID != "" {
		edge := edgeByPair[parentID+"->"+id]
		if label := atlasString(edge, "label"); label != "" {
			ui.PrintDim("%s%s  via: %s", prefix, childPrefix, atlasShorten(label, 96))
		}
	}
	printed := 1
	children := childrenByParent[id]
	for i, child := range children {
		if printed > remaining {
			break
		}
		printed += printAtlasStructureNode(child, prefix+childPrefix, i == len(children)-1, childrenByParent, edgeByPair, seen, remaining-printed)
	}
	return printed
}

func atlasStructureNodeTitle(node map[string]interface{}) string {
	label := atlasString(node, "label")
	if label == "" {
		label = atlasString(node, "id")
	}
	if role := atlasString(node, "role"); role == "root" {
		return label + " (root)"
	}
	return label
}

func atlasStructureNodeMeta(node map[string]interface{}) string {
	parts := []string{}
	if lane := atlasString(node, "lane"); lane != "" {
		parts = append(parts, lane)
	}
	if role := atlasString(node, "role"); role != "" && role != "root" {
		parts = append(parts, role)
	}
	if support := atlasInt(node["support"]); support > 0 {
		parts = append(parts, fmt.Sprintf("%d shots", support))
	}
	if confidence := atlasFloat(node["confidence"]); confidence > 0 && confidence < 0.58 {
		parts = append(parts, fmt.Sprintf("low confidence %.2f", confidence))
	}
	flags := atlasStringSlice(node["flags"])
	if len(flags) > 0 {
		parts = append(parts, strings.Join(flags, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, "; ") + "]"
}

func printAtlasStructureLegend() {
	ui.Println()
	ui.PrintDim("Legend: weak_parent means Atlas found a path but the parent is inferred from low-confidence evidence such as dismiss/back/close. contextual_state means modal, player, detail, or other non-primary app state.")
}

func atlasShorten(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return strings.TrimSpace(value[:limit-3]) + "..."
}

func atlasStringSlice(value interface{}) []string {
	items := atlasSlice(value)
	out := make([]string, 0, len(items))
	for _, raw := range items {
		if text, ok := raw.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func atlasFloat(value interface{}) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	default:
		return 0
	}
}

func atlasMapNextActions(issues []atlasIssue) []string {
	var next []string
	for _, issue := range issues {
		if issue.Command != "" {
			next = append(next, issue.Command)
		}
		if len(next) >= 5 {
			break
		}
	}
	if len(next) == 0 {
		next = append(next, fmt.Sprintf("revyl atlas map --app %s", atlasApp))
	}
	return next
}

func atlasAreaNextActions(screens []map[string]interface{}) []string {
	var next []string
	for _, screen := range screens {
		if len(next) >= 4 {
			break
		}
		id := atlasString(screen, "id")
		if id != "" {
			next = append(next, fmt.Sprintf("revyl atlas screen %s --app %s", id, atlasApp))
		}
	}
	return next
}

func atlasFlowTouchesScreens(flow map[string]interface{}, screenIDs map[string]bool) bool {
	for _, key := range []string{"screens", "nodes"} {
		for _, raw := range atlasSlice(flow[key]) {
			item := atlasMap(raw)
			if screenIDs[atlasString(item, "id")] || screenIDs[atlasString(item, "entity_id")] {
				return true
			}
		}
	}
	return false
}

func atlasScreenCommand(group []map[string]interface{}) string {
	if len(group) == 0 {
		return ""
	}
	return fmt.Sprintf("revyl atlas screen %s --app %s", atlasString(group[0], "id"), atlasApp)
}

func isSystemLikeScreen(screen map[string]interface{}) bool {
	area := strings.ToLower(atlasString(screen, "product_area"))
	label := strings.ToLower(atlasScreenLabel(screen))
	return strings.Contains(area, "system") ||
		strings.Contains(label, "ios_") ||
		strings.Contains(label, "system") ||
		strings.Contains(label, "share_sheet") ||
		strings.Contains(label, "permission")
}

func issueRank(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return 0
	case "review":
		return 1
	case "info":
		return 2
	default:
		return 3
	}
}

func atlasLimitOr(fallback int) int {
	if atlasLimit > 0 {
		return atlasLimit
	}
	return fallback
}

func printAtlasProductAreas(value interface{}) {
	items := atlasSlice(value)
	if len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Product areas:")
	for _, raw := range items {
		item := atlasMap(raw)
		ui.PrintDim("  %s: %d screens", atlasString(item, "area"), atlasInt(item["screens"]))
	}
}

func printAtlasNamedList(title string, value interface{}) {
	items := atlasSlice(value)
	if len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("%s:", title)
	for _, raw := range items {
		item := atlasMap(raw)
		label := atlasString(item, "label")
		if label == "" {
			label = atlasString(item, "title")
		}
		if label == "" {
			label = atlasString(item, "observation_id")
		}
		if label == "" {
			label = atlasString(item, "id")
		}
		if support := atlasInt(item["support"]); support > 0 {
			ui.PrintDim("  %s  support=%d", label, support)
		} else if relation := atlasString(item, "relation"); relation != "" {
			ui.PrintDim("  %s  %s confidence=%v", label, relation, item["confidence"])
		} else {
			ui.PrintDim("  %s  obs=%d variants=%d", label, atlasInt(item["observation_count"]), atlasInt(item["variant_count"]))
		}
		if id := atlasString(item, "id"); id != "" {
			ui.PrintDim("    %s", id)
		}
	}
}

func printAtlasIssues(title string, value interface{}) {
	items := atlasSlice(value)
	if len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("%s:", title)
	for _, raw := range items {
		item := atlasMap(raw)
		ui.PrintDim("  [%s] %s", atlasString(item, "severity"), atlasString(item, "title"))
		if detail := atlasString(item, "detail"); detail != "" {
			ui.PrintDim("    %s", detail)
		}
		if command := atlasString(item, "command"); command != "" {
			ui.PrintDim("    next: %s", command)
		}
	}
}

func printAtlasStringList(title string, value interface{}) {
	items := atlasSlice(value)
	if len(items) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("%s:", title)
	for _, raw := range items {
		if text, ok := raw.(string); ok && text != "" {
			ui.PrintDim("  %s", text)
		}
	}
}

func printAtlasCountMap(title string, value interface{}) {
	items := atlasCountMap(value)
	if len(items) == 0 {
		return
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ui.Println()
	ui.PrintInfo("%s:", title)
	for _, key := range keys {
		ui.PrintDim("  %s: %d", key, items[key])
	}
}

func atlasScreenLabel(item map[string]interface{}) string {
	label := atlasString(item, "label")
	if label == "" {
		label = atlasString(item, "semantic_name")
	}
	if label == "" {
		label = atlasString(item, "id")
	}
	return label
}

func atlasMap(value interface{}) map[string]interface{} {
	if item, ok := value.(map[string]interface{}); ok {
		return item
	}
	return map[string]interface{}{}
}

func atlasSlice(value interface{}) []interface{} {
	switch items := value.(type) {
	case []interface{}:
		return items
	case []map[string]interface{}:
		out := make([]interface{}, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	case []atlasIssue:
		out := make([]interface{}, 0, len(items))
		for _, item := range items {
			out = append(out, map[string]interface{}{
				"severity": item.Severity,
				"title":    item.Title,
				"detail":   item.Detail,
				"evidence": item.Evidence,
				"command":  item.Command,
			})
		}
		return out
	case []string:
		out := make([]interface{}, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	}
	return nil
}

func atlasCountMap(value interface{}) map[string]int {
	switch typed := value.(type) {
	case map[string]int:
		return typed
	case map[string]interface{}:
		out := map[string]int{}
		for key, raw := range typed {
			out[key] = atlasInt(raw)
		}
		return out
	default:
		return map[string]int{}
	}
}

func atlasInt(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}
