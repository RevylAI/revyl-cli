// Package main: revyl CLI subcommands for inspecting **finished** test runs.
//
// `revyl run {summary,identity,state}` is the parallel surface to
// `revyl device state` (live dev-loop sessions). Both share the same
// device-state JSONL contract, but `run` operates against the
// recorded artifact fetched from the backend via task_id.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/runinspect"
)

var runCmd = &cobra.Command{
	Use:   "run [task_id]",
	Short: "Inspect a test run (summary, state, logs, network, perf, trace)",
	Long: `Inspect a test run by its task_id.

'revyl run {logs,network,perf,state}' work whether the run is still
executing or already finished: while it runs they read the in-progress
capture the worker ships to S3 per step (labelled partial); once it
finishes they read the finalized artifacts. (summary and trace are
post-run only.)

Bare 'revyl run <task_id>' is shorthand for 'revyl run summary <task_id>'.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRootRun,
}

// runRootRun routes bare 'revyl run <task_id>' to the summary subcommand.
func runRootRun(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return runSummaryRun(cmd, args)
}

// --- run summary ----------------------------------------------------------

var runSummaryCmd = &cobra.Command{
	Use:   "summary <task_id>",
	Short: "Show a finished test run's outcome, step list, and identity highlights",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl run summary 7aa6ce3c-70e0-4d25-b4d9-e31fd6c62b23
  revyl run summary 7aa6ce3c-... --json`,
	RunE: runSummaryRun,
}

func runSummaryRun(cmd *cobra.Command, args []string) error {
	taskID := strings.TrimSpace(args[0])
	report, lines, err := loadRunArtifacts(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	identity := runinspect.DetectIdentityHighlights(lines, report)
	summary := runinspect.BuildSummary(report, identity)
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(summary)
	}
	return printSummary(cmd, summary)
}

// --- run state ------------------------------------------------------------

