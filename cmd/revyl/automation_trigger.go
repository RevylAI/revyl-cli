package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

// pullRequestFilterFlag ties a GitHub pull request filter type to the flag
// that sets it, so trigger add, trigger update and their output stay in step.
type pullRequestFilterFlag struct {
	flag       string
	filterType string
	label      string
	usage      string
}

var pullRequestFilterFlags = []pullRequestFilterFlag{
	{flag: "pull-request-repository", filterType: "pull_request_repository_is_any_of", label: "repositories", usage: "Only pull requests in this repository, as owner/repo; repeat for any of several"},
	{flag: "pull-request-base-branch", filterType: "pull_request_base_branch_is_any_of", label: "base branches", usage: "Only pull requests into this base branch; repeat for any of several"},
	{flag: "pull-request-author", filterType: "pull_request_author_is_any_of", label: "authors", usage: "Only pull requests by this GitHub login; repeat for any of several"},
	{flag: "pull-request-label", filterType: "pull_request_has_any_label", label: "labels", usage: "Only pull requests carrying this label; repeat for any of several"},
}

func pullRequestFilterLabel(filterType string) string {
	for _, filter := range pullRequestFilterFlags {
		if filter.filterType == filterType {
			return filter.label
		}
	}
	return filterType
}

// pullRequestFilterValues reads one filter's type and values. known is false
// for a filter type this CLI predates, which is kept as it is.
func pullRequestFilterValues(item api.GithubPullRequestTriggerConfig_Filters_Item) (filterType string, values []string, known bool) {
	filterType, err := item.Discriminator()
	if err != nil {
		return "", nil, false
	}
	switch filterType {
	case "pull_request_repository_is_any_of":
		filter, err := item.AsPullRequestRepositoryIsAnyOfFilter()
		return filterType, filter.RepositoryFullNames, err == nil
	case "pull_request_base_branch_is_any_of":
		filter, err := item.AsPullRequestBaseBranchIsAnyOfFilter()
		return filterType, filter.BranchNames, err == nil
	case "pull_request_author_is_any_of":
		filter, err := item.AsPullRequestAuthorIsAnyOfFilter()
		return filterType, filter.GithubLogins, err == nil
	case "pull_request_has_any_label":
		filter, err := item.AsPullRequestHasAnyLabelFilter()
		return filterType, filter.LabelNames, err == nil
	default:
		return filterType, nil, false
	}
}

func newPullRequestFilter(filterType string, values []string) (api.GithubPullRequestTriggerConfig_Filters_Item, error) {
	var item api.GithubPullRequestTriggerConfig_Filters_Item
	var err error
	switch filterType {
	case "pull_request_repository_is_any_of":
		err = item.FromPullRequestRepositoryIsAnyOfFilter(api.PullRequestRepositoryIsAnyOfFilter{RepositoryFullNames: values})
	case "pull_request_base_branch_is_any_of":
		err = item.FromPullRequestBaseBranchIsAnyOfFilter(api.PullRequestBaseBranchIsAnyOfFilter{BranchNames: values})
	case "pull_request_author_is_any_of":
		err = item.FromPullRequestAuthorIsAnyOfFilter(api.PullRequestAuthorIsAnyOfFilter{GithubLogins: values})
	case "pull_request_has_any_label":
		err = item.FromPullRequestHasAnyLabelFilter(api.PullRequestHasAnyLabelFilter{LabelNames: values})
	default:
		err = fmt.Errorf("unknown pull request filter type %q", filterType)
	}
	return item, err
}

func addPullRequestFilterFlags(cmd *cobra.Command) {
	for _, filter := range pullRequestFilterFlags {
		cmd.Flags().StringArray(filter.flag, nil, filter.usage)
	}
}

// changedPullRequestFilters reads each filter flag that was passed into the
// values it sets, keyed by filter type.
func changedPullRequestFilters(cmd *cobra.Command) (map[string][]string, error) {
	changed := map[string][]string{}
	for _, filter := range pullRequestFilterFlags {
		if !cmd.Flags().Changed(filter.flag) {
			continue
		}
		raw, _ := cmd.Flags().GetStringArray(filter.flag)
		values := make([]string, 0, len(raw))
		for _, value := range raw {
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, usageErrorf("--%s takes a value; leave the flag out to drop the filter", filter.flag)
			}
			if !slices.Contains(values, value) {
				values = append(values, value)
			}
		}
		changed[filter.filterType] = values
	}
	return changed, nil
}

