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
	Long: `Inspect an app as a media-grounded knowledge graph.

Start with:
  revyl atlas apps
  revyl atlas brief --app "My App"
  revyl atlas graph --app "My App"
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

var atlasBriefCmd = &cobra.Command{
	Use:   "brief",
	Short: "Find graph anchors and evidence to begin exploring",
	RunE:  runAtlasBrief,
}

var atlasMapCmd = &cobra.Command{
	Use:   "map",
	Short: "Compatibility alias for the Atlas graph",
	RunE:  runAtlasMap,
}

var atlasGraphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Traverse the app's flat Atlas knowledge graph",
	Example: `  revyl atlas graph --app "My App"
  revyl atlas graph --app "My App" --screenshots --json
  revyl atlas graph --app "My App" --screenshot-dir /tmp/atlas-shots --json`,
	RunE: runAtlasGraph,
}

var atlasAreaCmd = &cobra.Command{
	Use:   "area <product-area>",
	Short: "Inspect a product-area subgraph and its boundary edges",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasArea,
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

var atlasReportCmd = &cobra.Command{
	Use:   "report <screen-or-observation-id>",
	Short: "Inspect the report that produced Atlas evidence",
	Example: `  revyl atlas report <screen-id> --app "My App"
  revyl atlas report <observation-id> --app "My App" --json`,
	Args: cobra.ExactArgs(1),
	RunE: runAtlasReport,
}

var atlasNeighborsCmd = &cobra.Command{
	Use:   "neighbors <screen-id>",
	Short: "Show neighboring Atlas screens",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasNeighbors,
}

var atlasEdgeCmd = &cobra.Command{
	Use:   "edge <source-screen> <target-screen>",
	Short: "Inspect one observed transition",
	Args:  cobra.ExactArgs(2),
	RunE:  runAtlasEdge,
}

var atlasSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search Atlas screens",
	Args:  cobra.ExactArgs(1),
	RunE:  runAtlasSearch,
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
	atlasDirection           string
	atlasAppsSearch          string
	atlasAppsAll             bool
	atlasEdgeRuns            bool
	atlasScreenshots         bool
	atlasScreenshotDir       string
)

const atlasGraphFetchLimit = 840

func init() {
	atlasCmd.AddCommand(
		atlasAppsCmd,
		atlasBriefCmd,
		atlasMapCmd,
		atlasAreaCmd,
		atlasGraphCmd,
		atlasOpenCmd,
		atlasScreenCmd,
		atlasObservationsCmd,
		atlasObservationCmd,
		atlasReportCmd,
		atlasNeighborsCmd,
		atlasEdgeCmd,
		atlasSearchCmd,
	)
	for _, cmd := range []*cobra.Command{
		atlasBriefCmd,
		atlasMapCmd,
		atlasAreaCmd,
		atlasGraphCmd,
		atlasScreenCmd,
		atlasNeighborsCmd,
		atlasEdgeCmd,
		atlasSearchCmd,
	} {
		addAtlasScopeFlags(cmd, true)
		addAtlasOutputFlags(cmd)
	}
	addAtlasScopeFlags(atlasReportCmd, true)
	atlasReportCmd.Flags().BoolVar(&atlasJSON, "json", false, "Output stable JSON")
	for _, cmd := range []*cobra.Command{
		atlasObservationsCmd,
		atlasObservationCmd,
	} {
		addAtlasScopeFlags(cmd, false)
		addAtlasOutputFlags(cmd)
	}
	atlasOpenCmd.Flags().StringVar(&atlasApp, "app", "", "App name or app id")
	atlasOpenCmd.Flags().StringVar(&atlasBuild, "build", "all", "Build id, build version, latest, or all")
	atlasOpenCmd.Flags().StringVar(&atlasSince, "since", "", "Product range: 1d, 7d, or 30d")
	atlasOpenCmd.Flags().BoolVar(&atlasIncludeVariants, "include-variants", false, "Show variant nodes")
	atlasAppsCmd.Flags().StringVar(&appListPlatform, "platform", "", "Filter by platform (android, ios)")
	atlasAppsCmd.Flags().StringVar(&atlasAppsSearch, "search", "", "Search by app name")
	atlasAppsCmd.Flags().BoolVar(&atlasAppsAll, "all", false, "Include apps without available Atlas data")
	atlasAppsCmd.Flags().BoolVar(&atlasJSON, "json", false, "Output raw JSON")
	atlasNeighborsCmd.Flags().StringVar(&atlasDirection, "direction", "both", "Neighbor direction: both, in, or out")
	atlasEdgeCmd.Flags().BoolVar(&atlasEdgeRuns, "runs", false, "Include recent raw evidence runs for the transition")
	_ = atlasBriefCmd.MarkFlagRequired("app")
	_ = atlasMapCmd.MarkFlagRequired("app")
	_ = atlasAreaCmd.MarkFlagRequired("app")
	_ = atlasGraphCmd.MarkFlagRequired("app")
	_ = atlasOpenCmd.MarkFlagRequired("app")
	_ = atlasScreenCmd.MarkFlagRequired("app")
	_ = atlasObservationsCmd.MarkFlagRequired("app")
	_ = atlasObservationCmd.MarkFlagRequired("app")
	_ = atlasReportCmd.MarkFlagRequired("app")
	_ = atlasNeighborsCmd.MarkFlagRequired("app")
	_ = atlasEdgeCmd.MarkFlagRequired("app")
	_ = atlasSearchCmd.MarkFlagRequired("app")
}

func addAtlasScopeFlags(cmd *cobra.Command, includeWorkflow bool) {
	cmd.Flags().StringVar(&atlasApp, "app", "", "App name or app id")
	cmd.Flags().StringVar(&atlasBuild, "build", "all", "Build id, build version, latest, or all")
	cmd.Flags().StringVar(&atlasFrom, "from", "", "Start time filter (ISO timestamp)")
	cmd.Flags().StringVar(&atlasTo, "to", "", "End time filter (ISO timestamp)")
	cmd.Flags().StringVar(&atlasSince, "since", "", "Relative start time, such as 7d")
	cmd.Flags().StringVar(&atlasReportID, "report-id", "", "Filter to one report")
	cmd.Flags().StringVar(&atlasTestID, "test-id", "", "Filter to one test")
	if includeWorkflow {
		cmd.Flags().StringVar(&atlasWorkflowExecutionID, "workflow-execution-id", "", "Filter to one workflow execution")
	}
	cmd.Flags().StringVar(&atlasSourceKind, "source-kind", "", "Filter by Atlas source kind")
	cmd.Flags().StringVar(&atlasSurfaceScope, "surface-scope", "app", "Surface scope: app, app+system, app+external, all")
	cmd.Flags().StringVar(&atlasVisibility, "visibility", "included", "Visibility: included or included+excluded_debug")
	cmd.Flags().BoolVar(&atlasIncludeVariants, "include-variants", false, "Include variant nodes")
	cmd.Flags().IntVar(&atlasLimit, "limit", atlasGraphFetchLimit, "Maximum results to return")
}

func addAtlasOutputFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&atlasJSON, "json", false, "Output stable JSON")
	cmd.Flags().BoolVar(&atlasScreenshots, "screenshots", false, "Include signed screenshot URLs")
	cmd.Flags().StringVar(&atlasScreenshotDir, "screenshot-dir", "", "Download screenshots and add local paths")
}

func runAtlasGuide(cmd *cobra.Command, args []string) error {
	ui.PrintInfo("Start with one of these:")
	ui.PrintDim("  revyl atlas apps")
	ui.PrintDim("  revyl atlas brief --app \"My App\"")
	ui.PrintDim("  revyl atlas graph --app \"My App\"")
	ui.PrintDim("  revyl atlas search \"checkout error\" --app \"My App\"")
	ui.Println()
	ui.PrintInfo("Then traverse bottom-up from a starting anchor:")
	ui.PrintDim("  revyl atlas open --app \"My App\"")
	ui.PrintDim("  revyl atlas screen <screen-id> --app \"My App\" --screenshots --screenshot-dir /tmp/atlas-shots")
	ui.PrintDim("  revyl atlas observations <screen-id> --app \"My App\" --screenshots --screenshot-dir /tmp/atlas-shots")
	ui.PrintDim("  revyl atlas report <screen-or-observation-id> --app \"My App\"")
	ui.PrintDim("  revyl atlas neighbors <screen-id> --app \"My App\"")
	ui.PrintDim("  revyl atlas edge <source-id> <target-id> --app \"My App\" --runs --json")
	ui.Println()
	ui.PrintDim("Treat names and summaries as navigation aids. Open the real screenshots and watch edge video evidence before claiming what the app does.")
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
		resolved, getErr := client.GetApp(cmd.Context(), parsed.String())
		if getErr != nil {
			return nil, getErr
		}
		return resolved, nil
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
			ui.PrintDim("  revyl atlas brief --app %s    # %s (%s)", match.ID, match.Name, match.Platform)
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
	var includeVariants *bool
	if cmd.Flags().Changed("include-variants") {
		includeVariants = &atlasIncludeVariants
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
		IncludeVariants:     includeVariants,
		Limit:               atlasLimit,
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

func atlasScreenshotsRequested() bool {
	return atlasScreenshots || strings.TrimSpace(atlasScreenshotDir) != ""
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
	printAtlasScreens(response["nodes"])
	printAtlasScreens(response["top_screens"])
	printAtlasScreens(response["results"])
	if screen, ok := response["screen"].(map[string]interface{}); ok {
		printAtlasScreen(screen)
	}
	printAtlasGroups(response["groups"])
	printAtlasGroups(response["observation_groups"])
	printAtlasNeighbors(response["neighbors"])
	printAtlasFlows(response["flows"])
	printAtlasNext(response["next_actions"])
	printAtlasScreenshotHint(response)
	return nil
}

func printAtlasScreenshotHint(response api.AtlasResponse) {
	if atlasScreenshotsRequested() || !atlasContainsKey(response, "screenshot_s3_key") {
		return
	}
	ui.Println()
	ui.PrintDim("Screenshots are omitted by default. Re-run with --screenshots for signed URLs or --screenshot-dir <dir> to download image files.")
}

func atlasContainsKey(value interface{}, key string) bool {
	switch typed := value.(type) {
	case api.AtlasResponse:
		return atlasContainsKey(map[string]interface{}(typed), key)
	case map[string]interface{}:
		if _, ok := typed[key]; ok {
			return true
		}
		for _, child := range typed {
			if atlasContainsKey(child, key) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if atlasContainsKey(child, key) {
				return true
			}
		}
	}
	return false
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
	case []map[string]interface{}:
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

func printAtlasNext(value interface{}) {
	items := atlasSlice(value)
	if len(items) == 0 {
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

func runAtlasApps(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	result, err := client.GetAtlasIndex(cmd.Context(), atlasAppsSearch, 250, 0, false, !atlasAppsAll)
	if err != nil {
		return err
	}
	if !atlasAppsAll && !atlasIndexSupportsReadiness(result) {
		return fmt.Errorf("the connected Revyl backend does not support Atlas readiness filtering; update the backend or rerun with --all")
	}
	apps := make([]map[string]interface{}, 0)
	readyCount := 0
	for _, raw := range atlasSlice(result["apps"]) {
		item := atlasMap(raw)
		app, ready := atlasIndexAppContract(item, atlasAppsAll)
		if ready {
			readyCount++
		}
		if !atlasAppsAll && !ready {
			continue
		}
		platform := atlasString(app, "platform")
		if appListPlatform != "" && !strings.EqualFold(platform, appListPlatform) {
			continue
		}
		apps = append(apps, app)
	}
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"contract":    "atlas_apps.v1",
			"apps":        apps,
			"count":       len(apps),
			"ready_count": readyCount,
			"total":       atlasInt(result["total"]),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	label := "Available Atlas apps"
	if atlasAppsAll {
		label = "Atlas apps"
	}
	ui.PrintInfo("%s (%d):", label, len(apps))
	for _, app := range apps {
		ui.PrintDim(
			"  %s  %s (%s)  ready=%t",
			atlasString(app, "id"),
			atlasString(app, "name"),
			atlasString(app, "platform"),
			atlasBool(app["atlas_ready"]),
		)
		ui.PrintDim("    revyl atlas brief --app %s", atlasString(app, "id"))
	}
	return nil
}

func atlasIndexSupportsReadiness(result api.AtlasResponse) bool {
	apps := atlasMaps(result["apps"])
	if len(apps) == 0 {
		return atlasInt(result["total"]) == 0
	}
	for _, item := range apps {
		if _, ok := item["olap"]; !ok {
			return false
		}
	}
	return true
}

func atlasIndexAppContract(item map[string]interface{}, includeDetails bool) (map[string]interface{}, bool) {
	ready := atlasBool(atlasMap(item["olap"])["ready"])
	app := map[string]interface{}{
		"id":              atlasString(item, "app_id"),
		"name":            atlasString(item, "app_name"),
		"platform":        atlasString(item, "platform"),
		"atlas_ready":     ready,
		"latest_build_id": item["latest_build_id"],
		"latest_version":  item["latest_build_version"],
		"updated_at":      item["updated_at"],
	}
	if includeDetails {
		app["atlas_status"] = atlasString(item, "status")
		app["stats"] = item["stats"]
	}
	return app, ready
}

func fetchAtlasAgentGraph(cmd *cobra.Command, client *api.Client) (api.AtlasQuery, *api.App, api.AtlasResponse, error) {
	query, app, err := atlasQueryFor(cmd, client)
	if err != nil {
		return api.AtlasQuery{}, nil, nil, err
	}
	query = atlasKnowledgeGraphQuery(query)
	graph, err := client.GetAtlasGraph(cmd.Context(), query)
	if err != nil {
		return api.AtlasQuery{}, nil, nil, err
	}
	return query, app, graph, nil
}

func runAtlasBrief(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	_, app, graph, err := fetchAtlasAgentGraph(cmd, client)
	if err != nil {
		return err
	}
	result := buildAtlasBrief(app, graph)
	if err := materializeAtlasScreenshots(result["visual_sample"]); err != nil {
		return err
	}
	return printAtlasContract(cmd, result, printAtlasBrief)
}

func runAtlasMap(cmd *cobra.Command, args []string) error {
	return runAtlasKnowledgeGraph(cmd)
}

func runAtlasGraph(cmd *cobra.Command, args []string) error {
	return runAtlasKnowledgeGraph(cmd)
}

func runAtlasKnowledgeGraph(cmd *cobra.Command) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	_, app, graph, err := fetchAtlasAgentGraph(cmd, client)
	if err != nil {
		return err
	}
	result := buildAtlasKnowledgeGraph(app, graph)
	if err := materializeAtlasScreenshots(result); err != nil {
		return err
	}
	return printAtlasContract(cmd, result, printAtlasKnowledgeGraph)
}

func runAtlasArea(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	_, app, graph, err := fetchAtlasAgentGraph(cmd, client)
	if err != nil {
		return err
	}
	result := buildAtlasAreaGraph(app, graph, args[0])
	if atlasInt(result["screen_count"]) == 0 {
		return fmt.Errorf("Atlas product area %q was not found; available areas: %s", args[0], strings.Join(atlasStringSlice(result["available_areas"]), ", "))
	}
	if err := materializeAtlasScreenshots(result); err != nil {
		return err
	}
	return printAtlasContract(cmd, result, printAtlasAreaSummary)
}

func atlasKnowledgeGraphQuery(query api.AtlasQuery) api.AtlasQuery {
	if query.Limit < atlasGraphFetchLimit {
		query.Limit = atlasGraphFetchLimit
	}
	includeDetails := false
	includeFlows := false
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
	viewerURL := fmt.Sprintf("%s/apps/%s/atlas", base, query.AppID)
	params := url.Values{}
	if query.BuildID != "" {
		params.Set("buildId", query.BuildID)
	}
	if atlasSince != "" {
		rangeValue := strings.ToLower(strings.TrimSpace(atlasSince))
		if rangeValue != "1d" && rangeValue != "7d" && rangeValue != "30d" {
			return fmt.Errorf("--since must be 1d, 7d, or 30d when opening the Atlas product")
		}
		params.Set("range", rangeValue)
	}
	if atlasIncludeVariants {
		params.Set("includeVariants", "1")
	}
	if encoded := params.Encode(); encoded != "" {
		viewerURL += "?" + encoded
	}
	ui.PrintLink("Atlas", viewerURL)
	return ui.OpenBrowser(viewerURL)
}

func runAtlasScreen(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	_, app, graph, err := fetchAtlasAgentGraph(cmd, client)
	if err != nil {
		return err
	}
	node, err := resolveAtlasNode(graph, args[0])
	if err != nil {
		return err
	}
	result := buildAtlasScreen(app, graph, node)
	if err := materializeAtlasScreenshots(result); err != nil {
		return err
	}
	return printAtlasContract(cmd, result, printAtlasScreenContract)
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
	resp["contract"] = "atlas_observations.v1"
	resp["projection"] = atlasEvidenceProjection(query)
	return printAtlasResponse(cmd, "Atlas observations", resp)
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
	resp["contract"] = "atlas_observation.v1"
	resp["projection"] = atlasEvidenceProjection(query)
	resp["next_actions"] = append(
		atlasStringSlice(resp["next_actions"]),
		fmt.Sprintf("revyl atlas report %s --app %s --json", args[0], query.AppID),
	)
	return printAtlasResponse(cmd, "Atlas observation", resp)
}

func runAtlasReport(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, app, graph, err := fetchAtlasAgentGraph(cmd, client)
	if err != nil {
		return err
	}

	requested := args[0]
	observationID, resolvedAs, sourceScreen, err := resolveAtlasReportTarget(graph, requested)
	if err != nil {
		return err
	}
	if resolvedAs == "screen" && observationID == "" {
		return fmt.Errorf("Atlas screen %s has no representative observation; run 'revyl atlas observations %s --app %s'", atlasScreenLabel(sourceScreen), atlasString(sourceScreen, "id"), query.AppID)
	}

	observationResponse, err := client.GetAtlasObservation(cmd.Context(), query, observationID)
	if err != nil {
		return fmt.Errorf("failed to resolve %q as an Atlas screen or observation: %w", requested, err)
	}
	observation := atlasMap(observationResponse["observation"])
	if len(observation) == 0 {
		return fmt.Errorf("Atlas observation %s did not include report provenance", observationID)
	}

	executionID := atlasString(observation, "execution_id")
	sessionID := atlasString(observation, "session_id")
	var envelope *api.CLIReportContextEnvelope
	var reportErr error
	if executionID != "" {
		envelope, reportErr = client.GetReportContextByExecution(cmd.Context(), executionID, true, true, false)
	}
	if envelope == nil && sessionID != "" {
		envelope, reportErr = client.GetReportBySession(cmd.Context(), sessionID, true, true, false)
	}
	if envelope == nil {
		if reportErr != nil {
			return fmt.Errorf("failed to fetch the report for Atlas observation %s: %w", observationID, reportErr)
		}
		return fmt.Errorf("Atlas observation %s has no execution or session report reference", observationID)
	}

	report, err := atlasUserFacingReport(envelope.Raw)
	if err != nil {
		return err
	}
	result := buildAtlasReportContract(app, graph, requested, resolvedAs, sourceScreen, observation, report)
	return printAtlasContract(cmd, result, printAtlasReportContract)
}

func resolveAtlasReportTarget(
	graph api.AtlasResponse,
	requested string,
) (string, string, map[string]interface{}, error) {
	node, err := resolveAtlasNode(graph, requested)
	if err == nil {
		return atlasString(node, "representative_observation_id"), "screen", node, nil
	}
	if _, parseErr := uuid.Parse(requested); parseErr == nil {
		return requested, "observation", nil, nil
	}
	return "", "", nil, err
}

func atlasUserFacingReport(raw json.RawMessage) (map[string]interface{}, error) {
	data, err := buildUserFacingReportJSON(raw)
	if err != nil {
		return nil, err
	}
	var report map[string]interface{}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("failed to parse report context: %w", err)
	}
	return report, nil
}

func buildAtlasReportContract(
	app *api.App,
	graph api.AtlasResponse,
	requested string,
	resolvedAs string,
	sourceScreen map[string]interface{},
	observation map[string]interface{},
	report map[string]interface{},
) map[string]interface{} {
	provenance := map[string]interface{}{
		"observation_id":        observation["observation_id"],
		"report_id":             observation["report_id"],
		"execution_id":          observation["execution_id"],
		"session_id":            observation["session_id"],
		"test_id":               observation["test_id"],
		"test_name":             observation["test_name"],
		"step_index":            observation["step_index"],
		"action_index":          observation["action_index"],
		"workflow_execution_id": report["workflow_execution_id"],
	}
	result := map[string]interface{}{
		"contract":   "atlas_report.v1",
		"app":        atlasAppSummary(app, graph),
		"projection": map[string]interface{}{"data_source": "evidence"},
		"requested": map[string]interface{}{
			"value":       requested,
			"resolved_as": resolvedAs,
		},
		"observation":  observation,
		"provenance":   provenance,
		"report":       report,
		"next_actions": atlasReportNextActions(graph, provenance),
	}
	if sourceScreen != nil {
		result["screen"] = atlasAgentScreen(sourceScreen)
	}
	return result
}

func atlasReportNextActions(graph api.AtlasResponse, provenance map[string]interface{}) []string {
	appID := atlasString(graph, "app_id")
	next := make([]string, 0, 4)
	if reportID := atlasString(provenance, "report_id"); reportID != "" {
		next = append(next, fmt.Sprintf("revyl atlas graph --app %s --report-id %s --json", appID, reportID))
	}
	if executionID := atlasString(provenance, "execution_id"); executionID != "" {
		next = append(next, fmt.Sprintf("revyl test report %s --json", executionID))
	} else if sessionID := atlasString(provenance, "session_id"); sessionID != "" {
		next = append(next, fmt.Sprintf("revyl device report --session-id %s --json", sessionID))
	}
	if workflowExecutionID := atlasString(provenance, "workflow_execution_id"); workflowExecutionID != "" {
		next = append(next, fmt.Sprintf("revyl workflow report %s --json", workflowExecutionID))
	}
	return next
}

func runAtlasNeighbors(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	_, app, graph, err := fetchAtlasAgentGraph(cmd, client)
	if err != nil {
		return err
	}
	node, err := resolveAtlasNode(graph, args[0])
	if err != nil {
		return err
	}
	result := buildAtlasNeighbors(app, graph, node, atlasDirection)
	return printAtlasContract(cmd, result, printAtlasNeighborsContract)
}

func runAtlasSearch(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	_, app, graph, err := fetchAtlasAgentGraph(cmd, client)
	if err != nil {
		return err
	}
	result := buildAtlasSearch(app, graph, args[0], atlasLimit)
	return printAtlasContract(cmd, result, printAtlasSearchContract)
}

func runAtlasEdge(cmd *cobra.Command, args []string) error {
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, app, graph, err := fetchAtlasAgentGraph(cmd, client)
	if err != nil {
		return err
	}
	source, err := resolveAtlasNode(graph, args[0])
	if err != nil {
		return err
	}
	target, err := resolveAtlasNode(graph, args[1])
	if err != nil {
		return err
	}
	result, matches := buildAtlasEdge(app, graph, source, target)
	if len(matches) == 0 {
		return fmt.Errorf("no observed transition from %s to %s", atlasScreenLabel(source), atlasScreenLabel(target))
	}
	if atlasEdgeRuns {
		evidence := make([]map[string]interface{}, 0, len(matches))
		for _, edge := range matches {
			runs, runsErr := client.GetAtlasEdgeRuns(
				cmd.Context(),
				query,
				atlasString(source, "id"),
				atlasString(target, "id"),
				atlasString(edge, "action_type"),
				atlasString(edge, "action_label"),
				5,
			)
			if runsErr != nil {
				return runsErr
			}
			evidence = append(evidence, map[string]interface{}{
				"action_type":  atlasString(edge, "action_type"),
				"action_label": atlasString(edge, "action_label"),
				"runs":         runs,
			})
		}
		result["evidence"] = evidence
		result["next_actions"] = append(
			atlasStringSlice(result["next_actions"]),
			atlasEdgeReportNextActions(query.AppID, evidence)...,
		)
	}
	return printAtlasContract(cmd, result, printAtlasEdgeContract)
}

func atlasEdgeReportNextActions(appID string, evidence []map[string]interface{}) []string {
	next := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, item := range evidence {
		response := atlasMap(item["runs"])
		runs := atlasMaps(response["runs"])
		if len(runs) == 0 {
			continue
		}
		run := runs[0]
		if reportID := atlasString(run, "report_id"); reportID != "" {
			command := fmt.Sprintf("revyl atlas graph --app %s --report-id %s --json", appID, reportID)
			if !seen[command] {
				next = append(next, command)
				seen[command] = true
			}
		}
		if executionID := atlasString(run, "execution_id"); executionID != "" {
			command := fmt.Sprintf("revyl test report %s --json", executionID)
			if !seen[command] {
				next = append(next, command)
				seen[command] = true
			}
		} else if sessionID := atlasString(run, "session_id"); sessionID != "" {
			command := fmt.Sprintf("revyl device report --session-id %s --json", sessionID)
			if !seen[command] {
				next = append(next, command)
				seen[command] = true
			}
		}
		if len(next) >= 3 {
			break
		}
	}
	return next
}

func printAtlasContract(cmd *cobra.Command, result map[string]interface{}, printer func(map[string]interface{})) error {
	if atlasJSONOutput(cmd) {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	printer(result)
	return nil
}

func buildAtlasBrief(app *api.App, graph api.AtlasResponse) map[string]interface{} {
	nodes := atlasMaps(graph["nodes"])
	connectivity := atlasConnectivity(graph)
	sort.SliceStable(nodes, func(i, j int) bool {
		left := connectivity[atlasString(nodes[i], "id")]
		right := connectivity[atlasString(nodes[j], "id")]
		if left != right {
			return left > right
		}
		return atlasInt(nodes[i]["observation_count"]) > atlasInt(nodes[j]["observation_count"])
	})
	anchors := atlasStartingAnchors(graph, nil)
	stats := atlasGraphStats(graph)
	name := atlasString(atlasAppSummary(app, graph), "name")
	if name == "" {
		name = atlasString(graph, "app_id")
	}
	result := map[string]interface{}{
		"contract":                  "atlas_brief.v2",
		"app":                       atlasAppSummary(app, graph),
		"summary":                   fmt.Sprintf("%s has %d screens connected by %d observed relationships across %d product areas.", name, atlasInt(stats["nodes"]), atlasInt(stats["edges"]), len(atlasMap(stats["product_areas"]))),
		"projection":                atlasProjectionContract(graph),
		"stats":                     stats,
		"product_areas":             atlasProductAreas(stats),
		"starting_anchors":          atlasLimitMaps(anchors, 8),
		"high_connectivity_screens": atlasConnectivityScreens(nodes, connectivity, 10),
		"visual_sample":             atlasVisualSample(nodes, 6),
		"curation":                  atlasCurationContract(graph),
	}
	appID := atlasString(graph, "app_id")
	next := []string{fmt.Sprintf("revyl atlas graph --app %s", appID)}
	if len(anchors) > 0 {
		id := atlasString(anchors[0], "id")
		next = append(next,
			fmt.Sprintf("revyl atlas screen %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", id, appID),
			fmt.Sprintf("revyl atlas neighbors %s --app %s", id, appID),
		)
	}
	result["next_actions"] = next
	return result
}

func buildAtlasKnowledgeGraph(app *api.App, graph api.AtlasResponse) map[string]interface{} {
	allNodes := atlasMaps(graph["nodes"])
	nodes := allNodes
	limit := atlasLimitOr(len(nodes))
	if limit > len(nodes) {
		limit = len(nodes)
	}
	nodes = nodes[:limit]
	returnedNodeIDs := map[string]bool{}
	for _, node := range nodes {
		returnedNodeIDs[atlasString(node, "id")] = true
	}
	nodeByID := atlasNodeIndex(graph)
	edges := make([]map[string]interface{}, 0, len(atlasMaps(graph["edges"])))
	for _, edge := range atlasMaps(graph["edges"]) {
		if !returnedNodeIDs[atlasString(edge, "source_entity_id")] || !returnedNodeIDs[atlasString(edge, "target_entity_id")] {
			continue
		}
		edges = append(edges, atlasAgentEdge(edge, nodeByID))
	}
	sort.SliceStable(edges, func(i, j int) bool {
		left := atlasString(atlasMap(edges[i]["source"]), "name") + "\x00" + atlasString(atlasMap(edges[i]["target"]), "name")
		right := atlasString(atlasMap(edges[j]["source"]), "name") + "\x00" + atlasString(atlasMap(edges[j]["target"]), "name")
		return left < right
	})
	appID := atlasString(graph, "app_id")
	truncated := limit < len(allNodes) || atlasBool(atlasMap(graph["projection"])["truncated"])
	return map[string]interface{}{
		"contract":         "atlas_graph.v1",
		"app":              atlasAppSummary(app, graph),
		"projection":       atlasProjectionContract(graph),
		"stats":            atlasGraphStats(graph),
		"returned":         map[string]interface{}{"nodes": len(nodes), "edges": len(edges)},
		"truncated":        truncated,
		"has_more":         truncated,
		"starting_anchors": atlasStartingAnchors(graph, returnedNodeIDs),
		"nodes":            atlasAgentScreens(nodes, len(nodes)),
		"edges":            edges,
		"curation":         atlasCurationContract(graph),
		"viewer_url":       graph["viewer_url"],
		"next_actions": []string{
			fmt.Sprintf("revyl atlas brief --app %s", appID),
			fmt.Sprintf("revyl atlas search \"<capability>\" --app %s", appID),
		},
	}
}

func atlasGraphStats(graph api.AtlasResponse) map[string]interface{} {
	stats := atlasMap(graph["stats"])
	return map[string]interface{}{
		"nodes":         len(atlasMaps(graph["nodes"])),
		"edges":         len(atlasMaps(graph["edges"])),
		"observations":  stats["observations"],
		"product_areas": stats["product_areas"],
	}
}

func atlasConnectivity(graph api.AtlasResponse) map[string]int {
	result := map[string]int{}
	for _, edge := range atlasMaps(graph["edges"]) {
		result[atlasString(edge, "source_entity_id")]++
		result[atlasString(edge, "target_entity_id")]++
	}
	return result
}

func atlasConnectivityScreens(nodes []map[string]interface{}, connectivity map[string]int, limit int) []map[string]interface{} {
	if limit <= 0 || limit > len(nodes) {
		limit = len(nodes)
	}
	result := make([]map[string]interface{}, 0, limit)
	for _, node := range nodes[:limit] {
		screen := atlasAgentScreen(node)
		screen["connections"] = connectivity[atlasString(node, "id")]
		result = append(result, screen)
	}
	return result
}

func atlasStartingAnchors(graph api.AtlasResponse, allowedIDs map[string]bool) []map[string]interface{} {
	nodes := atlasMaps(graph["nodes"])
	pinnedIDs := map[string]bool{}
	for _, id := range atlasStringSlice(atlasMap(graph["curation"])["pinned_entry_root_entity_ids"]) {
		pinnedIDs[id] = true
	}
	incoming := map[string]int{}
	outgoing := map[string]int{}
	for _, edge := range atlasMaps(graph["edges"]) {
		if atlasString(edge, "edge_kind") == "hierarchy" {
			continue
		}
		sourceID := atlasString(edge, "source_entity_id")
		targetID := atlasString(edge, "target_entity_id")
		if sourceID != "" {
			outgoing[sourceID]++
		}
		if targetID != "" {
			incoming[targetID]++
		}
	}
	anchors := make([]map[string]interface{}, 0)
	for _, node := range nodes {
		id := atlasString(node, "id")
		if allowedIDs != nil && !allowedIDs[id] {
			continue
		}
		reasons := make([]string, 0, 3)
		if pinnedIDs[id] {
			reasons = append(reasons, "curated_entry")
		}
		if atlasBool(node["is_entry_point"]) {
			reasons = append(reasons, "semantic_entry")
		}
		if incoming[id] == 0 {
			reasons = append(reasons, "observed_root")
		}
		if len(reasons) == 0 {
			continue
		}
		anchor := atlasAgentScreen(node)
		anchor["reasons"] = reasons
		anchor["incoming_edges"] = incoming[id]
		anchor["outgoing_edges"] = outgoing[id]
		anchors = append(anchors, anchor)
	}
	sort.SliceStable(anchors, func(i, j int) bool {
		leftReasons := atlasStringSlice(anchors[i]["reasons"])
		rightReasons := atlasStringSlice(anchors[j]["reasons"])
		leftRank := atlasAnchorReasonRank(leftReasons)
		rightRank := atlasAnchorReasonRank(rightReasons)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		leftConnections := atlasInt(anchors[i]["incoming_edges"]) + atlasInt(anchors[i]["outgoing_edges"])
		rightConnections := atlasInt(anchors[j]["incoming_edges"]) + atlasInt(anchors[j]["outgoing_edges"])
		if leftConnections != rightConnections {
			return leftConnections > rightConnections
		}
		leftObservations := atlasInt(anchors[i]["observations"])
		rightObservations := atlasInt(anchors[j]["observations"])
		if leftObservations != rightObservations {
			return leftObservations > rightObservations
		}
		return atlasString(anchors[i], "name") < atlasString(anchors[j], "name")
	})
	return anchors
}

func atlasAnchorReasonRank(reasons []string) int {
	for _, reason := range reasons {
		if reason == "curated_entry" {
			return 0
		}
	}
	for _, reason := range reasons {
		if reason == "observed_root" {
			return 1
		}
	}
	return 2
}

func resolveAtlasNode(graph api.AtlasResponse, value string) (map[string]interface{}, error) {
	want := strings.ToLower(strings.TrimSpace(value))
	exact := make([]map[string]interface{}, 0)
	partial := make([]map[string]interface{}, 0)
	for _, node := range atlasMaps(graph["nodes"]) {
		id := strings.ToLower(atlasString(node, "id"))
		name := strings.ToLower(atlasScreenLabel(node))
		if want == id || want == name {
			exact = append(exact, node)
			continue
		}
		if strings.Contains(name, want) {
			partial = append(partial, node)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("Atlas screen %q was not found; run 'revyl atlas search %q --app %s'", value, value, atlasString(graph, "app_id"))
	}
	names := make([]string, 0, len(matches))
	for _, node := range matches {
		names = append(names, fmt.Sprintf("%s (%s)", atlasScreenLabel(node), atlasString(node, "id")))
	}
	return nil, fmt.Errorf("Atlas screen %q is ambiguous: %s", value, strings.Join(names, ", "))
}

func buildAtlasScreen(app *api.App, graph api.AtlasResponse, node map[string]interface{}) map[string]interface{} {
	nodeID := atlasString(node, "id")
	nodeByID := atlasNodeIndex(graph)
	incoming, outgoing := atlasEdgesForNode(graph, nodeID, nodeByID)
	return map[string]interface{}{
		"contract":       "atlas_screen.v2",
		"app":            atlasAppSummary(app, graph),
		"projection":     atlasProjectionContract(graph),
		"screen":         atlasAgentScreen(node),
		"incoming_count": len(incoming),
		"outgoing_count": len(outgoing),
		"incoming":       atlasLimitMaps(incoming, 12),
		"outgoing":       atlasLimitMaps(outgoing, 12),
		"next_actions": []string{
			fmt.Sprintf("revyl atlas observations %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", nodeID, atlasString(graph, "app_id")),
			fmt.Sprintf("revyl atlas neighbors %s --app %s", nodeID, atlasString(graph, "app_id")),
		},
	}
}

func buildAtlasNeighbors(app *api.App, graph api.AtlasResponse, node map[string]interface{}, direction string) map[string]interface{} {
	nodeID := atlasString(node, "id")
	incoming, outgoing := atlasEdgesForNode(graph, nodeID, atlasNodeIndex(graph))
	if direction == "in" || direction == "inbound" {
		outgoing = []map[string]interface{}{}
	}
	if direction == "out" || direction == "outbound" {
		incoming = []map[string]interface{}{}
	}
	result := map[string]interface{}{
		"contract":       "atlas_neighbors.v1",
		"app":            atlasAppSummary(app, graph),
		"projection":     atlasProjectionContract(graph),
		"screen":         atlasAgentScreen(node),
		"incoming_count": len(incoming),
		"outgoing_count": len(outgoing),
		"incoming":       incoming,
		"outgoing":       outgoing,
	}
	result["next_actions"] = atlasTraversalNextActions(graph, nodeID, incoming, outgoing)
	return result
}

func buildAtlasSearch(app *api.App, graph api.AtlasResponse, query string, limit int) map[string]interface{} {
	type rankedNode struct {
		node    map[string]interface{}
		score   int
		matched []string
	}
	ranked := make([]rankedNode, 0)
	for _, node := range atlasMaps(graph["nodes"]) {
		score, matched := atlasNodeSearchScore(node, query)
		if score > 0 {
			ranked = append(ranked, rankedNode{node: node, score: score, matched: matched})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return atlasInt(ranked[i].node["observation_count"]) > atlasInt(ranked[j].node["observation_count"])
	})
	requestedLimit := limit
	if limit <= 0 || limit > len(ranked) {
		limit = len(ranked)
	}
	results := make([]map[string]interface{}, 0, limit)
	for _, match := range ranked[:limit] {
		item := atlasAgentScreen(match.node)
		item["relevance"] = match.score
		item["matched_fields"] = match.matched
		results = append(results, item)
	}
	truncated := atlasBool(atlasMap(graph["projection"])["truncated"]) || (requestedLimit > 0 && requestedLimit < len(ranked))
	result := map[string]interface{}{
		"contract":   "atlas_search.v1",
		"app":        atlasAppSummary(app, graph),
		"projection": atlasProjectionContract(graph),
		"query":      query,
		"count":      len(results),
		"results":    results,
		"truncated":  truncated,
		"has_more":   truncated,
	}
	if len(results) > 0 {
		id := atlasString(results[0], "id")
		appID := atlasString(graph, "app_id")
		result["next_actions"] = []string{
			fmt.Sprintf("revyl atlas screen %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", id, appID),
			fmt.Sprintf("revyl atlas neighbors %s --app %s", id, appID),
		}
	}
	return result
}

func buildAtlasEdge(app *api.App, graph api.AtlasResponse, source, target map[string]interface{}) (map[string]interface{}, []map[string]interface{}) {
	sourceID := atlasString(source, "id")
	targetID := atlasString(target, "id")
	nodeByID := atlasNodeIndex(graph)
	matches := make([]map[string]interface{}, 0)
	transitions := make([]map[string]interface{}, 0)
	for _, edge := range atlasMaps(graph["edges"]) {
		if atlasString(edge, "source_entity_id") != sourceID || atlasString(edge, "target_entity_id") != targetID {
			continue
		}
		matches = append(matches, edge)
		transitions = append(transitions, atlasAgentEdge(edge, nodeByID))
	}
	return map[string]interface{}{
		"contract":         "atlas_edge.v1",
		"app":              atlasAppSummary(app, graph),
		"projection":       atlasProjectionContract(graph),
		"source":           atlasAgentScreen(source),
		"target":           atlasAgentScreen(target),
		"transition_count": len(transitions),
		"transitions":      transitions,
		"next_actions": []string{
			fmt.Sprintf("revyl atlas observations %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", sourceID, atlasString(graph, "app_id")),
			fmt.Sprintf("revyl atlas observations %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", targetID, atlasString(graph, "app_id")),
		},
	}, matches
}

func atlasTraversalNextActions(graph api.AtlasResponse, nodeID string, incoming, outgoing []map[string]interface{}) []string {
	appID := atlasString(graph, "app_id")
	next := []string{
		fmt.Sprintf("revyl atlas observations %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", nodeID, appID),
		fmt.Sprintf("revyl atlas report %s --app %s --json", nodeID, appID),
	}
	for _, edge := range outgoing {
		targetID := atlasString(atlasMap(edge["target"]), "id")
		if targetID == "" || len(next) >= 5 {
			continue
		}
		next = append(next,
			fmt.Sprintf("revyl atlas screen %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", targetID, appID),
			fmt.Sprintf("revyl atlas edge %s %s --app %s --runs --json", nodeID, targetID, appID),
		)
	}
	for _, edge := range incoming {
		sourceID := atlasString(atlasMap(edge["source"]), "id")
		if sourceID == "" || len(next) >= 5 {
			continue
		}
		next = append(next, fmt.Sprintf("revyl atlas screen %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", sourceID, appID))
	}
	if len(next) > 5 {
		return next[:5]
	}
	return next
}

func atlasNodeIndex(graph api.AtlasResponse) map[string]map[string]interface{} {
	result := map[string]map[string]interface{}{}
	for _, node := range atlasMaps(graph["nodes"]) {
		result[atlasString(node, "id")] = node
	}
	return result
}

func atlasEdgesForNode(graph api.AtlasResponse, nodeID string, nodeByID map[string]map[string]interface{}) ([]map[string]interface{}, []map[string]interface{}) {
	incoming := make([]map[string]interface{}, 0)
	outgoing := make([]map[string]interface{}, 0)
	for _, edge := range atlasMaps(graph["edges"]) {
		if atlasString(edge, "source_entity_id") == nodeID {
			outgoing = append(outgoing, atlasAgentEdge(edge, nodeByID))
		}
		if atlasString(edge, "target_entity_id") == nodeID {
			incoming = append(incoming, atlasAgentEdge(edge, nodeByID))
		}
	}
	sort.SliceStable(incoming, func(i, j int) bool {
		return atlasInt(incoming[i]["observations"]) > atlasInt(incoming[j]["observations"])
	})
	sort.SliceStable(outgoing, func(i, j int) bool {
		return atlasInt(outgoing[i]["observations"]) > atlasInt(outgoing[j]["observations"])
	})
	return incoming, outgoing
}

func atlasNodeSearchScore(node map[string]interface{}, query string) (int, []string) {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return 0, nil
	}
	fields := []struct {
		name   string
		value  string
		weight int
	}{
		{"name", atlasScreenLabel(node), 40},
		{"description", atlasString(node, "semantic_description"), 16},
		{"product_area", atlasString(node, "product_area"), 12},
		{"screen_kind", atlasString(node, "screen_kind"), 10},
		{"primary_actions", strings.Join(atlasStringSlice(node["primary_actions"]), " "), 14},
		{"primary_objects", strings.Join(atlasStringSlice(node["primary_objects"]), " "), 14},
		{"visible_labels", strings.Join(atlasStringSlice(atlasMap(node["semantic_summary"])["visible_labels"]), " "), 8},
	}
	matchedSet := map[string]bool{}
	score := 0
	for _, term := range terms {
		termMatched := false
		for _, field := range fields {
			value := strings.ToLower(field.value)
			if strings.Contains(value, term) {
				termMatched = true
				matchedSet[field.name] = true
				score += field.weight
			}
		}
		if !termMatched {
			return 0, nil
		}
	}
	if strings.EqualFold(strings.TrimSpace(query), atlasScreenLabel(node)) {
		score += 100
	}
	matched := make([]string, 0, len(matchedSet))
	for field := range matchedSet {
		matched = append(matched, field)
	}
	sort.Strings(matched)
	return score, matched
}

func atlasProjectionContract(graph api.AtlasResponse) map[string]interface{} {
	projection := atlasMap(graph["projection"])
	return map[string]interface{}{
		"data_source": projection["data_source"],
	}
}

func atlasEvidenceProjection(query api.AtlasQuery) map[string]interface{} {
	return map[string]interface{}{
		"data_source": "evidence",
	}
}

func atlasCurationContract(graph api.AtlasResponse) map[string]interface{} {
	curation := atlasMap(graph["curation"])
	return map[string]interface{}{
		"hidden_screens": len(atlasSlice(curation["hidden_root_entity_ids"])),
		"pinned_entries": len(atlasSlice(curation["pinned_entry_root_entity_ids"])),
	}
}

func atlasAgentScreens(nodes []map[string]interface{}, limit int) []map[string]interface{} {
	if limit <= 0 || limit > len(nodes) {
		limit = len(nodes)
	}
	result := make([]map[string]interface{}, 0, limit)
	for _, node := range nodes[:limit] {
		result = append(result, atlasAgentScreen(node))
	}
	return result
}

func atlasVisualSample(nodes []map[string]interface{}, limit int) []map[string]interface{} {
	if limit <= 0 || len(nodes) == 0 {
		return []map[string]interface{}{}
	}
	selectedIDs := map[string]bool{}
	selectedAreas := map[string]bool{}
	result := make([]map[string]interface{}, 0, min(limit, len(nodes)))
	appendNode := func(node map[string]interface{}) {
		id := atlasString(node, "id")
		if selectedIDs[id] || len(result) >= limit {
			return
		}
		selectedIDs[id] = true
		result = append(result, atlasAgentScreen(node))
	}
	for _, node := range nodes {
		area := atlasString(node, "product_area")
		if area == "" || selectedAreas[area] {
			continue
		}
		selectedAreas[area] = true
		appendNode(node)
	}
	for _, node := range nodes {
		appendNode(node)
	}
	return result
}

func atlasAgentScreen(node map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"id":                            atlasString(node, "id"),
		"name":                          atlasScreenLabel(node),
		"description":                   node["semantic_description"],
		"product_area":                  node["product_area"],
		"screen_kind":                   node["screen_kind"],
		"observations":                  atlasInt(node["observation_count"]),
		"primary_actions":               node["primary_actions"],
		"primary_objects":               node["primary_objects"],
		"representative_observation_id": node["representative_observation_id"],
		"landmarks": map[string]interface{}{
			"entry":     atlasBool(node["is_entry_point"]),
			"hub":       atlasBool(node["is_hub"]),
			"terminal":  atlasBool(node["is_terminal"]),
			"transient": atlasBool(node["is_transient"]),
		},
	}
	if node["screenshot_url"] != nil {
		result["screenshot_url"] = node["screenshot_url"]
	}
	if node["local_screenshot_path"] != nil {
		result["local_screenshot_path"] = node["local_screenshot_path"]
	}
	return result
}

func atlasAgentEdge(edge map[string]interface{}, nodeByID map[string]map[string]interface{}) map[string]interface{} {
	sourceID := atlasString(edge, "source_entity_id")
	targetID := atlasString(edge, "target_entity_id")
	actionType := atlasString(edge, "action_type")
	actionLabel := atlasString(edge, "action_label")
	label := atlasString(edge, "action_label")
	if label == "" {
		label = actionType
	}
	relationType := atlasString(edge, "edge_kind")
	if relationType == "" || relationType == "transition" {
		relationType = "observed_transition"
	}
	return map[string]interface{}{
		"edge_key":         sourceID + "->" + targetID + "|" + relationType + "|" + actionType + "|" + actionLabel,
		"label":            label,
		"source":           atlasScreenReference(nodeByID[sourceID], sourceID),
		"target":           atlasScreenReference(nodeByID[targetID], targetID),
		"relation_type":    relationType,
		"action_type":      actionType,
		"action_label":     actionLabel,
		"action_target":    edge["action_target"],
		"description":      edge["action_description"],
		"observations":     atlasInt(edge["observation_count"]),
		"has_test_support": atlasInt(edge["test_support"]) > 0,
	}
}

func atlasScreenReference(node map[string]interface{}, fallbackID string) map[string]interface{} {
	if node == nil {
		return map[string]interface{}{"id": fallbackID}
	}
	return map[string]interface{}{
		"id":           atlasString(node, "id"),
		"name":         atlasScreenLabel(node),
		"product_area": node["product_area"],
	}
}

func atlasLimitMaps(items []map[string]interface{}, limit int) []map[string]interface{} {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func printAtlasBrief(result map[string]interface{}) {
	ui.PrintInfo("%s", atlasString(result, "summary"))
	projection := atlasMap(result["projection"])
	ui.PrintDim("  source=%s", atlasString(projection, "data_source"))
	printAtlasProductAreas(result["product_areas"])
	printAtlasAnchors(result["starting_anchors"])
	printAtlasNamedList("Highly connected screens", result["high_connectivity_screens"])
	printAtlasNamedList("Visual evidence sample", result["visual_sample"])
	ui.Println()
	ui.PrintDim("Starting anchors are traversal hints, not a parent/child hierarchy. Inspect their screenshots, then follow observed edges in both directions.")
	printAtlasNext(result["next_actions"])
}

func printAtlasKnowledgeGraph(result map[string]interface{}) {
	app := atlasMap(result["app"])
	stats := atlasMap(result["stats"])
	returned := atlasMap(result["returned"])
	ui.PrintInfo("%s knowledge graph", atlasString(app, "name"))
	ui.PrintDim("  %d screens, %d observed relationships, %d observations", atlasInt(stats["nodes"]), atlasInt(stats["edges"]), atlasInt(stats["observations"]))
	if atlasBool(result["truncated"]) {
		ui.PrintDim("  response is truncated; increase --limit before treating this as the complete graph")
	} else if atlasInt(returned["nodes"]) != atlasInt(stats["nodes"]) || atlasInt(returned["edges"]) != atlasInt(stats["edges"]) {
		ui.PrintDim("  returned %d screens and %d relationships; increase --limit for more", atlasInt(returned["nodes"]), atlasInt(returned["edges"]))
	}
	printAtlasURL("Viewer", result["viewer_url"])
	printAtlasAnchors(result["starting_anchors"])
	printAtlasGraphEdges("Connections", result["edges"])
	ui.Println()
	ui.PrintDim("No parent, primary path, or preferred journey is inferred. Begin at an anchor, inspect its media, then traverse each relevant edge.")
	if !atlasIncludeVariants {
		ui.PrintDim("Use --include-variants when state variants are relevant to the question.")
	}
	printAtlasNext(result["next_actions"])
}

func printAtlasScreenContract(result map[string]interface{}) {
	screen := atlasMap(result["screen"])
	ui.PrintInfo("%s", atlasString(screen, "name"))
	ui.PrintDim("  %s / %s  observations=%d", atlasString(screen, "product_area"), atlasString(screen, "screen_kind"), atlasInt(screen["observations"]))
	if description := atlasString(screen, "description"); description != "" {
		ui.PrintDim("  %s", description)
	}
	printAtlasStringList("Primary actions", screen["primary_actions"])
	printAtlasNamedList("Incoming transitions", result["incoming"])
	printAtlasNamedList("Outgoing transitions", result["outgoing"])
	ui.Println()
	ui.PrintDim("Open this screen's representative and grouped screenshots before relying on its generated name or summary.")
	printAtlasNext(result["next_actions"])
}

func printAtlasReportContract(result map[string]interface{}) {
	app := atlasMap(result["app"])
	provenance := atlasMap(result["provenance"])
	report := atlasMap(result["report"])
	title := atlasString(report, "test_name")
	if title == "" {
		title = atlasString(provenance, "report_id")
	}
	if title == "" {
		title = atlasString(provenance, "observation_id")
	}
	ui.PrintInfo("Atlas evidence report: %s", title)
	ui.PrintDim("  app=%s  observation=%s", atlasString(app, "name"), atlasString(provenance, "observation_id"))
	if screen := atlasMap(result["screen"]); len(screen) > 0 {
		ui.PrintDim("  representative screen=%s (%s)", atlasString(screen, "name"), atlasString(screen, "id"))
	}
	ui.PrintDim(
		"  report=%s  execution=%s  session=%s",
		atlasString(provenance, "report_id"),
		atlasString(provenance, "execution_id"),
		atlasString(provenance, "session_id"),
	)
	if goal := atlasString(report, "test_goal_summary"); goal != "" {
		ui.PrintDim("  goal: %s", goal)
	}
	if success, ok := report["success"].(bool); ok {
		ui.PrintDim("  success=%t  steps=%d", success, atlasInt(report["total_steps"]))
	}
	ui.Println()
	ui.PrintDim("Review the originating goal, steps, and actions before classifying unexpected Atlas evidence as a product path, test setup, redirect, or bad observation.")
	printAtlasNext(result["next_actions"])
}

func printAtlasNeighborsContract(result map[string]interface{}) {
	screen := atlasMap(result["screen"])
	ui.PrintInfo("Neighbors: %s", atlasString(screen, "name"))
	ui.PrintDim("  %d incoming, %d outgoing", atlasInt(result["incoming_count"]), atlasInt(result["outgoing_count"]))
	printAtlasNamedList("Incoming", result["incoming"])
	printAtlasNamedList("Outgoing", result["outgoing"])
	ui.Println()
	ui.PrintDim("Traverse one edge at a time. Open each destination screenshot; if a connection is unclear, inspect its run video and work backward from the recorded action.")
	printAtlasNext(result["next_actions"])
}

func printAtlasSearchContract(result map[string]interface{}) {
	ui.PrintInfo("Atlas search: %s (%d matches)", atlasString(result, "query"), atlasInt(result["count"]))
	printAtlasNamedList("Screens", result["results"])
	if atlasBool(result["truncated"]) {
		ui.PrintDim("Search covered a truncated graph or result set; increase --limit before treating these as all matches.")
	}
	ui.PrintDim("Search locates candidate nodes; it does not explain the path. Inspect the media, then traverse neighbors.")
	printAtlasNext(result["next_actions"])
}

func printAtlasEdgeContract(result map[string]interface{}) {
	source := atlasMap(result["source"])
	target := atlasMap(result["target"])
	ui.PrintInfo("%s -> %s", atlasString(source, "name"), atlasString(target, "name"))
	printAtlasNamedList("Observed transitions", result["transitions"])
	evidence := atlasMaps(result["evidence"])
	if len(evidence) == 0 {
		ui.PrintDim("  Re-run with --runs --json, then watch the recorded clips before interpreting an unclear connection.")
	} else {
		runCount := 0
		for _, item := range evidence {
			runCount += len(atlasSlice(item["runs"]))
		}
		ui.PrintDim("  recent evidence runs=%d (video details are in JSON output)", runCount)
	}
	printAtlasNext(result["next_actions"])
}

func buildAtlasAreaGraph(app *api.App, graph api.AtlasResponse, area string) map[string]interface{} {
	nodes := atlasMaps(graph["nodes"])
	want := strings.ToLower(strings.TrimSpace(area))
	areaSet := map[string]bool{}
	for _, node := range nodes {
		if productArea := atlasString(node, "product_area"); productArea != "" {
			areaSet[productArea] = true
		}
	}
	availableAreas := make([]string, 0, len(areaSet))
	for productArea := range areaSet {
		availableAreas = append(availableAreas, productArea)
	}
	sort.Strings(availableAreas)
	matches := make([]map[string]interface{}, 0)
	for _, node := range nodes {
		if strings.EqualFold(atlasString(node, "product_area"), want) {
			matches = append(matches, node)
		}
	}
	if len(matches) == 0 {
		for _, node := range nodes {
			if strings.Contains(strings.ToLower(atlasString(node, "product_area")), want) {
				matches = append(matches, node)
			}
		}
	}
	matchedIDs := map[string]bool{}
	for _, node := range matches {
		matchedIDs[atlasString(node, "id")] = true
	}
	nodeByID := atlasNodeIndex(graph)
	edges := make([]map[string]interface{}, 0)
	boundaryEntryIDs := map[string]bool{}
	internalCount := 0
	boundaryCount := 0
	for _, edge := range atlasMaps(graph["edges"]) {
		sourceID := atlasString(edge, "source_entity_id")
		targetID := atlasString(edge, "target_entity_id")
		sourceInside := matchedIDs[sourceID]
		targetInside := matchedIDs[targetID]
		if !sourceInside && !targetInside {
			continue
		}
		contract := atlasAgentEdge(edge, nodeByID)
		scope := "internal"
		if sourceInside != targetInside {
			boundaryCount++
			if targetInside {
				scope = "inbound_boundary"
				boundaryEntryIDs[targetID] = true
			} else {
				scope = "outbound_boundary"
			}
		} else {
			internalCount++
		}
		contract["scope"] = scope
		edges = append(edges, contract)
	}
	anchors := atlasStartingAnchors(graph, matchedIDs)
	anchorIDs := map[string]bool{}
	for _, anchor := range anchors {
		anchorIDs[atlasString(anchor, "id")] = true
	}
	for id := range boundaryEntryIDs {
		if anchorIDs[id] || nodeByID[id] == nil {
			continue
		}
		anchor := atlasAgentScreen(nodeByID[id])
		anchor["reasons"] = []string{"area_entry"}
		anchors = append(anchors, anchor)
	}
	appID := atlasString(graph, "app_id")
	return map[string]interface{}{
		"contract":         "atlas_area.v2",
		"app":              atlasAppSummary(app, graph),
		"projection":       atlasProjectionContract(graph),
		"area":             area,
		"starting_anchors": anchors,
		"nodes":            atlasAgentScreens(matches, atlasLimitOr(len(matches))),
		"edges":            atlasLimitMaps(edges, atlasLimitOr(len(edges))),
		"screen_count":     len(matches),
		"edge_count":       len(edges),
		"internal_edges":   internalCount,
		"boundary_edges":   boundaryCount,
		"available_areas":  availableAreas,
		"next_actions":     atlasAreaNextActionsForApp(matches, appID),
	}
}

func printAtlasAreaSummary(result map[string]interface{}) {
	ui.PrintInfo("Atlas area: %s", result["area"])
	ui.PrintDim("  %d screens, %d internal edges, %d boundary edges", atlasInt(result["screen_count"]), atlasInt(result["internal_edges"]), atlasInt(result["boundary_edges"]))
	printAtlasAnchors(result["starting_anchors"])
	printAtlasNamedList("Screens", result["nodes"])
	printAtlasGraphEdges("Internal and boundary connections", result["edges"])
	ui.Println()
	ui.PrintDim("Boundary edges are preserved so the area remains connected to the rest of the app graph.")
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

func atlasBool(value interface{}) bool {
	result, _ := value.(bool)
	return result
}

func atlasAreaNextActionsForApp(screens []map[string]interface{}, appID string) []string {
	var next []string
	for _, screen := range screens {
		if len(next) >= 4 {
			break
		}
		id := atlasString(screen, "id")
		if id != "" {
			next = append(next,
				fmt.Sprintf("revyl atlas screen %s --app %s --screenshots --screenshot-dir /tmp/atlas-shots", id, appID),
				fmt.Sprintf("revyl atlas neighbors %s --app %s", id, appID),
			)
		}
	}
	if len(next) > 4 {
		return next[:4]
	}
	return next
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

func printAtlasAnchors(value interface{}) {
	anchors := atlasMaps(value)
	if len(anchors) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Starting anchors:")
	for _, anchor := range anchors {
		reasons := strings.Join(atlasStringSlice(anchor["reasons"]), ", ")
		ui.PrintDim("  %s  [%s]  in=%d out=%d obs=%d", atlasString(anchor, "name"), reasons, atlasInt(anchor["incoming_edges"]), atlasInt(anchor["outgoing_edges"]), atlasInt(anchor["observations"]))
		ui.PrintDim("    %s", atlasString(anchor, "id"))
	}
}

func printAtlasGraphEdges(title string, value interface{}) {
	edges := atlasMaps(value)
	if len(edges) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("%s:", title)
	for _, edge := range edges {
		source := atlasMap(edge["source"])
		target := atlasMap(edge["target"])
		label := atlasString(edge, "label")
		if label == "" {
			label = atlasString(edge, "relation_type")
		}
		scope := atlasString(edge, "scope")
		if scope != "" {
			scope = " " + scope
		}
		ui.PrintDim("  %s --[%s; obs=%d%s]--> %s", atlasString(source, "name"), label, atlasInt(edge["observations"]), scope, atlasString(target, "name"))
		ui.PrintDim("    %s", atlasString(edge, "edge_key"))
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
		ui.PrintDim("  %s", atlasNamedListSummary(item))
		if id := atlasString(item, "id"); id != "" {
			ui.PrintDim("    %s", id)
		}
	}
}

func atlasNamedListSummary(item map[string]interface{}) string {
	label := atlasString(item, "label")
	if label == "" {
		label = atlasString(item, "name")
	}
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
		return fmt.Sprintf("%s  support=%d", label, support)
	}
	if relation := atlasString(item, "relation"); relation != "" {
		return fmt.Sprintf("%s  %s confidence=%v", label, relation, item["confidence"])
	}
	observationCount, hasObservations := item["observations"]
	if !hasObservations {
		observationCount, hasObservations = item["observation_count"]
	}
	if connections, hasConnections := item["connections"]; hasConnections {
		if hasObservations {
			return fmt.Sprintf("%s  connections=%d obs=%d", label, atlasInt(connections), atlasInt(observationCount))
		}
		return fmt.Sprintf("%s  connections=%d", label, atlasInt(connections))
	}
	variantCount, hasVariants := item["variant_count"]
	if hasObservations && hasVariants {
		return fmt.Sprintf("%s  obs=%d variants=%d", label, atlasInt(observationCount), atlasInt(variantCount))
	}
	if hasObservations {
		return fmt.Sprintf("%s  obs=%d", label, atlasInt(observationCount))
	}
	if hasVariants {
		return fmt.Sprintf("%s  variants=%d", label, atlasInt(variantCount))
	}
	return label
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
	switch item := value.(type) {
	case api.AtlasResponse:
		return map[string]interface{}(item)
	case map[string]interface{}:
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
	case []string:
		out := make([]interface{}, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	}
	return nil
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