var runStateCmd = &cobra.Command{
	Use:   "state <task_id> [--path P] [--at-step N]",
	Short: "Inspect the captured UserDefaults / SQLite for a run",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl run state 7aa6ce3c-...                                  # list captured paths
  revyl run state 7aa6ce3c-... --path Library/Preferences/com.x.plist
  revyl run state 7aa6ce3c-... --path Documents/cache.sqlite3 --at-step 5
  revyl run state 7aa6ce3c-... --download --output ./state.jsonl.gz`,
	RunE: runStateRun,
}

func runStateRun(cmd *cobra.Command, args []string) error {
	taskID := strings.TrimSpace(args[0])
	report, client, err := loadReportOnly(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	// Liveness is advisory — it only frames messaging ("not yet available"
	// while running vs "not available" on a finished run). A failed session
	// lookup (deleted/expired session, transient error) must not block reading
	// artifacts whose presigned URLs are already in the report, so this is
	// best-effort: on error we fall back to the not-live zero value.
	live, _ := runinspect.ResolveRunLiveness(cmd.Context(), client, report)
	partial := report.DeviceStatePartial
	download, _ := cmd.Flags().GetBool("download")

	if report.DeviceStateURL == "" {
		if live.Live {
			return liveNotYetAvailable("device state", taskID)
		}
		return fmt.Errorf("no device state captured for this run")
	}
	if download {
		if isLiveURL(report.DeviceStateURL) {
			return errLiveDownload
		}
		return runStateDownload(cmd, taskID)
	}

	// The live (in-progress) and finalized device_state objects share the
	// JSONL format, so the same fetch+parse path serves both — including
	// the per-step --path / --at-step views, which read the records
	// accumulated so far. Bypass the on-disk cache for a still-growing
	// live object so a later finished-run read isn't served a stale copy.
	cacheDir := runinspect.DefaultCacheDir()
	if isLiveURL(report.DeviceStateURL) {
		cacheDir = ""
	}
	lines, err := runinspect.FetchDeviceStateLines(
		cmd.Context(), report, http.DefaultClient, cacheDir,
	)
	if err != nil {
		if errors.Is(err, runinspect.ErrNoDeviceStateArtifact) {
			return fmt.Errorf("no device state captured for this run")
		}
		return fmt.Errorf("fetch device-state artifact: %w", err)
	}
	path, _ := cmd.Flags().GetString("path")
	atStep, _ := cmd.Flags().GetInt("at-step")
	indexer := runinspect.IndexerFromReport(report)

	if path == "" {
		// No --path: list every captured path + brief metadata.
		listing := runinspect.ListCapturedPaths(lines, indexer, atStep)
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"task_id": taskID,
				"paths":   listing,
				"partial": partial,
			})
		}
		if partial {
			printPartialNote(cmd)
		}
		return printStateListing(cmd, listing)
	}

	snapshot := runinspect.LatestStateForPath(lines, indexer, path, atStep)
	if snapshot == nil {
		return fmt.Errorf("no captured state for path %q in this run", path)
	}
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"task_id":  taskID,
			"path":     path,
			"snapshot": snapshot,
			"partial":  partial,
		})
	}
	if partial {
		printPartialNote(cmd)
	}
	return printStateSnapshot(cmd, path, snapshot)
}

// errLiveDownload is returned when --download is used against a run that
// is still executing: the raw .gz artifacts are only created when the run
// uploads them at the end, so there is nothing to download yet.
var errLiveDownload = fmt.Errorf("--download fetches the recorded artifact, which isn't available until the run completes")

// liveNotYetAvailable explains why an artifact can't be read yet for a
// run that is still executing: the per-step shipper uploads capture
// incrementally, so the object may not have landed for the first time
// yet. Retrying shortly (or after completion) resolves it.
func liveNotYetAvailable(kind, taskID string) error {
	return fmt.Errorf(
		"%s isn't available yet for this run — capture is uploaded incrementally while the run executes. Retry in a moment, or once the run finishes (task %s)",
		kind, taskID,
	)
}

// partialNote labels output sourced from an in-progress live object
// rather than the finalized end-of-run artifact.
const partialNote = "(partial — run still in progress; finalized data available after completion)"

func printPartialNote(cmd *cobra.Command) {
	fmt.Fprintln(cmd.OutOrStdout(), partialNote)
}

// isLiveURL reports whether a presigned artifact URL points at an
// in-progress {session_id}/live/ object. The cache bypass and the
// --download refusal key on this rather than the report's partial flag:
// the URL path is ground truth, so a stale or wrong flag can never cause
// a still-growing object to be cached under the finalized key (the
// poisoning that bit us once) or handed to --download.
func isLiveURL(url string) bool {
	return runinspect.IsLiveURL(url)
}

// runStateDownload streams the raw device_state.jsonl.gz artifact to
// disk without parsing it — symmetric with the network / perf / logs
// --download flow.
func runStateDownload(cmd *cobra.Command, taskID string) error {
	report, _, err := loadReportOnly(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	if report.DeviceStateURL == "" {
		return fmt.Errorf("no device state uploaded for this run")
	}
	outPath, _ := cmd.Flags().GetString("output")
	if outPath == "" {
		outPath = "device_state.jsonl.gz"
	}
	written, err := runinspect.DownloadArtifactToFile(
		cmd.Context(), report.DeviceStateURL, http.DefaultClient, outPath,
	)
	if err != nil {
		return err
	}
	jsonOrPrint(cmd, map[string]any{
		"task_id":       taskID,
		"artifact":      "device_state",
		"downloaded_to": outPath,
		"bytes":         written,
	}, fmt.Sprintf("Downloaded device state to %s (%d bytes)", outPath, written))
	return nil
}

// --- run logs -------------------------------------------------------------

var runLogsCmd = &cobra.Command{
	Use:   "logs <task_id>",
	Short: "Show captured device logs (iOS os_log / Android logcat) for a run (live or finished)",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl run logs 7aa6ce3c-...
  revyl run logs 7aa6ce3c-... --grep segment
  revyl run logs 7aa6ce3c-... --level warn,error --tail 50
  revyl run logs 7aa6ce3c-... --download --output ./logs.txt.gz`,
	RunE: runLogsRun,
}