func isPullRequestTriggerType(triggerType string) bool {
	return triggerType == string(api.AutomationTriggerResponseTriggerTypeGithubPullRequestOpened) ||
		triggerType == string(api.AutomationTriggerResponseTriggerTypeGithubPullRequestMerged)
}

func parseAutomationRecordID(kind, reference string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(reference))
	if err != nil {
		return "", usageErrorf("%q isn't an automation %s ID: pass the ID that 'revyl automation get' or 'revyl automation run list' prints", reference, kind)
	}
	return id.String(), nil
}

func automationTriggerNotFound(triggerID string) error {
	return usageErrorf("no trigger in your organization has ID %s; 'revyl automation get <automation>' lists an automation's triggers", triggerID)
}

func newAutomationTriggerCommand() *cobra.Command {
	trigger := &cobra.Command{
		Use:   "trigger",
		Short: "Add, read, change and remove an automation's triggers",
		Long: `Add, read, change and remove an automation's triggers.

A trigger starts a run of its automation: on a schedule, on a POST to its
incoming webhook URL, or when a GitHub pull request is opened or merged. Every
automation also has one manual trigger, which 'revyl automation run start'
uses. A trigger is addressed by its ID, which 'revyl automation get' prints.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	add := &cobra.Command{
		Use:   "add <automation-name|id> --trigger-type <trigger-type>",
		Short: "Add a trigger to an automation",
		Long: `Add a trigger to an automation. --trigger-type says which, and its own flags
configure it:

  schedule                    starts a run each time its cron expression comes
                              due: --cron and --timezone, both required
  incoming_webhook            starts a run on each POST to its own URL (any body
                              up to 256 KiB, optional Idempotency-Key header).
                              The URL is printed once, here: it holds a secret
                              token that Revyl keeps only a digest of
  github_pull_request_opened  starts a run when a pull request is opened, or
  github_pull_request_merged  when one is merged. Your organization needs a
                              GitHub connection ('revyl github connect'). A pull
                              request has to pass every filter you set; within
                              one filter, any of its values passes

'revyl automation trigger-types' lists every type with what it takes.`,
		Example: `  revyl automation trigger add nightly-smoke --trigger-type schedule --cron "0 9 * * 1-5" --timezone America/Los_Angeles
  revyl automation trigger add nightly-smoke --trigger-type incoming_webhook
  revyl automation trigger add nightly-smoke --trigger-type github_pull_request_merged \
    --pull-request-repository acme/mobile-app --pull-request-base-branch main`,
		Args: cobra.ExactArgs(1),
		RunE: runAutomationTriggerAdd,
	}
	add.Flags().String("trigger-type", "", "schedule, incoming_webhook, github_pull_request_opened or github_pull_request_merged")
	add.Flags().String("cron", "", "Schedule: five fields (minute, hour, day of month, month, day of week), e.g. \"0 9 * * 1-5\"")
	add.Flags().String("timezone", "", "Schedule: the IANA timezone the cron is read in, e.g. America/Los_Angeles")
	addPullRequestFilterFlags(add)
	_ = add.MarkFlagRequired("trigger-type")

	get := &cobra.Command{
		Use:   "get <automation-trigger-id>",
		Short: "Read one trigger",
		Long: `Read one trigger: its type, config and next fire time, and the automation it
belongs to.`,
		Example: "  revyl automation trigger get <automation-trigger-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE:    runAutomationTriggerGet,
	}

	update := &cobra.Command{
		Use:   "update <automation-trigger-id>",
		Short: "Change a trigger in place",
		Long: `Change a trigger in place. It keeps its ID, so its past runs still point at it,
and its type can't change. Each flag replaces only the value it names.

On a schedule trigger, pass --cron, --timezone or both. On a GitHub pull
request trigger, each filter flag you pass replaces that filter's whole list,
and --remove-filter drops a filter altogether. An incoming webhook trigger and
the manual trigger have nothing to change.`,
		Example: `  revyl automation trigger update <automation-trigger-id> --cron "30 8 * * 1-5"
  revyl automation trigger update <automation-trigger-id> --pull-request-label ready-for-qa --remove-filter pull_request_author_is_any_of`,
		Args: cobra.ExactArgs(1),
		RunE: runAutomationTriggerUpdate,
	}
	update.Flags().String("cron", "", "Schedule: the new cron expression")
	update.Flags().String("timezone", "", "Schedule: the new IANA timezone")
	addPullRequestFilterFlags(update)
	update.Flags().StringArray("remove-filter", nil, "GitHub pull request: drop this filter, e.g. pull_request_has_any_label; repeat for several")

	remove := &cobra.Command{
		Use:   "remove <automation-trigger-id>",
		Short: "Remove a trigger",
		Long: `Remove a trigger. Its past runs keep their trigger type but no longer point at a
trigger, and an incoming webhook trigger's URL stops working at once. The
manual trigger can't be removed.`,
		Example: "  revyl automation trigger remove <automation-trigger-id>",
		Args:    cobra.ExactArgs(1),
		RunE:    runAutomationTriggerRemove,
	}

	rotate := &cobra.Command{
		Use:   "rotate-incoming-webhook-url <automation-trigger-id>",
		Short: "Give an incoming webhook trigger a new URL",
		Long: `Give an incoming webhook trigger a new URL. The old URL stops working at once,
the new one is printed once, and the trigger keeps its ID.`,
		Example: "  revyl automation trigger rotate-incoming-webhook-url <automation-trigger-id>",
		Args:    cobra.ExactArgs(1),
		RunE:    runAutomationTriggerRotateIncomingWebhookURL,
	}

	trigger.AddCommand(add, get, update, remove, rotate)
	recordAnalyticsDomainBeforeEachRun(trigger, automationTriggerAnalyticsDomain)
	return trigger
}

