package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/revyl/cli/internal/api"
)

// Human output in this file writes with fmt rather than the ui package, whose
// output feeds failure analytics: names, descriptions, filters and event
// payloads are customer content, and an incoming webhook URL is a credential.

func formatAutomationTime(value time.Time) string {
	return value.Local().Format("2006-01-02 15:04:05 MST")
}

func formatOptionalAutomationTime(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return formatAutomationTime(*value)
}

func automationOnOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func printAutomationTable(w io.Writer, headers []string, rows [][]string) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(table, strings.Join(row, "\t"))
	}
	_ = table.Flush()
}

func describeAutomationAction(actionType string, config api.RunTestActionConfig) string {
	if actionType == automationActionTypeRunTest {
		return fmt.Sprintf("run_test: starts test %s", config.TestId)
	}
	return actionType
}

func printAutomationSummary(w io.Writer, automation *api.AutomationResponse) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(table, "Automation\t%s\n", automation.Name)
	fmt.Fprintf(table, "ID\t%s\n", automation.Id)
	fmt.Fprintf(table, "Status\t%s\n", automationOnOff(automation.IsEnabled))
	fmt.Fprintf(table, "Action\t%s\n", describeAutomationAction(automation.ActionType, automation.ActionConfig))
	if description := stringValue(automation.Description); description != "" {
		fmt.Fprintf(table, "Description\t%s\n", description)
	}
	if automation.LastRun != nil {
		run := automation.LastRun
		fmt.Fprintf(table, "Last run\t%s, %s, started by %s trigger at %s\n", run.Id, run.RunStatus, run.TriggerType, formatAutomationTime(run.CreatedAt))
	} else {
		fmt.Fprintf(table, "Last run\tnone yet\n")
	}
	_ = table.Flush()

	var triggers []api.AutomationTriggerResponse
	if automation.Triggers != nil {
		triggers = *automation.Triggers
	}
	fmt.Fprintf(w, "\nTriggers (%d)\n", len(triggers))
	for _, trigger := range triggers {
		fmt.Fprintf(w, "  %s  %s\n", trigger.TriggerType, trigger.Id)
		for _, line := range describeAutomationTrigger(trigger) {
			fmt.Fprintf(w, "      %s\n", line)
		}
	}
}

func printAutomationList(w io.Writer, automations []api.AutomationResponse) {
	rows := make([][]string, 0, len(automations))
	for _, automation := range automations {
		lastRun := "-"
		if automation.LastRun != nil {
			lastRun = fmt.Sprintf("%s %s", automation.LastRun.RunStatus, formatAutomationTime(automation.LastRun.CreatedAt))
		}
		rows = append(rows, []string{automation.Name, automationOnOff(automation.IsEnabled), automationTriggerTypesSummary(automation.Triggers), lastRun, automation.Id.String()})
	}
	printAutomationTable(w, []string{"NAME", "STATUS", "TRIGGERS", "LAST RUN", "ID"}, rows)
}

func automationTriggerTypesSummary(triggers *[]api.AutomationTriggerResponse) string {
	if triggers == nil || len(*triggers) == 0 {
		return "-"
	}
	counts := map[string]int{}
	var order []string
	for _, trigger := range *triggers {
		triggerType := string(trigger.TriggerType)
		if counts[triggerType] == 0 {
			order = append(order, triggerType)
		}
		counts[triggerType]++
	}
	parts := make([]string, 0, len(order))
	for _, triggerType := range order {
		if counts[triggerType] > 1 {
			parts = append(parts, fmt.Sprintf("%s x%d", triggerType, counts[triggerType]))
		} else {
			parts = append(parts, triggerType)
		}
	}
	return strings.Join(parts, ", ")
}

// describeAutomationTrigger is a trigger's config and next fire time, one line each.
func describeAutomationTrigger(trigger api.AutomationTriggerResponse) []string {
	var lines []string
	switch trigger.TriggerType {
	case api.AutomationTriggerResponseTriggerTypeSchedule:
		config, err := trigger.TriggerConfig.AsScheduleTriggerConfig()
		if err != nil {
			lines = append(lines, "config: "+automationRawJSON(trigger.TriggerConfig))
			break
		}
		lines = append(lines, fmt.Sprintf("cron %q in %s", config.CronExpression, config.IanaTimezone))
	case api.AutomationTriggerResponseTriggerTypeManual:
		lines = append(lines, "starts a run with 'revyl automation run start', or Run in Revyl")
	case api.AutomationTriggerResponseTriggerTypeIncomingWebhook:
		lines = append(lines, "starts a run on each POST to its incoming webhook URL, which was printed once when it was added or rotated")
	case api.AutomationTriggerResponseTriggerTypeGithubPullRequestOpened, api.AutomationTriggerResponseTriggerTypeGithubPullRequestMerged:
		config, err := trigger.TriggerConfig.AsGithubPullRequestTriggerConfig()
		if err != nil {
			lines = append(lines, "config: "+automationRawJSON(trigger.TriggerConfig))
			break
		}
		lines = append(lines, describePullRequestFilters(config)...)
	default:
		lines = append(lines, "config: "+automationRawJSON(trigger.TriggerConfig))
	}
	if trigger.NextFireAt != nil {
		lines = append(lines, "next fires "+formatAutomationTime(*trigger.NextFireAt))
	}
	return lines
}