func runLogsRun(cmd *cobra.Command, args []string) error {
	taskID := strings.TrimSpace(args[0])
	report, client, err := loadReportOnly(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	// Liveness is advisory — it only frames messaging ("not yet available"
	// while running vs "not available" on a finished run). A failed session
	// lookup (deleted/expired session, transient error) must not block reading
	// artifacts whose presigned URLs are already in the report, so this is
	// best-effort: on error we fall back to the not-live zero value.
	live, _ := runinspect.ResolveRunLiveness(cmd.Context(), client, report)
	download, _ := cmd.Flags().GetBool("download")

	// The device-logs endpoint resolves the finalized object when present
	// and otherwise falls back to the in-progress {session_id}/live/
	// object, flagging the response partial. Logs share their text format
	// both ways, so the parser is identical — only caching and --download
	// (post-run only) and the partial note differ.
	logsResp, err := runinspect.ResolveDeviceLogsURL(cmd.Context(), client, report.ReportID)
	if err != nil {
		if errors.Is(err, runinspect.ErrArtifactNotAvailable) {
			if live.Live {
				return liveNotYetAvailable("device logs", taskID)
			}
			return fmt.Errorf("no device logs uploaded for this run")
		}
		return err
	}
	partial := logsResp.Partial != nil && *logsResp.Partial
	fromLive := isLiveURL(logsResp.DownloadUrl)

	if download {
		if fromLive {
			return errLiveDownload
		}
		outPath, _ := cmd.Flags().GetString("output")
		if outPath == "" {
			outPath = logsResp.Filename
			if outPath == "" {
				outPath = "device_logs.txt.gz"
			}
		}
		written, err := runinspect.DownloadArtifactToFile(
			cmd.Context(), logsResp.DownloadUrl, http.DefaultClient, outPath,
		)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]any{
			"task_id":       taskID,
			"artifact":      "device_logs",
			"downloaded_to": outPath,
			"bytes":         written,
		}, fmt.Sprintf("Downloaded device logs to %s (%d bytes)", outPath, written))
		return nil
	}
	// Bypass the on-disk cache for a still-growing live object.
	cacheDir := runinspect.DefaultCacheDir()
	if fromLive {
		cacheDir = ""
	}
	raw, err := runinspect.FetchArtifactBytes(
		cmd.Context(), logsResp.DownloadUrl, http.DefaultClient,
		cacheDir, taskID, "device_logs.txt", logsResp.Compressed,
	)
	if err != nil {
		return err
	}
	lines := runinspect.ParseLogText(raw)

	filter := runinspect.LogFilter{}
	if pattern, _ := cmd.Flags().GetString("grep"); pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("--grep: %w", err)
		}
		filter.Grep = re
	}
	if levelTokens, _ := cmd.Flags().GetStringSlice("level"); len(levelTokens) > 0 {
		filter.Levels = runinspect.NormaliseLevels(levelTokens)
	}
	filtered := filter.Apply(lines)
	if tail, _ := cmd.Flags().GetInt("tail"); tail > 0 {
		filtered = runinspect.TailN(filtered, tail)
	}

	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"task_id": taskID,
			"lines":   filtered,
			"total":   len(lines),
			"matched": len(filtered),
			"partial": partial,
		})
	}
	if partial {
		printPartialNote(cmd)
	}
	return printLogLines(cmd, filtered, len(lines))
}

func printLogLines(cmd *cobra.Command, filtered []runinspect.LogLine, total int) error {
	w := cmd.OutOrStdout()
	if len(filtered) == 0 {
		if total == 0 {
			fmt.Fprintln(w, "(no log lines captured for this run)")
			return nil
		}
		fmt.Fprintf(w, "(no lines matched out of %d total)\n", total)
		return nil
	}
	for _, l := range filtered {
		fmt.Fprintln(w, l.Raw)
	}
	if len(filtered) != total {
		fmt.Fprintf(w, "\n%d of %d lines matched.\n", len(filtered), total)
	}
	return nil
}

// --- run network ----------------------------------------------------------

