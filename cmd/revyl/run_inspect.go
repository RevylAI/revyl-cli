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
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/runinspect"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Inspect finished test runs (state, identity, summary)",
	Long: `Inspect a completed test run by its task_id.

Unlike 'revyl device state' (which targets a live dev-loop session),
'revyl run' operates against the recorded artifacts that were uploaded
to S3 when the run finished — no active worker required.`,
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
	taskID := args[0]
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

// --- run identity ---------------------------------------------------------

var runIdentityCmd = &cobra.Command{
	Use:   "identity <task_id>",
	Short: "Show who was logged in, what org, role flags, and vendor analytics IDs",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl run identity 7aa6ce3c-...
  revyl run identity 7aa6ce3c-... --at-step 3
  revyl run identity 7aa6ce3c-... --json`,
	RunE: runIdentityRun,
}

func runIdentityRun(cmd *cobra.Command, args []string) error {
	taskID := args[0]
	report, lines, err := loadRunArtifacts(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	atStep, _ := cmd.Flags().GetInt("at-step")
	fields := runinspect.DetectIdentity(lines, runinspect.IndexerFromReport(report), runinspect.IdentityOptions{AtStep: atStep})
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"task_id":  taskID,
			"identity": fields,
		})
	}
	return printIdentity(cmd, fields)
}

// --- run traces -----------------------------------------------------------

var runTracesCmd = &cobra.Command{
	Use:   "traces <task_id> [--at-step N] [--vendor sentry]",
	Short: "List backend-pivotable trace IDs captured during the run (Sentry events, transactions, replays)",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl run traces 7aa6ce3c-...
  revyl run traces 7aa6ce3c-... --at-step 3
  revyl run traces 7aa6ce3c-... --vendor sentry --json`,
	RunE: runTracesRun,
}

func runTracesRun(cmd *cobra.Command, args []string) error {
	taskID := args[0]
	report, lines, err := loadRunArtifacts(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
	}
	atStep, _ := cmd.Flags().GetInt("at-step")
	vendorStrs, _ := cmd.Flags().GetStringSlice("vendor")
	opts := runinspect.TracesOptions{AtStep: atStep}
	for _, v := range vendorStrs {
		opts.Vendors = append(opts.Vendors, runinspect.TraceVendor(strings.TrimSpace(v)))
	}
	traces := runinspect.DetectTraces(lines, runinspect.IndexerFromReport(report), opts)
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"task_id": taskID,
			"traces":  traces,
		})
	}
	return printTraces(cmd, traces)
}

// --- run state ------------------------------------------------------------

