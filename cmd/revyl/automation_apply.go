package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/api"
)

const automationApplyMaxFileBytes = 1 << 20

// Fields 'revyl automation get --json' prints that Revyl sets itself, so a file
// copied from that output applies without editing.
var (
	automationServerSetFields        = []string{"org_id", "created_by_user_id", "created_at", "updated_at", "deleted_at", "last_run"}
	automationTriggerServerSetFields = []string{"org_id", "automation_id", "next_fire_at", "created_at", "updated_at", "incoming_webhook_url"}
)

func newAutomationApplyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "apply <path|->",
		Short: "Create or update a whole automation from a YAML or JSON file",
		Long: `Create or update a whole automation from one YAML or JSON file, or - for stdin,
in the shape 'revyl automation get --json' prints: name, description,
is_enabled, action_type, action_config and triggers. Fields Revyl sets itself,
such as created_at and last_run, are ignored, so that output applies as it is.

It updates the automation that has the file's id, or else its name, and creates
one when there is none. The file is the whole automation: a field it leaves out
takes its default (is_enabled is true, description is empty).

Its triggers end up matching the file: one with an id is changed in place, one
identical to an existing trigger is left alone, any other is added, and an
existing trigger the file leaves out is removed. The manual trigger always
stays. Each incoming webhook trigger the file added gets its URL printed, once.`,
		Example: `  revyl automation get nightly-smoke --json > nightly-smoke.json
  revyl automation apply nightly-smoke.json

  cat <<'EOF' | revyl automation apply -
  name: nightly-smoke
  action_type: run_test
  action_config:
    test_id: <test-id>
  triggers:
    - trigger_type: schedule
      trigger_config:
        cron_expression: "0 9 * * 1-5"
        iana_timezone: America/Los_Angeles
  EOF`,
		Args: cobra.ExactArgs(1),
		RunE: runAutomationApply,
	}
}