func runAutomationTriggerAdd(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	triggerType := strings.TrimSpace(automationStringFlag(cmd, "trigger-type"))
	filters, err := changedPullRequestFilters(cmd)
	if err != nil {
		return err
	}
	scheduleFlagsPassed := flags.Changed("cron") || flags.Changed("timezone")

	var request api.AutomationCreateRequest_Triggers_Item
	switch {
	case triggerType == string(api.AutomationTriggerResponseTriggerTypeSchedule):
		if len(filters) > 0 {
			return usageErrorf("the --pull-request-* flags configure a GitHub pull request trigger, not a schedule")
		}
		cron, timezone := strings.TrimSpace(automationStringFlag(cmd, "cron")), strings.TrimSpace(automationStringFlag(cmd, "timezone"))
		if cron == "" || timezone == "" {
			return usageErrorf("a schedule trigger needs both --cron <cron-expression> and --timezone <iana-timezone>, e.g. --cron \"0 9 * * 1-5\" --timezone America/Los_Angeles")
		}
		err = request.FromScheduleTriggerTypeAndConfig(api.ScheduleTriggerTypeAndConfig{TriggerConfig: api.ScheduleTriggerConfig{CronExpression: cron, IanaTimezone: timezone}})
	case triggerType == string(api.AutomationTriggerResponseTriggerTypeIncomingWebhook):
		if scheduleFlagsPassed || len(filters) > 0 {
			return usageErrorf("an incoming webhook trigger takes no other flags: it has nothing to configure")
		}
		err = request.FromIncomingWebhookTriggerTypeAndConfig(api.IncomingWebhookTriggerTypeAndConfig{TriggerConfig: api.IncomingWebhookTriggerConfig{}})
	case isPullRequestTriggerType(triggerType):
		if scheduleFlagsPassed {
			return usageErrorf("--cron and --timezone configure a schedule trigger, not a GitHub pull request trigger")
		}
		config := api.GithubPullRequestTriggerConfig{}
		if len(filters) > 0 {
			items := make([]api.GithubPullRequestTriggerConfig_Filters_Item, 0, len(filters))
			for _, filter := range pullRequestFilterFlags {
				values, ok := filters[filter.filterType]
				if !ok {
					continue
				}
				item, err := newPullRequestFilter(filter.filterType, values)
				if err != nil {
					return err
				}
				items = append(items, item)
			}
			config.Filters = &items
		}
		if triggerType == string(api.AutomationTriggerResponseTriggerTypeGithubPullRequestOpened) {
			err = request.FromGithubPullRequestOpenedTriggerTypeAndConfig(api.GithubPullRequestOpenedTriggerTypeAndConfig{TriggerConfig: config})
		} else {
			err = request.FromGithubPullRequestMergedTriggerTypeAndConfig(api.GithubPullRequestMergedTriggerTypeAndConfig{TriggerConfig: config})
		}
	case triggerType == string(api.AutomationTriggerResponseTriggerTypeManual):
		return usageErrorf("every automation already has its one manual trigger: start a run with 'revyl automation run start <automation-name|id>'")
	default:
		return usageErrorf("--trigger-type %q isn't a trigger type this CLI can add: use schedule, incoming_webhook, github_pull_request_opened or github_pull_request_merged, or run 'revyl upgrade' for newer ones ('revyl automation trigger-types' lists your organization's)", triggerType)
	}
	if err != nil {
		return fmt.Errorf("build trigger: %w", err)
	}

	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	automation, err := resolveAutomation(cmd, client, automationTriggerAnalyticsDomain, args[0])
	if err != nil {
		return err
	}
	added, err := client.AddAutomationTrigger(cmd.Context(), automation.Id.String(), request)
	if err != nil {
		return automationRequestError(cmd, automationTriggerAnalyticsDomain, "add trigger", err)
	}
	recordAutomationOutcome(cmd, automationTriggerAnalyticsDomain, "added", added.Id.String())
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, added)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Added %s trigger %s to automation %s.\n", added.TriggerType, added.Id, automation.Name)
	for _, line := range describeAutomationTrigger(added.Trigger()) {
		fmt.Fprintf(out, "  %s\n", line)
	}
	if webhookURL := stringValue(added.IncomingWebhookUrl); webhookURL != "" {
		fmt.Fprintf(out, "\nIncoming webhook URL:\n%s\n", webhookURL)
		fmt.Fprintf(automationProgress(cmd), "\nThis is the only time Revyl shows this URL: it holds a secret token, so keep it like a password. Each POST to it starts a run.\nIf it leaks, replace it with: revyl automation trigger rotate-incoming-webhook-url %s\n", added.Id)
	}
	return nil
}