var runStateCmd = &cobra.Command{
	Use:   "state <task_id> [--path P] [--at-step N]",
	Short: "Inspect the captured UserDefaults / SQLite for a run",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl run state 7aa6ce3c-...                                  # list captured paths
  revyl run state 7aa6ce3c-... --path Library/Preferences/com.x.plist
  revyl run state 7aa6ce3c-... --path Documents/cache.sqlite3 --at-step 5`,
	RunE: runStateRun,
}

func runStateRun(cmd *cobra.Command, args []string) error {
	taskID := args[0]
	report, lines, err := loadRunArtifacts(cmd.Context(), cmd, taskID)
	if err != nil {
		return err
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
			})
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
		})
	}
	return printStateSnapshot(cmd, path, snapshot)
}

// --- shared plumbing ------------------------------------------------------

// loadRunArtifacts wraps runinspect.LoadArtifacts with the CLI's
// auth-key + dev-mode flag plumbing. Every `revyl run *` subcommand
// calls this so the cache, timeout, and error mapping live in the
// runinspect package (single owner, shared with the MCP tools).
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
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Full identity: `revyl run identity <task_id>`")
	}
	return nil
}

func printIdentity(cmd *cobra.Command, fields []runinspect.IdentityField) error {
	w := cmd.OutOrStdout()
	if len(fields) == 0 {
		fmt.Fprintln(w, "No identity fields detected in the captured state.")
		return nil
	}
	byKind := groupIdentityByKind(fields)
	order := []runinspect.IdentityKind{
		runinspect.IdentityKindUser,
		runinspect.IdentityKindEmail,
		runinspect.IdentityKindUsername,
		runinspect.IdentityKindRole,
		runinspect.IdentityKindAccount,
		runinspect.IdentityKindOrg,
		runinspect.IdentityKindVendor,
	}
	headers := map[runinspect.IdentityKind]string{
		runinspect.IdentityKindUser:     "USER",
		runinspect.IdentityKindEmail:    "EMAIL",
		runinspect.IdentityKindUsername: "USERNAME",
		runinspect.IdentityKindRole:     "ROLE FLAGS",
		runinspect.IdentityKindAccount:  "ACCOUNT",
		runinspect.IdentityKindOrg:      "ORG / WORKSPACE",
		runinspect.IdentityKindVendor:   "VENDOR IDS (for vendor-dashboard pivot)",
	}
	for _, kind := range order {
		fields := byKind[kind]
		if len(fields) == 0 {
			continue
		}
		fmt.Fprintln(w, headers[kind])
		for _, f := range fields {
			fmt.Fprintf(w, "  %-32s %v\n", f.Label+":", formatIdentityValue(f.Value))
			if f.Source.Path != "" {
				fmt.Fprintf(w, "    source: %s:%s\n", f.Source.Path, f.Source.KeyPath)
			}
		}
		fmt.Fprintln(w)
	}
	return nil
}

func printTraces(cmd *cobra.Command, traces []runinspect.TraceRecord) error {
	w := cmd.OutOrStdout()
	if len(traces) == 0 {
		fmt.Fprintln(w, "No backend-pivotable trace IDs found in the captured state.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "If the app uses Sentry and you expected traces here, check:")
		fmt.Fprintln(w, "  - Did the SDK actually capture events during the run? Check the report's failure mode.")
		fmt.Fprintln(w, "  - Was the test bot blocked before any networked action ran?")
		fmt.Fprintln(w, "  - Sentry envelopes auto-delete after upload; very short runs may upload before our sampler captured.")
		return nil
	}
	for _, r := range traces {
		fmt.Fprintf(w, "%s\n", r.Value)
		fmt.Fprintf(w, "  vendor:      %s\n", r.Vendor)
		fmt.Fprintf(w, "  kind:        %s (%s)\n", r.Kind, r.Label)
		if r.Transaction != "" {
			fmt.Fprintf(w, "  transaction: %s\n", r.Transaction)
		}
		if len(r.Related) > 0 {
			// Stable ordering for human-friendly diffs across runs.
			keys := make([]string, 0, len(r.Related))
			for k := range r.Related {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if r.Related[k] == r.Value {
					continue
				}
				fmt.Fprintf(w, "  %s:   %s\n", k, r.Related[k])
			}
		}
		fmt.Fprintf(w, "  steps:       %d..%d\n", r.FirstSeenStep, r.LastSeenStep)
		if r.FirstTimestamp != "" {
			fmt.Fprintf(w, "  timestamp:   %s\n", r.FirstTimestamp)
		}
		if r.Source.Path != "" {
			fmt.Fprintf(w, "  source:      %s\n", r.Source.Path)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Paste any of these into your Sentry org's search (or your tracing backend) to correlate this run.")
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

func groupIdentityByKind(fields []runinspect.IdentityField) map[runinspect.IdentityKind][]runinspect.IdentityField {
	out := make(map[runinspect.IdentityKind][]runinspect.IdentityField)
	for _, f := range fields {
		out[f.Kind] = append(out[f.Kind], f)
	}
	return out
}

func init() {
	runCmd.PersistentFlags().Bool("json", false, "Emit raw JSON instead of pretty-printed output")
	runCmd.PersistentFlags().Bool("dev", false, "Use dev/staging backend (default: production)")

	runIdentityCmd.Flags().Int("at-step", 0,
		"Only show identity visible as of this 1-indexed step (default: all)")

	runStateCmd.Flags().String("path", "",
		"Inspect this captured path (plist or sqlite). Omit to list all paths.")
	runStateCmd.Flags().Int("at-step", 0,
		"Show state as of this 1-indexed step (default: latest)")

	runTracesCmd.Flags().Int("at-step", 0,
		"Only show traces visible as of this 1-indexed step (default: all)")
	runTracesCmd.Flags().StringSlice("vendor", nil,
		"Restrict to specific vendors (currently only 'sentry' is supported)")

	runCmd.AddCommand(runSummaryCmd)
	runCmd.AddCommand(runIdentityCmd)
	runCmd.AddCommand(runStateCmd)
	runCmd.AddCommand(runTracesCmd)
}
