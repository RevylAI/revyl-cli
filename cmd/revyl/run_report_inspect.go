package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/runinspect"
	"github.com/spf13/cobra"
)

func newRunInspectCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "inspect <reference_id>", Short: "Progressively inspect a report: overview, steps, actions, or field chunks", Args: cobra.ExactArgs(1), RunE: runReportInspect}
	cmd.Flags().String("section", "overview", "Report section: overview, steps, actions, or field")
	cmd.Flags().String("step-id", "", "Select an exact step UUID within this report")
	cmd.Flags().String("action-id", "", "Select an exact action UUID within step-id")
	cmd.Flags().String("field-version", "", "Version from the first field chunk; required with a nonzero field offset")
	cmd.Flags().String("field", "", "RFC 6901 JSON pointer relative to the selected report, step, or action (section=field)")
	cmd.Flags().String("grep", "", "Regexp across all fields in the selected section, before compaction and pagination")
	cmd.Flags().String("status", "", "Effective step status: passed, failed, warning, running, or pending; also scopes actions by their step")
	cmd.Flags().Int("offset", 0, "Matching item offset; section=field uses Unicode character offsets in encoded JSON")
	cmd.Flags().Int("limit", 20, "Maximum items per page (1..100); field chunks are at most 4000 characters")
	return cmd
}

func runReportInspect(cmd *cobra.Command, args []string) (resultErr error) {
	defer func() {
		var failure *investigationError
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if jsonOutput && errors.As(resultErr, &failure) {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				SchemaVersion int                 `json:"schema_version"`
				Error         *investigationError `json:"error"`
			}{1, failure}); err != nil {
				resultErr = err
			}
		}
	}()
	invalid := func(err error) error {
		return &investigationError{Code: "invalid_arguments", Tool: "reports.inspect", Message: err.Error(), Recovery: []toolRecovery{}}
	}

	o := runinspect.InvestigationOptions{}
	o.Section, _ = cmd.Flags().GetString("section")
	o.StepID, _ = cmd.Flags().GetString("step-id")
	o.ActionID, _ = cmd.Flags().GetString("action-id")
	o.Field, _ = cmd.Flags().GetString("field")
	o.FieldVersion, _ = cmd.Flags().GetString("field-version")
	o.Grep, _ = cmd.Flags().GetString("grep")
	o.Status, _ = cmd.Flags().GetString("status")
	o.Offset, _ = cmd.Flags().GetInt("offset")
	o.Limit, _ = cmd.Flags().GetInt("limit")
	if err := o.Validate(); err != nil {
		return invalid(err)
	}
	for _, id := range []string{args[0], o.StepID, o.ActionID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return invalid(fmt.Errorf("report references, step-id and action-id must be UUIDs"))
			}
		}
	}
	kind, _ := cmd.Flags().GetString("id-kind")
	if kind != "session" && kind != "execution" {
		return invalid(fmt.Errorf("id-kind must be session or execution"))
	}
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	fieldReadsSteps := o.Field == "/steps" || strings.HasPrefix(o.Field, "/steps/")
	fieldReadsActions := o.Field == "/actions" || strings.HasPrefix(o.Field, "/actions/")
	includeSteps := o.Section == "steps" || o.Section == "actions" || (o.Section == "field" && (o.StepID != "" || fieldReadsSteps))
	includeActions := o.Section == "actions" || (o.Section == "field" && (o.ActionID != "" || (o.StepID != "" && fieldReadsActions) || (o.StepID == "" && fieldReadsSteps)))
	var envelope *api.CLIReportContextEnvelope
	if kind == "session" {
		envelope, err = client.GetReportBySession(cmd.Context(), args[0], includeSteps, includeActions, false)
	} else {
		envelope, err = client.GetReportContextByExecution(cmd.Context(), args[0], includeSteps, includeActions, false)
	}
	if err != nil {
		return err
	}
	page, err := runinspect.InspectReport(envelope.Raw, o)
	if err != nil {
		if errors.Is(err, runinspect.ErrReportFieldChanged) {
			return &investigationError{Code: "evidence_changed", Tool: "reports.inspect", Message: err.Error(), Recovery: []toolRecovery{}}
		}
		return invalid(err)
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	if jsonOutput, _ := cmd.Flags().GetBool("json"); !jsonOutput {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(page)
}

func init() { runCmd.AddCommand(newRunInspectCommand()) }