func describePullRequestFilters(config api.GithubPullRequestTriggerConfig) []string {
	if config.Filters == nil || len(*config.Filters) == 0 {
		return []string{"every pull request the GitHub connection delivers"}
	}
	lines := make([]string, 0, len(*config.Filters))
	for _, item := range *config.Filters {
		filterType, values, known := pullRequestFilterValues(item)
		if !known {
			lines = append(lines, "filter: "+automationRawJSON(item))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", pullRequestFilterLabel(filterType), strings.Join(values, ", ")))
	}
	return lines
}

func automationRawJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

// describeJSONSchemaFields names a config's fields from its JSON Schema, and
// the variants of any field that is a list of tagged values, such as a
// trigger's filter types.
func describeJSONSchemaFields(schema map[string]any) string {
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		return "none"
	}
	required := map[string]bool{}
	if names, ok := schema["required"].([]any); ok {
		for _, name := range names {
			if text, ok := name.(string); ok {
				required[text] = true
			}
		}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		part := name
		if !required[name] {
			part += " (optional)"
		}
		if variants := jsonSchemaListVariants(properties[name]); len(variants) > 0 {
			part += ": " + strings.Join(variants, ", ")
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func jsonSchemaListVariants(property any) []string {
	fields, _ := property.(map[string]any)
	items, _ := fields["items"].(map[string]any)
	discriminator, _ := items["discriminator"].(map[string]any)
	mapping, _ := discriminator["mapping"].(map[string]any)
	variants := make([]string, 0, len(mapping))
	for variant := range mapping {
		variants = append(variants, variant)
	}
	sort.Strings(variants)
	return variants
}

func printAutomationRunSummary(w io.Writer, run *api.AutomationRunDetailResponse, devMode bool) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(table, "Run\t%s\n", run.Id)
	fmt.Fprintf(table, "Automation\t%s (%s)\n", run.AutomationName, run.AutomationId)
	fmt.Fprintf(table, "Status\t%s\n", run.RunStatus)
	trigger := string(run.TriggerType)
	if run.AutomationTriggerId != nil {
		trigger += " " + run.AutomationTriggerId.String()
	} else {
		trigger += " (trigger since removed)"
	}
	fmt.Fprintf(table, "Trigger\t%s\n", trigger)
	if run.TriggerEvents != nil {
		for _, event := range *run.TriggerEvents {
			fmt.Fprintf(table, "Event\t%s\n", escapeControlCharacters(describeAutomationTriggerEvent(event)))
		}
	}
	fmt.Fprintf(table, "Action\t%s\n", describeAutomationAction(run.ActionType, run.ActionConfig))
	if run.ProducedTestExecutionId != nil {
		fmt.Fprintf(table, "Produced\ttest execution %s\n", run.ProducedTestExecutionId)
		fmt.Fprintf(table, "Report\t%s\n", organizationTestReportLink(devMode, run.ProducedTestExecutionId.String()))
	}
	fmt.Fprintf(table, "Queued\t%s\n", formatAutomationTime(run.CreatedAt))
	fmt.Fprintf(table, "Started\t%s\n", formatOptionalAutomationTime(run.StartedAt))
	fmt.Fprintf(table, "Finished\t%s\n", formatOptionalAutomationTime(run.FinishedAt))
	if code, message := stringValue(run.FailureCode), stringValue(run.FailureMessage); code != "" || message != "" {
		fmt.Fprintf(table, "Failure\t%s\n", strings.TrimPrefix(code+": "+message, ": "))
	}
	_ = table.Flush()
}

// escapeControlCharacters writes each control character as its Go escape, so
// text a webhook sender or pull request author wrote can't drive the terminal.
func escapeControlCharacters(text string) string {
	var escaped strings.Builder
	for _, character := range text {
		if !unicode.IsControl(character) {
			escaped.WriteRune(character)
			continue
		}
		quoted := strconv.QuoteRune(character)
		escaped.WriteString(quoted[1 : len(quoted)-1])
	}
	return escaped.String()
}

// describeAutomationTriggerEvent is the event that started a run: the schedule
// slot, the webhook request, the pull request, or who started it by hand.
func describeAutomationTriggerEvent(event api.AutomationTriggerEventResponse) string {
	payload, _ := event.EventPayload.(map[string]any)
	text := func(key string) string {
		value, _ := payload[key].(string)
		return value
	}
	switch event.TriggerType {
	case "schedule":
		return "schedule slot " + text("scheduled_for")
	case "manual":
		if user := text("requested_by_user_id"); user != "" {
			return "started by hand by user " + user
		}
		return "started by hand with an API key"
	case "incoming_webhook":
		contentType := text("content_type")
		if contentType == "" {
			contentType = "no content type"
		}
		body := text("request_body_text")
		if raw, ok := payload["request_body_json"]; ok {
			body = automationRawJSON(raw)
		}
		const previewBytes = 200
		if len(body) > previewBytes {
			body = body[:previewBytes] + "... (--json has the whole body)"
		}
		return fmt.Sprintf("webhook POST at %s, %s: %s", formatAutomationTime(event.CreatedAt), contentType, body)
	case "github_pull_request_opened", "github_pull_request_merged":
		number, _ := payload["pull_request_number"].(float64)
		return fmt.Sprintf("%s#%d %q by %s, %s into %s: %s", text("repository_full_name"), int(number), text("pull_request_title"), text("author_github_login"), text("head_branch_name"), text("base_branch_name"), text("pull_request_url"))
	default:
		return fmt.Sprintf("%s at %s: %s", event.TriggerType, formatAutomationTime(event.CreatedAt), automationRawJSON(event.EventPayload))
	}
}