var runNetworkCmd = &cobra.Command{
	Use:   "network <task_id>",
	Short: "Show captured HTTP traffic for a run (iOS only; live or finished)",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl run network 7aa6ce3c-...                          # host rollup
  revyl run network 7aa6ce3c-... --host segment
  revyl run network 7aa6ce3c-... --status 4xx,5xx
  revyl run network 7aa6ce3c-... --grep anonymousId --body
  revyl run network 7aa6ce3c-... --download --output ./net.json.gz`,
	RunE: runNetworkRun,
}

func runNetworkRun(cmd *cobra.Command, args []string) error {
	taskID := strings.TrimSpace(args[0])
	report, client, err := loadReportOnly(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	// Liveness is advisory — it only frames messaging ("not yet available"
	// while running vs "not available" on a finished run). A failed session
	// lookup (deleted/expired session, transient error) must not block reading
	// artifacts whose presigned URLs are already in the report, so this is
	// best-effort: on error we fall back to the not-live zero value.
	live, _ := runinspect.ResolveRunLiveness(cmd.Context(), client, report)
	download, _ := cmd.Flags().GetBool("download")
	partial := report.NetworkRequestsPartial

	if report.NetworkRequestsURL == "" {
		// Network capture is iOS-only, so the shipper produces no live
		// object and finalize uploads none on Android.
		if strings.EqualFold(report.Platform, "android") || live.Platform == "android" {
			return fmt.Errorf("HTTP traffic capture is not available on Android (iOS only)")
		}
		if live.Live {
			return liveNotYetAvailable("network capture", taskID)
		}
		return fmt.Errorf("no network capture uploaded for this run")
	}
	if download {
		if isLiveURL(report.NetworkRequestsURL) {
			return errLiveDownload
		}
		outPath, _ := cmd.Flags().GetString("output")
		if outPath == "" {
			outPath = "network_requests.json.gz"
		}
		written, err := runinspect.DownloadArtifactToFile(
			cmd.Context(), report.NetworkRequestsURL, http.DefaultClient, outPath,
		)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]any{
			"task_id":       taskID,
			"artifact":      "network_requests",
			"downloaded_to": outPath,
			"bytes":         written,
		}, fmt.Sprintf("Downloaded network capture to %s (%d bytes)", outPath, written))
		return nil
	}
	// Bypass the on-disk cache for a still-growing live object.
	cacheDir := runinspect.DefaultCacheDir()
	if isLiveURL(report.NetworkRequestsURL) {
		cacheDir = ""
	}
	raw, err := runinspect.FetchArtifactBytes(
		cmd.Context(), report.NetworkRequestsURL, http.DefaultClient,
		cacheDir, taskID, "network_requests.json", true,
	)
	if err != nil {
		return err
	}
	// Auto-detect aggregated-JSON (finalized) vs per-request JSONL (live)
	// from the bytes — no dependence on the partial flag.
	cap, err := runinspect.ParseNetwork(raw)
	if err != nil {
		return err
	}

	filter := runinspect.NetworkFilter{}
	if hosts, _ := cmd.Flags().GetStringSlice("host"); len(hosts) > 0 {
		for _, h := range hosts {
			for _, part := range strings.Split(h, ",") {
				if p := strings.TrimSpace(part); p != "" {
					filter.HostGlobs = append(filter.HostGlobs, p)
				}
			}
		}
	}
	if statuses, _ := cmd.Flags().GetStringSlice("status"); len(statuses) > 0 {
		filter.Statuses = runinspect.ParseStatuses(statuses)
	}
	if failed, _ := cmd.Flags().GetBool("failed"); failed {
		filter.FailedOnly = true
	}
	if pattern, _ := cmd.Flags().GetString("grep"); pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("--grep: %w", err)
		}
		filter.Grep = re
	}
	if since, _ := cmd.Flags().GetFloat64("since"); since > 0 {
		s := since
		filter.SinceSeconds = &s
	}
	if until, _ := cmd.Flags().GetFloat64("until"); until > 0 {
		u := until
		filter.UntilSeconds = &u
	}
	filtered := filter.Apply(cap.Requests)

	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"task_id":  taskID,
			"requests": filtered,
			"summary":  cap.Summary,
			"matched":  len(filtered),
			"total":    len(cap.Requests),
			"partial":  partial,
		})
	}

	if partial {
		printPartialNote(cmd)
	}
	noFilters := len(filter.HostGlobs) == 0 && len(filter.Statuses) == 0 &&
		!filter.FailedOnly && filter.Grep == nil &&
		filter.SinceSeconds == nil && filter.UntilSeconds == nil
	withBody, _ := cmd.Flags().GetBool("body")
	if noFilters {
		return printNetworkHostRollup(cmd, cap)
	}
	return printNetworkRequests(cmd, filtered, len(cap.Requests), withBody)
}

func printNetworkHostRollup(cmd *cobra.Command, cap *runinspect.NetworkCapture) error {
	w := cmd.OutOrStdout()
	s := cap.Summary
	fmt.Fprintf(w, "Requests: %d total, %d failed, %d bytes received\n",
		s.TotalRequests, s.FailedRequests, s.TotalResponseBytes)
	fmt.Fprintln(w)
	rows := runinspect.RollupByHost(cap.Requests)
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no requests captured)")
		return nil
	}
	fmt.Fprintln(w, "HOSTS")
	for _, row := range rows {
		failed := ""
		if row.FailedCount > 0 {
			failed = fmt.Sprintf("  %d failed", row.FailedCount)
		}
		fmt.Fprintf(w, "  %4d  %-50s  %s%s\n",
			row.Count, row.Host, formatBytes(row.BytesIn), failed)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Filter:   --host glob | --status 4xx | --failed | --grep regex | --since N --until N")
	fmt.Fprintln(w, "Inspect:  add --body to include request/response previews")
	return nil
}

func printNetworkRequests(cmd *cobra.Command, reqs []runinspect.NetworkRequest, total int, withBody bool) error {
	w := cmd.OutOrStdout()
	if len(reqs) == 0 {
		fmt.Fprintf(w, "(no requests matched out of %d total)\n", total)
		return nil
	}
	for i, r := range reqs {
		host := truncate(r.URL, 90)
		statusLabel := fmt.Sprintf("%d", r.StatusCode)
		if r.Error != nil {
			statusLabel = "ERR"
		}
		fmt.Fprintf(w, "#%02d  t=%7.2fs  %-6s %-4s %s\n",
			i, r.StartTimeS, statusLabel, r.Method, host)
		if withBody {
			if r.RequestBodyPreview != nil && *r.RequestBodyPreview != "" {
				fmt.Fprintf(w, "       req:  %s\n", truncate(*r.RequestBodyPreview, 300))
			}
			if r.ResponseBodyPreview != nil && *r.ResponseBodyPreview != "" {
				fmt.Fprintf(w, "       resp: %s\n", truncate(*r.ResponseBodyPreview, 300))
			}
			if r.Error != nil {
				fmt.Fprintf(w, "       err:  %s\n", *r.Error)
			}
		}
	}
	if len(reqs) != total {
		fmt.Fprintf(w, "\n%d of %d requests matched.\n", len(reqs), total)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	if cut <= 0 {
		return "…"
	}
	// Walk back to a rune boundary so we don't split a multi-byte UTF-8 char.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func formatBytes(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(n)/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// --- run perf -------------------------------------------------------------

var runPerfCmd = &cobra.Command{
	Use:   "perf <task_id>",
	Short: "Show hardware-metrics summary (CPU, memory) for a run (live or finished)",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl run perf 7aa6ce3c-...
  revyl run perf 7aa6ce3c-... --metric cpu --json
  revyl run perf 7aa6ce3c-... --download --output ./perf.json.gz`,
	RunE: runPerfRun,
}

