// Package main: revyl CLI subcommands for the device-state inspector.
//
// `revyl device state {list,snapshot,diff,userdefaults,sqlite}` mirrors
// the MCP tools in internal/mcp/device_tools.go. Both surfaces share
// the same input/output structs (DeviceStateListOutput etc.) so adding
// a field threads through once and lands in both.

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	mcppkg "github.com/revyl/cli/internal/mcp"
)

var deviceStateCmd = &cobra.Command{
	Use:   "state",
	Short: "Inspect on-device UserDefaults + SQLite state",
	Long: `Inspect or query the running app's UserDefaults plists and SQLite
databases. Requires an active dev-loop session (see 'revyl device start').`,
}

var deviceStateListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all UserDefaults plists and SQLite DBs in the app's container, with schemas",
	Example: `  revyl device state list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		body, err := mgr.WorkerRequestForSession(
			cmd.Context(), session.Index, "/device_state/list", nil,
		)
		if err != nil {
			return err
		}
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			_, err := cmd.OutOrStdout().Write(body)
			return err
		}
		var out mcppkg.DeviceStateListOutput
		if err := json.Unmarshal(body, &out); err != nil {
			return err
		}
		return printDeviceStateList(cmd, out)
	},
}

var deviceStateSnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Capture a baseline snapshot and print the snapshot_id",
	Example: `  revyl device state snapshot
  revyl device state snapshot | xargs -I{} revyl device state diff --since {}`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		body, err := mgr.WorkerRequestForSession(
			cmd.Context(), session.Index, "/device_state/snapshot", map[string]any{},
		)
		if err != nil {
			return err
		}
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			_, err := cmd.OutOrStdout().Write(body)
			return err
		}
		var out mcppkg.DeviceStateSnapshotOutput
		if err := json.Unmarshal(body, &out); err != nil {
			return err
		}
		if !out.Success {
			return fmt.Errorf("snapshot failed: %s", out.Error)
		}
		// Bare snapshot_id on its own line — friendly for `xargs`.
		fmt.Fprintln(cmd.OutOrStdout(), out.SnapshotID)
		return nil
	},
}

var deviceStateDiffCmd = &cobra.Command{
	Use:   "diff --since <snapshot_id>",
	Short: "Show what's changed since a snapshot",
	Example: `  revyl device state diff --since 4823
  revyl device state diff --since 4823 --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		since, _ := cmd.Flags().GetString("since")
		if strings.TrimSpace(since) == "" {
			return fmt.Errorf("--since <snapshot_id> is required")
		}
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		body, err := mgr.WorkerRequestForSession(
			cmd.Context(), session.Index, "/device_state/diff",
			map[string]any{"snapshot_id": since},
		)
		if err != nil {
			return err
		}
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			_, err := cmd.OutOrStdout().Write(body)
			return err
		}
		var out mcppkg.DeviceStateDiffOutput
		if err := json.Unmarshal(body, &out); err != nil {
			return err
		}
		if out.Error != "" {
			return fmt.Errorf("diff failed: %s", out.Error)
		}
		return printDeviceStateDiff(cmd, out)
	},
}

var deviceStateUserDefaultsCmd = &cobra.Command{
	Use:   "userdefaults <plist-path> [--key <k>]",
	Short: "Read one UserDefaults plist or a single key",
	Args:  cobra.ExactArgs(1),
	Example: `  revyl device state userdefaults Library/Preferences/com.example.plist
  revyl device state userdefaults Library/Preferences/com.example.plist --key userId`,
	RunE: func(cmd *cobra.Command, args []string) error {
		plistPath := args[0]
		key, _ := cmd.Flags().GetString("key")
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		reqBody := map[string]any{"plist_path": plistPath}
		if key != "" {
			reqBody["key"] = key
		}
		body, err := mgr.WorkerRequestForSession(
			cmd.Context(), session.Index, "/device_state/userdefaults", reqBody,
		)
		if err != nil {
			return err
		}
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			_, err := cmd.OutOrStdout().Write(body)
			return err
		}
		var out mcppkg.DeviceStateQueryOutput
		if err := json.Unmarshal(body, &out); err != nil {
			return err
		}
		if out.Error != "" {
			return fmt.Errorf("userdefaults read failed: %s", out.Error)
		}
		pretty, _ := json.MarshalIndent(out.Value, "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(pretty))
		return nil
	},
}