func runAutomationApply(cmd *cobra.Command, args []string) error {
	document, err := readAutomationDocument(cmd, args[0])
	if err != nil {
		return err
	}
	fileID := ""
	if raw, ok := document["id"]; ok && raw != nil {
		text, _ := raw.(string)
		id, err := uuid.Parse(strings.TrimSpace(text))
		if err != nil {
			return usageErrorf("the file's id %v isn't an automation ID: fix it, or leave id out to match the automation by its name", raw)
		}
		fileID = id.String()
	}
	delete(document, "id")
	for _, field := range automationServerSetFields {
		delete(document, field)
	}
	name, _ := document["name"].(string)
	if strings.TrimSpace(name) == "" {
		return usageErrorf("the file needs the automation's name")
	}
	triggers, err := automationDocumentTriggers(document)
	if err != nil {
		return err
	}

	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	var existing *api.AutomationResponse
	matchedByID := false
	if fileID != "" {
		existing, err = client.GetAutomation(cmd.Context(), fileID)
		switch {
		case isAutomationResourceNotFound(err):
			existing = nil
		case err != nil:
			return automationRequestError(cmd, automationAnalyticsDomain, "read automation", err)
		default:
			matchedByID = true
		}
	}
	if existing == nil {
		page, err := client.ListAutomations(cmd.Context(), url.Values{"name": {name}, "limit": {"1"}})
		if err != nil {
			return automationRequestError(cmd, automationAnalyticsDomain, "find automation", err)
		}
		if page.Items != nil && len(*page.Items) > 0 && strings.EqualFold((*page.Items)[0].Name, name) {
			existing = &(*page.Items)[0]
		}
	}
	if !matchedByID {
		for _, trigger := range triggers {
			delete(trigger, "id")
		}
	}

	var (
		automation  api.AutomationResponse
		webhookURLs []api.AutomationIncomingWebhookUrl
		verb        string
	)
	if existing == nil {
		var request api.AutomationCreateRequest
		if err := decodeAutomationDocument(document, &request); err != nil {
			return err
		}
		created, err := client.CreateAutomation(cmd.Context(), request)
		if err != nil {
			return automationRequestError(cmd, automationAnalyticsDomain, "create automation", err)
		}
		automation, verb = created.Automation, "Created"
		if created.IncomingWebhookUrls != nil {
			webhookURLs = *created.IncomingWebhookUrls
		}
		recordAutomationOutcome(cmd, automationAnalyticsDomain, "created", automation.Id.String())
	} else {
		if _, ok := document["is_enabled"]; !ok {
			document["is_enabled"] = true
		}
		var request api.AutomationUpdateRequest
		if err := decodeAutomationDocument(document, &request); err != nil {
			return err
		}
		updated, err := client.ReplaceAutomation(cmd.Context(), existing.Id.String(), request)
		if err != nil {
			return automationRequestError(cmd, automationAnalyticsDomain, "update automation", err)
		}
		automation, verb = updated.Automation, "Updated"
		if updated.IncomingWebhookUrls != nil {
			webhookURLs = *updated.IncomingWebhookUrls
		}
		recordAutomationOutcome(cmd, automationAnalyticsDomain, "updated", "")
	}

	if automationJSONOutput(cmd) {
		output, err := automationWithIncomingWebhookURLs(automation, webhookURLs)
		if err != nil {
			return err
		}
		return writeAutomationJSON(cmd, output)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s automation %s.\n\n", verb, automation.Name)
	printAutomationSummary(out, &automation)
	for _, webhook := range webhookURLs {
		fmt.Fprintf(out, "\nIncoming webhook URL of trigger %s:\n%s\n", webhook.TriggerId, webhook.Url)
	}
	if len(webhookURLs) > 0 {
		fmt.Fprintln(automationProgress(cmd), "\nThis is the only time Revyl shows these URLs: each holds a secret token, so keep it like a password.")
	}
	return nil
}

// readAutomationDocument reads the file, or stdin for -, as YAML, which JSON
// also is, into the JSON object it describes.
func readAutomationDocument(cmd *cobra.Command, path string) (map[string]any, error) {
	var source io.Reader
	if path == "-" {
		source = cmd.InOrStdin()
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, usageErrorf("open %s: %v", path, err)
		}
		defer file.Close()
		source = file
	}
	content, err := io.ReadAll(io.LimitReader(source, automationApplyMaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(content) > automationApplyMaxFileBytes {
		return nil, usageErrorf("%s is over 1 MiB, which is more than any automation needs", path)
	}

	var parsed any
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return nil, usageErrorf("%s isn't valid YAML or JSON: %v", path, err)
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return nil, usageErrorf("%s holds a value JSON can't express, such as a mapping with non-text keys: %v", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil || document == nil {
		return nil, usageErrorf("%s must hold one automation: a mapping with name, action_type, action_config and triggers", path)
	}
	return document, nil
}

// automationDocumentTriggers returns the file's triggers with the fields Revyl
// sets removed, for the caller to edit in place.
func automationDocumentTriggers(document map[string]any) ([]map[string]any, error) {
	raw, ok := document["triggers"]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, usageErrorf("the file's triggers must be a list")
	}
	triggers := make([]map[string]any, 0, len(items))
	for index, item := range items {
		trigger, ok := item.(map[string]any)
		if !ok {
			return nil, usageErrorf("trigger %d in the file must be a mapping with trigger_type and trigger_config", index+1)
		}
		for _, field := range automationTriggerServerSetFields {
			delete(trigger, field)
		}
		triggers = append(triggers, trigger)
	}
	return triggers, nil
}

func decodeAutomationDocument(document map[string]any, request any) error {
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode automation: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil {
		return usageErrorf("the file doesn't match an automation (%v): it takes name, description, is_enabled, action_type, action_config and triggers, in the shape 'revyl automation get --json' prints", err)
	}
	return nil
}

// automationWithIncomingWebhookURLs is the automation object with each added
// incoming webhook trigger carrying the URL Revyl shows only this once.
func automationWithIncomingWebhookURLs(automation api.AutomationResponse, webhookURLs []api.AutomationIncomingWebhookUrl) (any, error) {
	if len(webhookURLs) == 0 {
		return automation, nil
	}
	encoded, err := json.Marshal(automation)
	if err != nil {
		return nil, fmt.Errorf("encode automation: %w", err)
	}
	var output map[string]any
	if err := json.Unmarshal(encoded, &output); err != nil {
		return nil, fmt.Errorf("encode automation: %w", err)
	}
	triggers, _ := output["triggers"].([]any)
	for _, item := range triggers {
		trigger, _ := item.(map[string]any)
		for _, webhook := range webhookURLs {
			if trigger != nil && trigger["id"] == webhook.TriggerId.String() {
				trigger["incoming_webhook_url"] = webhook.Url
			}
		}
	}
	return output, nil
}