func runPerfRun(cmd *cobra.Command, args []string) error {
	taskID := strings.TrimSpace(args[0])
	report, client, err := loadReportOnly(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	// Liveness is advisory — it only frames messaging ("not yet available"
	// while running vs "not available" on a finished run). A failed session
	// lookup (deleted/expired session, transient error) must not block reading
	// artifacts whose presigned URLs are already in the report, so this is
	// best-effort: on error we fall back to the not-live zero value.
	live, _ := runinspect.ResolveRunLiveness(cmd.Context(), client, report)
	download, _ := cmd.Flags().GetBool("download")
	partial := report.HardwareMetricsPartial

	if report.HardwareMetricsURL == "" {
		if live.Live {
			return liveNotYetAvailable("hardware metrics", taskID)
		}
		return fmt.Errorf("no hardware metrics uploaded for this run")
	}
	if download {
		if isLiveURL(report.HardwareMetricsURL) {
			return errLiveDownload
		}
		outPath, _ := cmd.Flags().GetString("output")
		if outPath == "" {
			outPath = "hardware_metrics.json.gz"
		}
		written, err := runinspect.DownloadArtifactToFile(
			cmd.Context(), report.HardwareMetricsURL, http.DefaultClient, outPath,
		)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]any{
			"task_id":       taskID,
			"artifact":      "hardware_metrics",
			"downloaded_to": outPath,
			"bytes":         written,
		}, fmt.Sprintf("Downloaded hardware metrics to %s (%d bytes)", outPath, written))
		return nil
	}
	// Bypass the on-disk cache for a still-growing live object.
	cacheDir := runinspect.DefaultCacheDir()
	if isLiveURL(report.HardwareMetricsURL) {
		cacheDir = ""
	}
	raw, err := runinspect.FetchArtifactBytes(
		cmd.Context(), report.HardwareMetricsURL, http.DefaultClient,
		cacheDir, taskID, "hardware_metrics.json", true,
	)
	if err != nil {
		return err
	}
	// Auto-detect aggregated-JSON (finalized) vs per-sample JSONL (live)
	// from the bytes — no dependence on the partial flag.
	cap, err := runinspect.ParsePerf(raw)
	if err != nil {
		return err
	}
	cpu, mem, cpuOK, memOK := runinspect.ComputeMetricStats(cap)

	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		out := map[string]any{
			"task_id": taskID,
			"summary": cap.Summary,
			"partial": partial,
		}
		if cpuOK {
			out["cpu"] = cpu
		}
		if memOK {
			out["memory"] = mem
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if partial {
		printPartialNote(cmd)
	}
	metric, _ := cmd.Flags().GetString("metric")
	return printPerfStats(cmd, cap, cpu, mem, cpuOK, memOK, metric)
}

func printPerfStats(
	cmd *cobra.Command,
	cap *runinspect.PerfCapture,
	cpu, mem runinspect.MetricStats,
	cpuOK, memOK bool,
	metric string,
) error {
	w := cmd.OutOrStdout()
	s := cap.Summary
	if s.SampleCount != nil {
		fmt.Fprintf(w, "Samples:    %d", *s.SampleCount)
		if s.DurationS != nil {
			fmt.Fprintf(w, " over %.1fs", *s.DurationS)
		}
		fmt.Fprintln(w)
	}
	if s.AvgFPS != nil || s.MinFPS != nil {
		fmt.Fprint(w, "FPS:       ")
		if s.AvgFPS != nil {
			fmt.Fprintf(w, " avg=%.1f", *s.AvgFPS)
		}
		if s.MinFPS != nil {
			fmt.Fprintf(w, " min=%.1f", *s.MinFPS)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)

	want := strings.ToLower(strings.TrimSpace(metric))
	showCPU := want == "" || want == "cpu"
	showMem := want == "" || want == "mem" || want == "memory"

	if showCPU {
		if cpuOK {
			printMetricRow(w, cpu)
		} else {
			fmt.Fprintln(w, "CPU %:    (no samples)")
		}
	}
	if showMem {
		if memOK {
			printMetricRow(w, mem)
		} else {
			fmt.Fprintln(w, "Mem RSS:  (no samples)")
		}
	}

	if len(cap.AppEvents) > 0 && want == "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "APP EVENTS")
		for _, e := range cap.AppEvents {
			fmt.Fprintf(w, "  t=%7.2fs  %s  %s\n", e.WallTimeS, e.Type, truncate(e.Data, 80))
		}
	}
	return nil
}

