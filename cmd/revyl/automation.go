package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

const (
	automationAnalyticsDomain        = "automation"
	automationTriggerAnalyticsDomain = "automation_trigger"
	automationRunAnalyticsDomain     = "automation_run"

	automationActionTypeRunTest = "run_test"
	automationListDefaultLimit  = 25
	automationListMaxLimit      = 100

	automationNotEnabledMessage        = "Automations isn't on for your organization yet. It's experimental, and Revyl staff turn it on per organization: ask Revyl support (support@revyl.ai) to turn it on"
	automationUnsupportedServerMessage = "this Revyl server doesn't have Automations yet: it answers /api/v1/automations with no such route. Point the CLI at a server that has them, or try again once Automations reaches yours"
)

func newAutomationCommand() *cobra.Command {
	automation := &cobra.Command{
		Use:   "automation",
		Short: "Create automations, add their triggers, and start and follow their runs",
		Long: `Create automations, add their triggers, and start and follow their runs.

An automation takes one action each time it runs (run_test starts a test), and
a run starts whenever one of its triggers fires: by hand, on a schedule, on a
POST to its incoming webhook URL, or when a GitHub pull request is opened or
merged. Every automation has a manual trigger, so it can run right away.

An automation is addressed by its name, which is unique in your organization,
or by its ID. A trigger and a run are addressed by their ID.

Automations is experimental, and Revyl staff turn it on per organization. Until
they do, every command here says so and exits 1.`,
		Example: `  # Create an automation that runs a test, then give it a weekday schedule
  revyl automation create --name nightly-smoke --action-type run_test --test login-flow
  revyl automation trigger add nightly-smoke --trigger-type schedule --cron "0 9 * * 1-5" --timezone America/Los_Angeles

  # Start a run now and block until it ends
  revyl automation run start nightly-smoke

  # Keep an automation in Git and apply it from CI
  revyl automation get nightly-smoke --json > nightly-smoke.json
  revyl automation apply nightly-smoke.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	create := &cobra.Command{
		Use:   "create",
		Short: "Create an automation with its action",
		Long: `Create an automation: a name and the one action each run takes.

It comes with its manual trigger, so it can run right away with
'revyl automation run start'. Add its other triggers with
'revyl automation trigger add', or create it with all of them in one step with
'revyl automation apply'.

run_test is the only action type so far: --test names the test each run starts.
With --no-enable the automation starts off, so only a manual run starts it until
'revyl automation enable'.`,
		Example: `  revyl automation create --name nightly-smoke --action-type run_test --test login-flow
  revyl automation create --name checkout-canary --action-type run_test --test <test-id> --description "Checkout on every merge" --no-enable --json`,
		Args: cobra.NoArgs,
		RunE: runAutomationCreate,
	}
	create.Flags().String("name", "", "The automation's name: 1 to 120 characters, unique in your organization")
	create.Flags().String("action-type", "", "The action each run takes: run_test, which starts the test --test names")
	create.Flags().String("test", "", "With --action-type run_test, the test each run starts: its name or ID")
	create.Flags().String("description", "", "What the automation is for")
	create.Flags().Bool("no-enable", false, "Create it off, so only a manual run starts it until 'revyl automation enable'")
	_ = create.MarkFlagRequired("name")
	_ = create.MarkFlagRequired("action-type")

	get := &cobra.Command{
		Use:   "get <automation-name|id>",
		Short: "Read one automation, with its triggers and last run",
		Long: `Read one automation: whether it's on, its action, each trigger with its ID, type,
config and next fire time, and its last run.

With --json, stdout is the automation object, in the shape
'revyl automation apply' takes back.`,
		Example: `  revyl automation get nightly-smoke
  revyl automation get nightly-smoke --json > nightly-smoke.json`,
		Args: cobra.ExactArgs(1),
		RunE: runAutomationGet,
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List automations, newest first",
		Long: `List your organization's automations, newest first.

Every filter applies before the page is cut, so a page holds up to --limit
matching automations. When there are more, the command prints the cursor that
reads the next page.`,
		Example: `  revyl automation list
  revyl automation list --only-enabled --trigger-type schedule
  revyl automation list --search smoke --limit 50 --json`,
		Args: cobra.NoArgs,
		RunE: runAutomationList,
	}
	list.Flags().String("search", "", "Only automations whose name contains this text")
	list.Flags().Bool("only-enabled", false, "Only the automations that are on")
	list.Flags().Bool("only-disabled", false, "Only the automations that are off")
	list.Flags().StringArray("trigger-type", nil, "Only automations with a trigger of this type, e.g. schedule; repeat for any of several")
	list.Flags().Int("limit", automationListDefaultLimit, "Page size, 1 to 100")
	list.Flags().String("cursor", "", "The next_cursor from the previous page")
	list.MarkFlagsMutuallyExclusive("only-enabled", "only-disabled")

	update := &cobra.Command{
		Use:   "update <automation-name|id>",
		Short: "Change an automation's name, description or action",
		Long: `Change an automation's name, description or action. Only the values you pass
change.

Runs that already exist keep the action they were created with. An empty
--description clears it. --action-type and --test replace the action, written as
in 'revyl automation create'.`,
		Example: `  revyl automation update nightly-smoke --name morning-smoke
  revyl automation update nightly-smoke --description ""
  revyl automation update nightly-smoke --action-type run_test --test checkout-flow --json`,
		Args: cobra.ExactArgs(1),
		RunE: runAutomationUpdate,
	}
	update.Flags().String("name", "", "The automation's new name: 1 to 120 characters, unique in your organization")
	update.Flags().String("description", "", "The new description; an empty string clears it")
	update.Flags().String("action-type", "", "Replace the action: run_test, with the test --test names")
	update.Flags().String("test", "", "With --action-type run_test, the test each new run starts: its name or ID")

	enable := &cobra.Command{
		Use:   "enable <automation-name|id>",
		Short: "Turn an automation on",
		Long: `Turn an automation on, so its triggers start runs again.

Its schedules count from now, so nothing fires for the time it was off.`,
		Example: "  revyl automation enable nightly-smoke",
		Args:    cobra.ExactArgs(1),
		RunE:    func(cmd *cobra.Command, args []string) error { return runAutomationSetEnabled(cmd, args[0], true) },
	}

	disable := &cobra.Command{
		Use:   "disable <automation-name|id>",
		Short: "Turn an automation off",
		Long: `Turn an automation off: its triggers stop starting runs, its incoming webhook
URLs answer 404, and its queued runs are cancelled.

Starting a run by hand still works.`,
		Example: "  revyl automation disable nightly-smoke",
		Args:    cobra.ExactArgs(1),
		RunE:    func(cmd *cobra.Command, args []string) error { return runAutomationSetEnabled(cmd, args[0], false) },
	}

	deleteCommand := &cobra.Command{
		Use:   "delete <automation-name|id>",
		Short: "Delete an automation",
		Long: `Delete an automation. Its triggers stop, its queued runs are cancelled, and its
name is free to use again.

It asks for confirmation first. --yes skips the question, and is required when
no terminal is attached.`,
		Example: `  revyl automation delete nightly-smoke
  revyl automation delete nightly-smoke --yes`,
		Args: cobra.ExactArgs(1),
		RunE: runAutomationDelete,
	}
	deleteCommand.Flags().Bool("yes", false, "Skip the confirmation; required when no terminal is attached")

	automation.AddCommand(
		create,
		get,
		list,
		update,
		enable,
		disable,
		deleteCommand,
		newAutomationApplyCommand(),
		newAutomationTriggerCommand(),
		newAutomationRunCommand(),
		newAutomationTriggerTypesCommand(),
		newAutomationActionTypesCommand(),
	)
	recordAnalyticsDomainBeforeEachRun(automation, automationAnalyticsDomain)
	return automation
}

// recordAnalyticsDomainBeforeEachRun makes each command in the group report
// its analytics domain, reads included; a write adds its outcome as it ends.
func recordAnalyticsDomainBeforeEachRun(group *cobra.Command, domain string) {
	for _, command := range group.Commands() {
		runE := command.RunE
		if command.HasSubCommands() || runE == nil {
			continue
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: domain})
			return runE(cmd, args)
		}
	}
}

func automationClient(cmd *cobra.Command) (*api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	return api.NewClientWithDevMode(apiKey, devMode), nil
}

func automationJSONOutput(cmd *cobra.Command) bool {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	return jsonOutput
}

// automationProgress is where progress and hints go: stderr, and nowhere at all
// with --json or --quiet, so machine-readable output is never mixed with text.
func automationProgress(cmd *cobra.Command) io.Writer {
	quiet, _ := cmd.Flags().GetBool("quiet")
	if quiet || automationJSONOutput(cmd) {
		return io.Discard
	}
	return cmd.ErrOrStderr()
}

func writeAutomationJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// recordAutomationOutcome adds the command's bounded outcome to its one
// terminal analytics event. entityID is recorded only when the server issued
// it in this command, never when the caller typed it.
func recordAutomationOutcome(cmd *cobra.Command, domain, status, entityID string) {
	completion := analytics.CommandCompletion{Domain: domain, DomainStatus: status}
	if entityID != "" {
		completion.Properties = map[string]interface{}{"entity_id": entityID}
	}
	analytics.SetCommandCompletion(cmd.Context(), completion)
}

// automationRoutesMissing reports a server that predates Automations: it
// answers 404 with the framework's generic body, or 405 for a path it serves
// with other methods. Every Automations 404 for a missing resource names it.
func automationRoutesMissing(apiErr *api.APIError) bool {
	switch apiErr.StatusCode {
	case http.StatusMethodNotAllowed:
		return true
	case http.StatusNotFound:
		detail := strings.TrimSpace(apiErr.Detail)
		return detail == "" || strings.EqualFold(detail, "Not Found")
	default:
		return false
	}
}

func isAutomationResourceNotFound(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound && !automationRoutesMissing(apiErr)
}

// automationRequestError is the error a command exits with after a failed
// Automations request. It tells an organization without Automations and a
// server without its routes apart from every other failure.
func automationRequestError(cmd *cobra.Command, domain, action string, err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusForbidden && apiErr.DetailString("code") == api.AutomationsNotEnabledCode:
			recordAutomationOutcome(cmd, domain, "automations_not_enabled", "")
			return usageErrorf("%s", automationNotEnabledMessage)
		case automationRoutesMissing(apiErr):
			recordAutomationOutcome(cmd, domain, "unsupported_server", "")
			return usageErrorf("%s", automationUnsupportedServerMessage)
		}
	}
	return analytics.WithSafeDiagnostic(fmt.Errorf("%s: %w", action, err), action+" failed")
}

// resolveAutomation reads the automation a command argument names: its ID, or
// else its name, matched exactly and ignoring case.
func resolveAutomation(cmd *cobra.Command, client *api.Client, domain, reference string) (*api.AutomationResponse, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return nil, usageErrorf("pass an automation's name or ID")
	}
	if id, err := uuid.Parse(reference); err == nil {
		automation, err := client.GetAutomation(cmd.Context(), id.String())
		if isAutomationResourceNotFound(err) {
			return nil, usageErrorf("no automation in your organization has ID %s; list them with 'revyl automation list'", id)
		}
		if err != nil {
			return nil, automationRequestError(cmd, domain, "read automation", err)
		}
		return automation, nil
	}
	page, err := client.ListAutomations(cmd.Context(), url.Values{"name": {reference}, "limit": {"1"}})
	if err != nil {
		return nil, automationRequestError(cmd, domain, "find automation", err)
	}
	if page.Items != nil && len(*page.Items) > 0 && strings.EqualFold((*page.Items)[0].Name, reference) {
		return &(*page.Items)[0], nil
	}
	return nil, analytics.WithSafeDiagnostic(
		usageErrorf("no automation named %q in your organization; list them with 'revyl automation list'", reference),
		"automation not found",
	)
}

// readAutomationAction reads --action-type and --test into the action they
// name, resolving the test's name to its ID.
func readAutomationAction(cmd *cobra.Command, client *api.Client) (string, api.RunTestActionConfig, string, error) {
	actionType, _ := cmd.Flags().GetString("action-type")
	testReference, _ := cmd.Flags().GetString("test")
	actionType = strings.TrimSpace(actionType)
	if actionType != automationActionTypeRunTest {
		return "", api.RunTestActionConfig{}, "", usageErrorf("--action-type %q isn't an action type this CLI knows: use run_test, or run 'revyl upgrade' for newer ones ('revyl automation action-types' lists your organization's)", actionType)
	}
	if strings.TrimSpace(testReference) == "" {
		return "", api.RunTestActionConfig{}, "", usageErrorf("--action-type run_test needs --test <test-name|id>, the test each run starts")
	}
	testID, testName, err := resolveTestID(cmd.Context(), strings.TrimSpace(testReference), nil, client)
	if err != nil {
		return "", api.RunTestActionConfig{}, "", err
	}
	parsed, err := uuid.Parse(testID)
	if err != nil {
		return "", api.RunTestActionConfig{}, "", usageErrorf("--test resolved to %q, which isn't a test ID", testID)
	}
	return actionType, api.RunTestActionConfig{TestId: parsed}, testName, nil
}

func runAutomationCreate(cmd *cobra.Command, _ []string) error {
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	name, _ := cmd.Flags().GetString("name")
	description, _ := cmd.Flags().GetString("description")
	noEnable, _ := cmd.Flags().GetBool("no-enable")

	actionType, actionConfig, _, err := readAutomationAction(cmd, client)
	if err != nil {
		return err
	}
	request := api.AutomationCreateRequest{
		Name:         name,
		ActionType:   actionType,
		ActionConfig: actionConfig,
	}
	if cmd.Flags().Changed("description") {
		request.Description = &description
	}
	if noEnable {
		enabled := false
		request.IsEnabled = &enabled
	}
	created, err := client.CreateAutomation(cmd.Context(), request)
	if err != nil {
		return automationRequestError(cmd, automationAnalyticsDomain, "create automation", err)
	}
	automation := created.Automation
	recordAutomationOutcome(cmd, automationAnalyticsDomain, "created", automation.Id.String())
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, automation)
	}
	printAutomationSummary(cmd.OutOrStdout(), &automation)
	fmt.Fprintf(automationProgress(cmd), "\nStart a run now: revyl automation run start %s\nAdd a trigger:   revyl automation trigger add %s --trigger-type schedule --cron \"0 9 * * 1-5\" --timezone <iana-timezone>\n", automation.Id, automation.Id)
	return nil
}

func runAutomationGet(cmd *cobra.Command, args []string) error {
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	automation, err := resolveAutomation(cmd, client, automationAnalyticsDomain, args[0])
	if err != nil {
		return err
	}
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, automation)
	}
	printAutomationSummary(cmd.OutOrStdout(), automation)
	return nil
}

func runAutomationList(cmd *cobra.Command, _ []string) error {
	search, _ := cmd.Flags().GetString("search")
	onlyEnabled, _ := cmd.Flags().GetBool("only-enabled")
	onlyDisabled, _ := cmd.Flags().GetBool("only-disabled")
	triggerTypes, _ := cmd.Flags().GetStringArray("trigger-type")
	limit, _ := cmd.Flags().GetInt("limit")
	cursor, _ := cmd.Flags().GetString("cursor")
	if limit < 1 || limit > automationListMaxLimit {
		return usageErrorf("--limit must be from 1 to %d", automationListMaxLimit)
	}

	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if strings.TrimSpace(search) != "" {
		query.Set("search", search)
	}
	switch {
	case onlyEnabled:
		query.Set("is_enabled", "true")
	case onlyDisabled:
		query.Set("is_enabled", "false")
	}
	for _, triggerType := range triggerTypes {
		query.Add("trigger_type", strings.TrimSpace(triggerType))
	}
	if strings.TrimSpace(cursor) != "" {
		query.Set("cursor", cursor)
	}

	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	page, err := client.ListAutomations(cmd.Context(), query)
	if err != nil {
		return automationRequestError(cmd, automationAnalyticsDomain, "list automations", err)
	}
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, page)
	}

	var items []api.AutomationResponse
	if page.Items != nil {
		items = *page.Items
	}
	if len(items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No automations match.")
		return nil
	}
	printAutomationList(cmd.OutOrStdout(), items)
	if nextCursor := stringValue(page.NextCursor); nextCursor != "" {
		fmt.Fprintf(automationProgress(cmd), "\nMore automations: add --cursor '%s'\n", nextCursor)
	}
	return nil
}

func runAutomationUpdate(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	actionChanged := flags.Changed("action-type") || flags.Changed("test")
	if !flags.Changed("name") && !flags.Changed("description") && !actionChanged {
		return usageErrorf("nothing to change: pass --name, --description, or --action-type with --test")
	}
	if actionChanged && !(flags.Changed("action-type") && flags.Changed("test")) {
		return usageErrorf("--action-type and --test replace the action together: pass both")
	}

	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	automation, err := resolveAutomation(cmd, client, automationAnalyticsDomain, args[0])
	if err != nil {
		return err
	}

	var request api.AutomationPatchRequest
	clearDescription := false
	if flags.Changed("name") {
		name, _ := flags.GetString("name")
		request.Name = &name
	}
	if flags.Changed("description") {
		description, _ := flags.GetString("description")
		if description == "" {
			clearDescription = true
		} else {
			request.Description = &description
		}
	}
	if actionChanged {
		actionType, actionConfig, _, err := readAutomationAction(cmd, client)
		if err != nil {
			return err
		}
		request.ActionType = &actionType
		request.ActionConfig = &actionConfig
	}

	updated, err := client.PatchAutomation(cmd.Context(), automation.Id.String(), request, clearDescription)
	if err != nil {
		return automationRequestError(cmd, automationAnalyticsDomain, "update automation", err)
	}
	recordAutomationOutcome(cmd, automationAnalyticsDomain, "updated", "")
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, updated)
	}
	printAutomationSummary(cmd.OutOrStdout(), updated)
	return nil
}

func runAutomationSetEnabled(cmd *cobra.Command, reference string, enabled bool) error {
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	automation, err := resolveAutomation(cmd, client, automationAnalyticsDomain, reference)
	if err != nil {
		return err
	}
	updated, err := client.PatchAutomation(cmd.Context(), automation.Id.String(), api.AutomationPatchRequest{IsEnabled: &enabled}, false)
	if err != nil {
		action := "turn automation off"
		if enabled {
			action = "turn automation on"
		}
		return automationRequestError(cmd, automationAnalyticsDomain, action, err)
	}
	status := "disabled"
	if enabled {
		status = "enabled"
	}
	recordAutomationOutcome(cmd, automationAnalyticsDomain, status, "")
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, updated)
	}
	if enabled {
		fmt.Fprintf(cmd.OutOrStdout(), "Automation %s is on. Its schedules count from now.\n", updated.Name)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Automation %s is off: its triggers stop starting runs, its incoming webhook URLs answer 404, and its queued runs were cancelled. Starting a run by hand still works.\n", updated.Name)
	}
	return nil
}

func runAutomationDelete(cmd *cobra.Command, args []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes && !ui.IsInteractive() {
		return usageErrorf("no terminal is attached to confirm the delete: pass --yes")
	}

	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	automation, err := resolveAutomation(cmd, client, automationAnalyticsDomain, args[0])
	if err != nil {
		return err
	}
	if !yes {
		confirmed, err := ui.PromptConfirm(fmt.Sprintf("Delete automation %s? Its triggers stop and its queued runs are cancelled.", automation.Name), false)
		if err != nil {
			return fmt.Errorf("confirm delete: %w", err)
		}
		if !confirmed {
			recordAutomationOutcome(cmd, automationAnalyticsDomain, "delete_declined", "")
			fmt.Fprintln(cmd.ErrOrStderr(), "Nothing was deleted.")
			return nil
		}
	}
	if err := client.DeleteAutomation(cmd.Context(), automation.Id.String()); err != nil {
		return automationRequestError(cmd, automationAnalyticsDomain, "delete automation", err)
	}
	recordAutomationOutcome(cmd, automationAnalyticsDomain, "deleted", "")
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, automationDeletedOutput{ID: automation.Id.String(), Deleted: true})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted automation %s (%s). Its name is free to use again.\n", automation.Name, automation.Id)
	return nil
}

// automationDeletedOutput is the --json output of a delete, which has no
// object left to print.
type automationDeletedOutput struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func newAutomationActionTypesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "action-types",
		Short: "List the action types your organization can use",
		Long: `List the action types your organization can use, each with its config fields
and what a run of it produces.

With --json, stdout is the catalog as Revyl serves it, each type's config as a
JSON Schema.`,
		Example: `  revyl automation action-types
  revyl automation action-types --json`,
		Args: cobra.NoArgs,
		RunE: runAutomationActionTypes,
	}
}

func runAutomationActionTypes(cmd *cobra.Command, _ []string) error {
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	actionTypes, err := client.ListAutomationActionTypes(cmd.Context())
	if err != nil {
		return automationRequestError(cmd, automationAnalyticsDomain, "list action types", err)
	}
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, actionTypes)
	}
	rows := make([][]string, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		rows = append(rows, []string{actionType.ActionType, describeJSONSchemaFields(actionType.ActionConfigJsonSchema), strings.ReplaceAll(actionType.ProducedResourceType, "_", " ")})
	}
	printAutomationTable(cmd.OutOrStdout(), []string{"ACTION TYPE", "CONFIG", "A RUN PRODUCES"}, rows)
	return nil
}