func readAutomationTrigger(cmd *cobra.Command, client *api.Client, reference string) (*api.AutomationTriggerResponse, error) {
	triggerID, err := parseAutomationRecordID("trigger", reference)
	if err != nil {
		return nil, err
	}
	trigger, err := client.GetAutomationTrigger(cmd.Context(), triggerID)
	if isAutomationResourceNotFound(err) {
		return nil, automationTriggerNotFound(triggerID)
	}
	if err != nil {
		return nil, automationRequestError(cmd, automationTriggerAnalyticsDomain, "read trigger", err)
	}
	return trigger, nil
}

func printAutomationTrigger(cmd *cobra.Command, trigger *api.AutomationTriggerResponse, automationName string) {
	table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(table, "Trigger\t%s\n", trigger.Id)
	fmt.Fprintf(table, "Type\t%s\n", trigger.TriggerType)
	if automationName != "" {
		fmt.Fprintf(table, "Automation\t%s (%s)\n", automationName, trigger.AutomationId)
	} else {
		fmt.Fprintf(table, "Automation\t%s\n", trigger.AutomationId)
	}
	for index, line := range describeAutomationTrigger(*trigger) {
		label := ""
		if index == 0 {
			label = "Config"
		}
		fmt.Fprintf(table, "%s\t%s\n", label, line)
	}
	fmt.Fprintf(table, "Added\t%s\n", formatAutomationTime(trigger.CreatedAt))
	_ = table.Flush()
}

func runAutomationTriggerGet(cmd *cobra.Command, args []string) error {
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	trigger, err := readAutomationTrigger(cmd, client, args[0])
	if err != nil {
		return err
	}
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, trigger)
	}
	automation, err := client.GetAutomation(cmd.Context(), trigger.AutomationId.String())
	if err != nil {
		return automationRequestError(cmd, automationTriggerAnalyticsDomain, "read the trigger's automation", err)
	}
	printAutomationTrigger(cmd, trigger, automation.Name)
	return nil
}