func printMetricRow(w interface{ Write(p []byte) (int, error) }, m runinspect.MetricStats) {
	fmt.Fprintf(w,
		"%-9s min=%7.1f  p50=%7.1f  p95=%7.1f  max=%7.1f %s  (avg %.1f, %d samples, peak at t=%.1fs)\n",
		m.Label+":", m.Min, m.P50, m.P95, m.Max, m.Unit, m.Avg, m.Samples, m.PeakWallTimeS,
	)
}

// --- run trace ------------------------------------------------------------

var runTraceCmd = &cobra.Command{
	Use:   "trace <task_id>",
	Short: "Download the captured Perfetto trace and print how to open it",
	Args:  cobra.ExactArgs(1),
	Long: `Perfetto traces are binary blobs only useful when loaded into the
Perfetto UI, so this command always downloads — there is no pretty-print
option. Drag the saved file into https://ui.perfetto.dev to inspect.`,
	Example: `  revyl run trace 7aa6ce3c-...
  revyl run trace 7aa6ce3c-... --output ./capture.perfetto-trace.gz`,
	RunE: runTraceRun,
}

func runTraceRun(cmd *cobra.Command, args []string) error {
	taskID := strings.TrimSpace(args[0])
	report, _, err := loadReportOnly(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	if report.PerfettoTraceURL == "" {
		return fmt.Errorf("no perfetto trace uploaded for this run")
	}
	outPath, _ := cmd.Flags().GetString("output")
	if outPath == "" {
		shortID := taskID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		outPath = fmt.Sprintf("perfetto_trace_%s.perfetto-trace.gz", shortID)
	}
	written, err := runinspect.DownloadArtifactToFile(
		cmd.Context(), report.PerfettoTraceURL, http.DefaultClient, outPath,
	)
	if err != nil {
		return err
	}
	jsonOrPrint(cmd, map[string]any{
		"task_id":       taskID,
		"artifact":      "perfetto_trace",
		"downloaded_to": outPath,
		"bytes":         written,
		"open_with":     "https://ui.perfetto.dev",
	}, fmt.Sprintf("Saved to %s (%d bytes)\nOpen with: https://ui.perfetto.dev (drag the file in)", outPath, written))
	return nil
}

// --- shared plumbing ------------------------------------------------------

// loadRunArtifacts wraps runinspect.LoadArtifacts with the CLI's
// auth-key + dev-mode flag plumbing. Every `revyl run *` subcommand
// that needs the device-state JSONL (summary, state) calls this so
// the cache, timeout, and error mapping live in the runinspect package
// (single owner, shared with the MCP tools).
func loadRunArtifacts(
	ctx context.Context,
	cmd *cobra.Command,
	taskID string,
) (*runinspect.Report, []runinspect.DeviceStateLine, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, nil, err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	return runinspect.LoadArtifacts(ctx, client, taskID)
}

// loadReportOnly is the no-device-state cousin of loadRunArtifacts.
// Logs / network / perf / trace only need the presigned-URL metadata
// from the report — fetching device-state JSONL on top would be a
// wasted ~10 MiB download for every invocation.
func loadReportOnly(
	ctx context.Context,
	cmd *cobra.Command,
	taskID string,
) (*runinspect.Report, *api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, nil, err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	report, err := runinspect.FetchReport(ctx, client, taskID)
	if err != nil {
		if errors.Is(err, runinspect.ErrReportNotFound) {
			return nil, nil, fmt.Errorf(
				"no report found for task %s — check the task_id and whether the run has completed",
				taskID,
			)
		}
		return nil, nil, err
	}
	return report, client, nil
}

// --- pretty printers ------------------------------------------------------

func printSummary(cmd *cobra.Command, s runinspect.Summary) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Task:       %s\n", s.TaskID)
	if s.TestName != "" {
		fmt.Fprintf(w, "Test:       %s", s.TestName)
		if s.TestID != "" {
			fmt.Fprintf(w, " (%s)", s.TestID)
		}
		fmt.Fprintln(w)
	}
	if s.Platform != "" {
		fmt.Fprintf(w, "Platform:   %s\n", s.Platform)
	}
	if s.RunSuccess != nil {
		status := "FAILED"
		if *s.RunSuccess {
			status = "PASSED"
		}
		fmt.Fprintf(w, "Status:     %s\n", status)
	}
	if s.DurationSeconds != nil {
		fmt.Fprintf(w, "Duration:   %.1f s\n", *s.DurationSeconds)
	}
	fmt.Fprintf(w, "Steps:      %d total, %d failed", s.TotalSteps, s.FailedSteps)
	if s.FailedStepIndex != nil {
		fmt.Fprintf(w, " (first fail: step %d)", *s.FailedStepIndex)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "STEPS")
	for _, step := range s.Steps {
		marker := "  "
		switch step.Status {
		case "failed":
			marker = "✗ "
		case "warning":
			marker = "! "
		case "passed":
			marker = "✓ "
		}
		fmt.Fprintf(w, "  %s%2d. [%s] %s\n",
			marker, step.Index, step.StepType, step.Description)
		if step.StatusReason != "" {
			fmt.Fprintf(w, "       reason: %s\n", step.StatusReason)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "ARTIFACTS")
	a := s.Artifacts
	fmt.Fprintf(w, "  device_state:     %s\n", availableTag(a.DeviceStateAvailable))
	fmt.Fprintf(w, "  network:          %s\n", availableTag(a.NetworkRequestsAvailable))
	fmt.Fprintf(w, "  hardware_metrics: %s\n", availableTag(a.HardwareMetricsAvailable))
	fmt.Fprintf(w, "  perfetto_trace:   %s\n", availableTag(a.PerfettoTraceAvailable))

	if len(s.IdentityHighlights) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "IDENTITY")
		for _, f := range s.IdentityHighlights {
			fmt.Fprintf(w, "  %-22s %v\n", f.Label+":", formatIdentityValue(f.Value))
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "MORE")
	if a.DeviceStateAvailable {
		fmt.Fprintf(w, "  state:    revyl run state    %s\n", s.TaskID)
	}
	if a.NetworkRequestsAvailable {
		fmt.Fprintf(w, "  network:  revyl run network  %s\n", s.TaskID)
	}
	if a.HardwareMetricsAvailable {
		fmt.Fprintf(w, "  perf:     revyl run perf     %s\n", s.TaskID)
	}
	if a.PerfettoTraceAvailable {
		fmt.Fprintf(w, "  trace:    revyl run trace    %s\n", s.TaskID)
	}
	// Device logs live behind a dedicated endpoint; the logs command
	// handles the "not uploaded" case itself, so suggest it unconditionally.
	fmt.Fprintf(w, "  logs:     revyl run logs     %s\n", s.TaskID)
	return nil
}