var deviceStateSqliteCmd = &cobra.Command{
	Use:   "sqlite <db-path> -- <SQL> [--param value ...]",
	Short: "Run a read-only SELECT/WITH against an app sqlite database",
	Args:  cobra.MinimumNArgs(2),
	Example: `  revyl device state sqlite "Library/Application Support/db.sqlite" -- \
    "SELECT count(*) FROM users"
  revyl device state sqlite Documents/app.db -- \
    "SELECT email FROM users WHERE id = ?" --param 42`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dbPath := args[0]
		// Everything after the db path is treated as the SQL (Cobra
		// auto-splits at "--", so flags after that are NOT parsed).
		sql := strings.Join(args[1:], " ")
		paramsRaw, _ := cmd.Flags().GetStringArray("param")
		// Worker accepts JSON-typed params; we send strings and let
		// SQLite coerce. For numeric/bool args, callers can quote in
		// SQL — keeping the CLI surface simple beats faux typing.
		params := make([]interface{}, 0, len(paramsRaw))
		for _, p := range paramsRaw {
			params = append(params, p)
		}
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		reqBody := map[string]any{
			"db_path": dbPath,
			"sql":     sql,
			"params":  params,
		}
		body, err := mgr.WorkerRequestForSession(
			cmd.Context(), session.Index, "/device_state/sqlite/query", reqBody,
		)
		if err != nil {
			return err
		}
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			_, err := cmd.OutOrStdout().Write(body)
			return err
		}
		var out mcppkg.DeviceStateQueryOutput
		if err := json.Unmarshal(body, &out); err != nil {
			return err
		}
		if out.Error != "" {
			return fmt.Errorf("sqlite query failed: %s", out.Error)
		}
		return printDeviceStateSqlite(cmd, out)
	},
}

// --- pretty printers ------------------------------------------------------
//
// Plain ASCII / minimal markup so output stays scannable when piped to
// `less` or `grep`. JSON mode (`--json`) is the structured option for
// programmatic consumption.

