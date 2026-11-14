package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/billingstatus"
	"github.com/revyl/cli/internal/ui"
)

var billingOpenBrowserFn = ui.OpenBrowser

var billingCmd = &cobra.Command{
	Use:   "billing",
	Short: "See usage and upgrade to Pro",
	Long: `See your plan, free minutes, usage and rates, or upgrade to Pro.

EXAMPLES:
  revyl billing status
  revyl billing status --json
  revyl billing add-card
  revyl billing add-card --no-open --json`,
}

var billingStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show plan, card, free minutes, usage, limits and rates",
	Args:  cobra.NoArgs,
	RunE:  runBillingStatus,
}

var billingAddCardCmd = &cobra.Command{
	Use:   "add-card",
	Short: "Upgrade to Pro by adding a card in Billing",
	Long: `Open the add-card page in your browser. Adding a card upgrades you to Pro.

Pro keeps you running after the month's free minutes, on 10 devices at once.
'revyl billing status' shows how many your organization runs now.

  No monthly fee
  $20 credit, charged when you add a card
  Auto-recharge $20 when credit drops under $5
  Steps up as you use more. Set your own amount any time.
  No monthly limit unless you set one

Use --no-open to print the link and the per-minute rates instead.`,
	Args: cobra.NoArgs,
	RunE: runBillingAddCard,
}

func init() {
	billingAddCardCmd.Flags().Bool("no-open", false, "Print the link without opening a browser")
	billingCmd.AddCommand(billingStatusCmd)
	billingCmd.AddCommand(billingAddCardCmd)
}

func runBillingStatus(cmd *cobra.Command, _ []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	devMode, _ := cmd.Flags().GetBool("dev")
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
	defer cancel()
	status, err := billingstatus.Compose(billingstatus.Read(ctx, api.NewClientWithDevMode(apiKey, devMode)), devMode)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeIndentedJSON(cmd.OutOrStdout(), status)
	}
	billingstatus.Write(cmd.OutOrStdout(), status)
	return nil
}

// billingAddCardOutput is the stable --json contract of revyl billing add-card.
type billingAddCardOutput struct {
	URL             string               `json:"url"`
	Opened          bool                 `json:"opened"`
	Message         string               `json:"message"`
	UpgradeBenefits *api.UpgradeBenefits `json:"upgrade_benefits,omitempty"`
}

func runBillingAddCard(cmd *cobra.Command, _ []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	devMode, _ := cmd.Flags().GetBool("dev")
	noOpen, _ := cmd.Flags().GetBool("no-open")
	output := composeBillingAddCard(cmd.Context(), devMode)
	if !noOpen {
		output.Opened = billingOpenBrowserFn(output.URL) == nil
	}
	if jsonOutput {
		return writeIndentedJSON(cmd.OutOrStdout(), output)
	}
	if output.Opened {
		fmt.Fprintf(cmd.ErrOrStderr(), "Opened Billing in your browser.\n%s\n", output.Message)
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), output.Message)
	return nil
}

// composeBillingAddCard offers Pro only to an organization the backend says
// can upgrade. An organization already on Pro, one on another plan, and one
// whose plan cannot be read get a billing link without the offer.
func composeBillingAddCard(ctx context.Context, devMode bool) billingAddCardOutput {
	plan, organizationID := readBillingPlan(ctx, devMode)
	if plan == nil {
		url := api.AddCardURL(devMode, organizationID)
		return billingAddCardOutput{URL: url, Message: "Add a card in Billing: " + url}
	}
	if !billingstatus.CanUpgradeToPro(*plan) {
		url := api.BillingSettingsURL(devMode)
		message := "Pro isn't available for this organization. Billing settings: " + url
		if plan.PaygEnrolled != nil && *plan.PaygEnrolled {
			message = "Already on Pro. Billing settings: " + url
		}
		return billingAddCardOutput{URL: url, Message: message}
	}
	output := billingAddCardOutput{URL: api.AddCardURL(devMode, organizationID), UpgradeBenefits: billingstatus.UpgradeBenefits(*plan)}
	var devicesNow, devicesPayg *int
	if benefits := output.UpgradeBenefits; benefits != nil {
		devicesNow, devicesPayg = &benefits.ConcurrentDevicesNow, &benefits.ConcurrentDevicesPayg
	}
	output.Message = billingstatus.AddCardLine(devicesNow, devicesPayg, output.URL) + "\n" + billingstatus.PaygPrices(readDeviceRates(ctx))
	return output
}