func printStateListing(cmd *cobra.Command, paths []runinspect.CapturedPath) error {
	w := cmd.OutOrStdout()
	if len(paths) == 0 {
		fmt.Fprintln(w, "No captured paths in this run.")
		return nil
	}
	fmt.Fprintln(w, "PLIST")
	plists := 0
	for _, p := range paths {
		if p.Kind != "plist" {
			continue
		}
		plists++
		fmt.Fprintf(w, "  %s  (%d keys, steps %d..%d%s)\n",
			p.Path, p.KeyCount, p.FirstSeenStep, p.LastSeenStep, rotatedSuffix(p.Rotated))
	}
	if plists == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "SQLITE")
	sql := 0
	for _, p := range paths {
		if p.Kind != "sqlite" {
			continue
		}
		sql++
		fmt.Fprintf(w, "  %s  (%d tables, steps %d..%d%s)\n",
			p.Path, p.TableCount, p.FirstSeenStep, p.LastSeenStep, rotatedSuffix(p.Rotated))
	}
	if sql == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Inspect one: `revyl run state <task_id> --path <p>`")
	return nil
}

func printStateSnapshot(cmd *cobra.Command, path string, snap *runinspect.PathSnapshot) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Path:       %s\n", path)
	fmt.Fprintf(w, "Kind:       %s\n", snap.Kind)
	fmt.Fprintf(w, "Size:       %d bytes\n", snap.Size)
	fmt.Fprintf(w, "Step range: %d..%d%s\n", snap.FirstSeenStep, snap.LastSeenStep, rotatedSuffix(snap.Rotated))
	fmt.Fprintln(w)
	if snap.Kind == "plist" {
		if len(snap.Values) == 0 {
			fmt.Fprintln(w, "  (no values captured)")
			return nil
		}
		keys := make([]string, 0, len(snap.Values))
		for k := range snap.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s = %s\n", k, formatPlistValueOneline(snap.Values[k]))
		}
		return nil
	}
	if snap.Kind == "sqlite" {
		if len(snap.Tables) == 0 {
			fmt.Fprintln(w, "  (no tables — file may be locked, encrypted, or non-SQLite)")
			return nil
		}
		names := make([]string, 0, len(snap.Tables))
		for n := range snap.Tables {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			t := snap.Tables[name]
			fmt.Fprintf(w, "  %s  (%d rows, %d cols)\n", name, t.RowCount, t.ColumnCount)
			if t.Schema != "" {
				fmt.Fprintf(w, "    schema: %s\n", t.Schema)
			}
		}
	}
	return nil
}