func printDeviceStateList(cmd *cobra.Command, out mcppkg.DeviceStateListOutput) error {
	w := cmd.OutOrStdout()
	fmt.Fprintln(w, "USERDEFAULTS")
	if len(out.UserDefaults) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, e := range out.UserDefaults {
		path, _ := e["path"].(string)
		size, _ := e["size"].(float64)
		keysAny, _ := e["keys"].([]interface{})
		keys := make([]string, 0, len(keysAny))
		for _, k := range keysAny {
			if s, ok := k.(string); ok {
				keys = append(keys, s)
			}
		}
		fmt.Fprintf(w, "  %s  (%d bytes)\n", path, int(size))
		if len(keys) > 0 {
			fmt.Fprintf(w, "    keys: %s\n", strings.Join(keys, ", "))
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "SQLITE")
	if len(out.SQLite) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, e := range out.SQLite {
		path, _ := e["path"].(string)
		size, _ := e["size"].(float64)
		fmt.Fprintf(w, "  %s  (%d bytes)\n", path, int(size))
		tablesAny, _ := e["tables"].([]interface{})
		for _, t := range tablesAny {
			tm, _ := t.(map[string]interface{})
			name, _ := tm["name"].(string)
			rc, _ := tm["row_count"].(float64)
			fmt.Fprintf(w, "    %s  (%d rows)\n", name, int(rc))
		}
	}
	if len(out.Errors) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "ERRORS")
		for _, e := range out.Errors {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	return nil
}

func printDeviceStateDiff(cmd *cobra.Command, out mcppkg.DeviceStateDiffOutput) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "DIFF %s..%s\n", out.FromSnapshotID, out.ToCursor)
	if len(out.UserDefaults) == 0 && len(out.SQLite) == 0 {
		fmt.Fprintln(w, "  (no changes)")
		return nil
	}
	for path, entry := range out.UserDefaults {
		fmt.Fprintf(w, "\n%s\n", path)
		if added, ok := entry["added"].(map[string]interface{}); ok {
			for k, v := range added {
				vb, _ := json.Marshal(v)
				fmt.Fprintf(w, "  + %s = %s\n", k, string(vb))
			}
		}
		if removed, ok := entry["removed"].([]interface{}); ok {
			for _, k := range removed {
				fmt.Fprintf(w, "  - %v\n", k)
			}
		}
		if changed, ok := entry["changed"].([]interface{}); ok {
			for _, c := range changed {
				cm, _ := c.(map[string]interface{})
				k, _ := cm["key"].(string)
				fromB, _ := json.Marshal(cm["from"])
				toB, _ := json.Marshal(cm["to"])
				fmt.Fprintf(w, "  ~ %s: %s -> %s\n", k, string(fromB), string(toB))
			}
		}
	}
	for path, entry := range out.SQLite {
		fmt.Fprintf(w, "\n%s\n", path)
		if tables, ok := entry["tables"].(map[string]interface{}); ok {
			for name, t := range tables {
				tm, _ := t.(map[string]interface{})
				if delta, ok := tm["row_count_delta"].(float64); ok {
					sign := "+"
					if delta < 0 {
						sign = ""
					}
					fmt.Fprintf(w, "  %s: rows %s%d\n", name, sign, int(delta))
				}
			}
		}
		if changes, ok := entry["schema_changes"].([]interface{}); ok && len(changes) > 0 {
			fmt.Fprintf(w, "  schema changes: ")
			parts := make([]string, 0, len(changes))
			for _, c := range changes {
				if s, ok := c.(string); ok {
					parts = append(parts, s)
				}
			}
			fmt.Fprintln(w, strings.Join(parts, ", "))
		}
	}
	return nil
}

func printDeviceStateSqlite(cmd *cobra.Command, out mcppkg.DeviceStateQueryOutput) error {
	w := cmd.OutOrStdout()
	if len(out.Cols) == 0 {
		fmt.Fprintln(w, "(no columns)")
		return nil
	}
	fmt.Fprintln(w, strings.Join(out.Cols, " | "))
	fmt.Fprintln(w, strings.Repeat("-", 2+3*len(out.Cols)))
	for _, row := range out.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			b, _ := json.Marshal(v)
			cells[i] = string(b)
		}
		fmt.Fprintln(w, strings.Join(cells, " | "))
	}
	if out.Truncated {
		fmt.Fprintln(w, "(truncated — add LIMIT/OFFSET to paginate)")
	}
	return nil
}

func init() {
	// Persistent flags shared by every `state` subcommand.
	deviceStateCmd.PersistentFlags().Bool("json", false,
		"Emit raw JSON instead of pretty-printed output")
	deviceStateCmd.PersistentFlags().IntP("s", "s", -1,
		"Session index to target (-1 for active)")

	deviceStateDiffCmd.Flags().String("since", "",
		"Snapshot id returned by a prior `device state snapshot` (required)")
	_ = deviceStateDiffCmd.MarkFlagRequired("since")

	deviceStateUserDefaultsCmd.Flags().String("key", "",
		"Read only this top-level key (omit to read the full plist)")

	deviceStateSqliteCmd.Flags().StringArray("param", nil,
		"Positional SQL parameter (repeat for each `?`)")

	deviceStateCmd.AddCommand(deviceStateListCmd)
	deviceStateCmd.AddCommand(deviceStateSnapshotCmd)
	deviceStateCmd.AddCommand(deviceStateDiffCmd)
	deviceStateCmd.AddCommand(deviceStateUserDefaultsCmd)
	deviceStateCmd.AddCommand(deviceStateSqliteCmd)
	deviceCmd.AddCommand(deviceStateCmd)
}