func runAutomationTriggerUpdate(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	filters, err := changedPullRequestFilters(cmd)
	if err != nil {
		return err
	}
	removedFilters, _ := flags.GetStringArray("remove-filter")
	scheduleFlagsPassed := flags.Changed("cron") || flags.Changed("timezone")
	if !scheduleFlagsPassed && len(filters) == 0 && len(removedFilters) == 0 {
		return usageErrorf("nothing to change: pass --cron or --timezone for a schedule trigger, or --pull-request-* or --remove-filter for a GitHub pull request trigger")
	}

	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	trigger, err := readAutomationTrigger(cmd, client, args[0])
	if err != nil {
		return err
	}

	var config api.AutomationTriggerResponse_TriggerConfig
	switch {
	case trigger.TriggerType == api.AutomationTriggerResponseTriggerTypeSchedule:
		if len(filters) > 0 || len(removedFilters) > 0 {
			return usageErrorf("trigger %s is a schedule: change it with --cron and --timezone", trigger.Id)
		}
		schedule, err := trigger.TriggerConfig.AsScheduleTriggerConfig()
		if err != nil {
			return fmt.Errorf("read schedule trigger %s: %w", trigger.Id, err)
		}
		if flags.Changed("cron") {
			schedule.CronExpression = strings.TrimSpace(automationStringFlag(cmd, "cron"))
		}
		if flags.Changed("timezone") {
			schedule.IanaTimezone = strings.TrimSpace(automationStringFlag(cmd, "timezone"))
		}
		err = config.FromScheduleTriggerConfig(schedule)
		if err != nil {
			return fmt.Errorf("build trigger config: %w", err)
		}
	case isPullRequestTriggerType(string(trigger.TriggerType)):
		if scheduleFlagsPassed {
			return usageErrorf("trigger %s is a GitHub pull request trigger: change it with the --pull-request-* flags and --remove-filter", trigger.Id)
		}
		current, err := trigger.TriggerConfig.AsGithubPullRequestTriggerConfig()
		if err != nil {
			return fmt.Errorf("read pull request trigger %s: %w", trigger.Id, err)
		}
		updated, err := mergePullRequestFilters(current, filters, removedFilters)
		if err != nil {
			return err
		}
		if err := config.FromGithubPullRequestTriggerConfig(updated); err != nil {
			return fmt.Errorf("build trigger config: %w", err)
		}
	case trigger.TriggerType == api.AutomationTriggerResponseTriggerTypeIncomingWebhook:
		return usageErrorf("an incoming webhook trigger has nothing to change; to replace its URL, run 'revyl automation trigger rotate-incoming-webhook-url %s'", trigger.Id)
	case trigger.TriggerType == api.AutomationTriggerResponseTriggerTypeManual:
		return usageErrorf("the manual trigger has nothing to change")
	default:
		return usageErrorf("this CLI can't change a %s trigger: run 'revyl upgrade'", trigger.TriggerType)
	}

	encodedConfig, err := config.MarshalJSON()
	if err != nil {
		return fmt.Errorf("build trigger config: %w", err)
	}
	var request api.AutomationTriggerConfigUpdateRequest
	if err := json.Unmarshal(encodedConfig, &request.TriggerConfig); err != nil {
		return fmt.Errorf("build trigger config: %w", err)
	}
	updated, err := client.UpdateAutomationTrigger(cmd.Context(), trigger.Id.String(), request)
	if err != nil {
		return automationRequestError(cmd, automationTriggerAnalyticsDomain, "update trigger", err)
	}
	recordAutomationOutcome(cmd, automationTriggerAnalyticsDomain, "updated", "")
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, updated)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated %s trigger %s.\n", updated.TriggerType, updated.Id)
	for _, line := range describeAutomationTrigger(*updated) {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", line)
	}
	return nil
}

// mergePullRequestFilters replaces each filter a flag set, drops each one
// --remove-filter names, appends the rest, and keeps every other filter as it
// is, including types this CLI predates.
func mergePullRequestFilters(current api.GithubPullRequestTriggerConfig, replaced map[string][]string, removed []string) (api.GithubPullRequestTriggerConfig, error) {
	removedTypes := map[string]bool{}
	var existing []api.GithubPullRequestTriggerConfig_Filters_Item
	if current.Filters != nil {
		existing = *current.Filters
	}
	for _, filterType := range removed {
		filterType = strings.TrimSpace(filterType)
		if _, alsoReplaced := replaced[filterType]; alsoReplaced {
			return current, usageErrorf("--remove-filter %s drops the filter its own flag sets: pass one or the other", filterType)
		}
		onTrigger := slices.ContainsFunc(existing, func(item api.GithubPullRequestTriggerConfig_Filters_Item) bool {
			itemType, _ := item.Discriminator()
			return itemType == filterType
		})
		knownType := slices.ContainsFunc(pullRequestFilterFlags, func(filter pullRequestFilterFlag) bool { return filter.filterType == filterType })
		if !onTrigger && !knownType {
			return current, usageErrorf("--remove-filter %q isn't a pull request filter type: use pull_request_repository_is_any_of, pull_request_base_branch_is_any_of, pull_request_author_is_any_of or pull_request_has_any_label", filterType)
		}
		removedTypes[filterType] = true
	}

	merged := make([]api.GithubPullRequestTriggerConfig_Filters_Item, 0, len(existing)+len(replaced))
	placed := map[string]bool{}
	for _, item := range existing {
		itemType, _ := item.Discriminator()
		if removedTypes[itemType] {
			continue
		}
		if values, ok := replaced[itemType]; ok {
			replacement, err := newPullRequestFilter(itemType, values)
			if err != nil {
				return current, err
			}
			merged = append(merged, replacement)
			placed[itemType] = true
			continue
		}
		merged = append(merged, item)
	}
	for _, filter := range pullRequestFilterFlags {
		values, ok := replaced[filter.filterType]
		if !ok || placed[filter.filterType] {
			continue
		}
		item, err := newPullRequestFilter(filter.filterType, values)
		if err != nil {
			return current, err
		}
		merged = append(merged, item)
	}
	current.Filters = &merged
	return current, nil
}