// readBillingPlan reads the organization's plan and the organization the CLI
// acts for when the CLI is signed in: a nil plan when it is not or the plan
// read fails, and an empty organization when only that read fails.
func readBillingPlan(ctx context.Context, devMode bool) (*api.PlanInfo, string) {
	token, err := auth.NewManager().GetActiveToken()
	if err != nil || token == "" {
		return nil, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := api.NewClientWithDevMode(token, devMode)
	plan, err := client.BillingPlanInfo(ctx)
	if err != nil {
		return nil, ""
	}
	identity, err := client.ValidateAPIKey(ctx)
	if err != nil {
		return &plan.PlanInfo, ""
	}
	return &plan.PlanInfo, identity.OrgID
}

func writeIndentedJSON(w io.Writer, value interface{}) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// printBillingNotice shows a usage notice from a work-start response on
// stderr, also in --json mode, where agents read stdout's JSON but often skip
// its billing_notice field.
func printBillingNotice(notice *api.UsageNotice) {
	if notice.Text() == "" {
		return
	}
	ui.PrintWarning("%s\n%s", notice.Text(), billingstatus.UsageNoticeGuidance)
}

// printQueueNotice shows a queued start's notice on stderr, also in --json
// mode, as soon as the start is accepted: the wait that follows is the queue.
func printQueueNotice(notice *billingstatus.QueueNotice) {
	if notice == nil {
		return
	}
	ui.PrintWarning("%s", notice.Text())
}

// addBillingFields adds a refused start's billing link, step and upgrade
// benefits, and an accepted start's usage notice, to a command's JSON output.
func addBillingFields(output map[string]interface{}, billingURL string, action api.BillingAction, benefits *api.UpgradeBenefits, notice *api.UsageNotice) {
	if billingURL != "" {
		output["billing_url"] = billingURL
		output["billing_action"] = action
		output["next_step"] = billingRefusalGuidance(context.Background(), action, benefits)
	}
	if benefits != nil {
		output["upgrade_benefits"] = benefits
	}
	if notice != nil {
		output["billing_notice"] = notice
	}
}

// printBillingRefusalGuidance follows the error line of the billing refusal
// in err with what Pro costs and what an agent must do. In --json mode
// next_step carries it.
func printBillingRefusalGuidance(w io.Writer, cmd *cobra.Command, err error) {
	if cmd == nil || err == nil {
		return
	}
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		return
	}
	refusal, ok := api.AsBillingRefusal(err)
	if !ok {
		return
	}
	fmt.Fprintf(w, "\n%s\n", billingRefusalGuidance(cmd.Context(), refusal.BillingAction, refusal.UpgradeBenefits))
}

// billingRefusalGuidance prices the Pro offer at the plan's device rates.
func billingRefusalGuidance(ctx context.Context, action api.BillingAction, benefits *api.UpgradeBenefits) string {
	var rates []api.RateCardItem
	if action == api.BillingActionAddCard {
		rates = readDeviceRates(ctx)
	}
	return billingstatus.RefusalGuidance(action, rates, benefits)
}

// readDeviceRates reads the plan's device rates when the CLI is signed in;
// nil when it is not or the read fails, since the copy stays usable without
// them.
func readDeviceRates(ctx context.Context) []api.RateCardItem {
	token, err := auth.NewManager().GetActiveToken()
	if err != nil || token == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	card, err := api.NewClientWithDevMode(token, false).BillingRates(ctx)
	if err != nil {
		return nil
	}
	return card.Rates
}

// billingErrorJSON is the --json stdout payload of a command a billing
// refusal stopped before it wrote its own output.
type billingErrorJSON struct {
	Error           string               `json:"error"`
	StatusCode      int                  `json:"status_code,omitempty"`
	Code            string               `json:"code,omitempty"`
	BillingURL      string               `json:"billing_url"`
	BillingAction   api.BillingAction    `json:"billing_action,omitempty"`
	UpgradeBenefits *api.UpgradeBenefits `json:"upgrade_benefits,omitempty"`
	NextStep        string               `json:"next_step"`
}

// writeBillingErrorJSON writes the billing refusal in err to stdout when the
// command ran with --json, so agents parsing stdout get the link.
func writeBillingErrorJSON(w io.Writer, cmd *cobra.Command, err error) {
	if cmd == nil || err == nil {
		return
	}
	if jsonOutput, _ := cmd.Flags().GetBool("json"); !jsonOutput {
		return
	}
	refusal, ok := api.AsBillingRefusal(err)
	if !ok || refusal.Reported {
		return
	}
	payload := billingErrorJSON{
		Error:           refusal.Message,
		BillingURL:      refusal.BillingURL,
		BillingAction:   refusal.BillingAction,
		UpgradeBenefits: refusal.UpgradeBenefits,
		NextStep:        billingRefusalGuidance(cmd.Context(), refusal.BillingAction, refusal.UpgradeBenefits),
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		payload.StatusCode = apiErr.StatusCode
		payload.Code = apiErr.Code
	}
	_ = writeIndentedJSON(w, payload)
}