// --- helpers --------------------------------------------------------------

func availableTag(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func rotatedSuffix(b bool) string {
	if b {
		return " (rotated)"
	}
	return ""
}

func formatIdentityValue(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%g", t)
	case nil:
		return "null"
	default:
		raw, _ := json.Marshal(v)
		return string(raw)
	}
}

func formatPlistValueOneline(v interface{}) string {
	switch t := v.(type) {
	case string:
		if len(t) > 200 {
			return t[:197] + "…"
		}
		return t
	case map[string]interface{}:
		if b, ok := t["__bytes__"].(bool); ok && b {
			n, _ := t["len"].(float64)
			return fmt.Sprintf("<bytes len=%d>", int(n))
		}
		raw, _ := json.Marshal(v)
		s := string(raw)
		if len(s) > 200 {
			return s[:197] + "…"
		}
		return s
	default:
		raw, _ := json.Marshal(v)
		s := string(raw)
		if len(s) > 200 {
			return s[:197] + "…"
		}
		return s
	}
}

func init() {
	runCmd.PersistentFlags().Bool("json", false, "Emit raw JSON instead of pretty-printed output")
	runCmd.PersistentFlags().Bool("dev", false, "Use dev/staging backend (default: production)")

	runStateCmd.Flags().String("path", "",
		"Inspect this captured path (plist or sqlite). Omit to list all paths.")
	runStateCmd.Flags().Int("at-step", 0,
		"Show state as of this 1-indexed step (default: latest)")
	runStateCmd.Flags().Bool("download", false,
		"Download the raw .jsonl.gz artifact instead of inspecting it")
	runStateCmd.Flags().String("output", "",
		"Output path for --download (default: ./device_state.jsonl.gz)")

	runLogsCmd.Flags().String("grep", "",
		"Regexp filter applied to each log line (Go re2 syntax)")
	runLogsCmd.Flags().StringSlice("level", nil,
		"Comma-separated levels to keep, e.g. warn,error (V/D/I/W/E/F and synonyms)")
	runLogsCmd.Flags().Int("tail", 0,
		"Show only the last N lines (after filtering)")
	runLogsCmd.Flags().Bool("download", false,
		"Download the raw .txt.gz artifact instead of printing")
	runLogsCmd.Flags().String("output", "",
		"Output path for --download (default: ./<server-suggested-filename>)")

	runNetworkCmd.Flags().StringSlice("host", nil,
		"Filter to requests whose URL host matches this glob/substring (comma-separated; repeatable)")
	runNetworkCmd.Flags().StringSlice("status", nil,
		"Filter to specific status codes or buckets, e.g. 200,404 or 4xx,5xx")
	runNetworkCmd.Flags().Bool("failed", false,
		"Only show requests with a transport error or HTTP status >= 400")
	runNetworkCmd.Flags().String("grep", "",
		"Regexp filter applied to URL + body previews (Go re2 syntax)")
	runNetworkCmd.Flags().Float64("since", 0,
		"Only show requests starting after N seconds from run start")
	runNetworkCmd.Flags().Float64("until", 0,
		"Only show requests starting before N seconds from run start")
	runNetworkCmd.Flags().Bool("body", false,
		"Include request/response body previews under each matched entry")
	runNetworkCmd.Flags().Bool("download", false,
		"Download the raw .json.gz artifact instead of printing")
	runNetworkCmd.Flags().String("output", "",
		"Output path for --download (default: ./network_requests.json.gz)")

	runPerfCmd.Flags().String("metric", "",
		"Narrow to one series: cpu | memory (default: show both)")
	runPerfCmd.Flags().Bool("download", false,
		"Download the raw .json.gz artifact instead of printing")
	runPerfCmd.Flags().String("output", "",
		"Output path for --download (default: ./hardware_metrics.json.gz)")

	runTraceCmd.Flags().String("output", "",
		"Output path for the saved trace (default: ./perfetto_trace_<short>.perfetto-trace.gz)")

	runCmd.AddCommand(runSummaryCmd)
	runCmd.AddCommand(runStateCmd)
	runCmd.AddCommand(runLogsCmd)
	runCmd.AddCommand(runNetworkCmd)
	runCmd.AddCommand(runPerfCmd)
	runCmd.AddCommand(runTraceCmd)
}