func runAutomationTriggerRemove(cmd *cobra.Command, args []string) error {
	triggerID, err := parseAutomationRecordID("trigger", args[0])
	if err != nil {
		return err
	}
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	err = client.RemoveAutomationTrigger(cmd.Context(), triggerID)
	if isAutomationResourceNotFound(err) {
		return automationTriggerNotFound(triggerID)
	}
	if err != nil {
		return automationRequestError(cmd, automationTriggerAnalyticsDomain, "remove trigger", err)
	}
	recordAutomationOutcome(cmd, automationTriggerAnalyticsDomain, "removed", "")
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, automationTriggerRemovedOutput{ID: triggerID, Removed: true})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed trigger %s. Its past runs keep their trigger type, and any incoming webhook URL it had no longer works.\n", triggerID)
	return nil
}

// automationTriggerRemovedOutput is the --json output of trigger remove, which
// has no object left to print.
type automationTriggerRemovedOutput struct {
	ID      string `json:"id"`
	Removed bool   `json:"removed"`
}

func runAutomationTriggerRotateIncomingWebhookURL(cmd *cobra.Command, args []string) error {
	triggerID, err := parseAutomationRecordID("trigger", args[0])
	if err != nil {
		return err
	}
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	rotated, err := client.RotateAutomationIncomingWebhookURL(cmd.Context(), triggerID)
	if isAutomationResourceNotFound(err) {
		return automationTriggerNotFound(triggerID)
	}
	if err != nil {
		return automationRequestError(cmd, automationTriggerAnalyticsDomain, "rotate incoming webhook URL", err)
	}
	recordAutomationOutcome(cmd, automationTriggerAnalyticsDomain, "incoming_webhook_url_rotated", "")
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, rotated)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Trigger %s has a new incoming webhook URL, and its old one stopped working:\n%s\n", rotated.Id, stringValue(rotated.IncomingWebhookUrl))
	fmt.Fprintln(automationProgress(cmd), "\nThis is the only time Revyl shows this URL: it holds a secret token, so keep it like a password.")
	return nil
}

func newAutomationTriggerTypesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "trigger-types",
		Short: "List every trigger type and whether your organization can use it",
		Long: `List every trigger type with its config fields and filter types, whether your
organization can use it, and what an unavailable one needs first (the GitHub
types need a GitHub connection).

With --json, stdout is the catalog as Revyl serves it, each type's config as a
JSON Schema.`,
		Example: `  revyl automation trigger-types
  revyl automation trigger-types --json`,
		Args: cobra.NoArgs,
		RunE: runAutomationTriggerTypes,
	}
}

func runAutomationTriggerTypes(cmd *cobra.Command, _ []string) error {
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	triggerTypes, err := client.ListAutomationTriggerTypes(cmd.Context())
	if err != nil {
		return automationRequestError(cmd, automationTriggerAnalyticsDomain, "list trigger types", err)
	}
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, triggerTypes)
	}
	rows := make([][]string, 0, len(triggerTypes))
	for _, triggerType := range triggerTypes {
		available := "yes"
		if !triggerType.IsAvailable {
			available = "no"
			if reason := stringValue(triggerType.UnavailableReason); reason != "" {
				available += ": " + reason
			}
		}
		rows = append(rows, []string{string(triggerType.TriggerType), available, describeJSONSchemaFields(triggerType.TriggerConfigJsonSchema)})
	}
	printAutomationTable(cmd.OutOrStdout(), []string{"TRIGGER TYPE", "AVAILABLE", "CONFIG"}, rows)
	return nil
}

func automationStringFlag(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}
