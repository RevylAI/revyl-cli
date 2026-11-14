package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/revyl/cli/internal/config"
)

// BillingAction names the step a billing denial asks the caller to take.
type BillingAction string

const (
	BillingActionAddCard       BillingAction = "add_card"
	BillingActionRaiseLimit    BillingAction = "raise_limit"
	BillingActionRequestAccess BillingAction = "request_access"
	BillingActionUpdateCard    BillingAction = "update_card"
	BillingActionTopUp         BillingAction = "top_up"
	BillingActionContactSales  BillingAction = "contact_sales"
)

// UsageNotice is the usage notice the backend attaches to a work-start
// response while an organization on free usage nears its monthly free minutes.
// The numbers are pointers so a value the backend did not send is passed on
// as absent rather than as zero.
type UsageNotice struct {
	Level                   string   `json:"level"`
	ThresholdPct            *int     `json:"threshold_pct,omitempty"`
	UsedMinutesIOS          *float64 `json:"used_minutes_ios,omitempty"`
	UsedMinutesAndroid      *float64 `json:"used_minutes_android,omitempty"`
	FreeMinutesIOS          *float64 `json:"free_minutes_ios,omitempty"`
	FreeMinutesAndroid      *float64 `json:"free_minutes_android,omitempty"`
	RateUSDPerMinuteIOS     *float64 `json:"rate_usd_per_minute_ios,omitempty"`
	RateUSDPerMinuteAndroid *float64 `json:"rate_usd_per_minute_android,omitempty"`
	BillingURL              string   `json:"billing_url"`
	Message                 string   `json:"message"`
	// UpgradeBenefits is what Pro adds, when the notice offers it.
	UpgradeBenefits *UpgradeBenefits `json:"upgrade_benefits,omitempty"`
}

// BillingRefusal is work the backend refused for billing, with the page and
// step that unblock it.
type BillingRefusal struct {
	Message         string
	BillingURL      string
	BillingAction   BillingAction
	UpgradeBenefits *UpgradeBenefits
	// Reported is set when the command already wrote the refusal as JSON.
	Reported bool
}

func (r *BillingRefusal) Error() string { return r.Message }

// AsBillingRefusal finds the billing refusal in err's chain: a refused start,
// or an API error that carries a billing link.
func AsBillingRefusal(err error) (*BillingRefusal, bool) {
	var refusal *BillingRefusal
	if errors.As(err, &refusal) {
		return refusal, true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.BillingURL != "" {
		return &BillingRefusal{Message: apiErr.Error(), BillingURL: apiErr.BillingURL, BillingAction: apiErr.BillingAction, UpgradeBenefits: apiErr.UpgradeBenefits, Reported: apiErr.BillingReported}, true
	}
	return nil, false
}

// Text is the notice as a person reads it: the backend's message, or only the
// link when the backend sent no message.
func (n *UsageNotice) Text() string {
	if n == nil {
		return ""
	}
	if message := strings.TrimSpace(n.Message); message != "" {
		return message
	}
	if billingURL := strings.TrimSpace(n.BillingURL); billingURL != "" {
		return "Revyl billing: " + billingURL
	}
	return ""
}

// billingResponseFields are the optional billing fields a work-start response
// or a billing denial may carry beside its documented payload.
type billingResponseFields struct {
	BillingURL      string           `json:"billing_url"`
	BillingAction   BillingAction    `json:"billing_action"`
	UsageNotice     *UsageNotice     `json:"billing_notice"`
	UpgradeBenefits *UpgradeBenefits `json:"upgrade_benefits"`
}

func readBillingResponseFields(body []byte) billingResponseFields {
	var fields billingResponseFields
	_ = json.Unmarshal(body, &fields)
	fields.BillingURL = strings.TrimSpace(fields.BillingURL)
	if fields.UsageNotice.Text() == "" {
		fields.UsageNotice = nil
	}
	return fields
}

// AddCardCharge is what upgrading to Pro charges, in the words every Pro
// offer uses.
const AddCardCharge = "$20 credit, charged when you add a card"

// BillingNextStep is the one line that tells a person what a billing denial
// needs and where to do it.
func BillingNextStep(action BillingAction, billingURL string) string {
	switch action {
	case BillingActionAddCard:
		return "Upgrade to Pro to keep running (" + AddCardCharge + "): " + billingURL
	case BillingActionRaiseLimit:
		return "Raise the limit: " + billingURL
	case BillingActionRequestAccess:
		return "Send this link to an owner or admin: " + billingURL
	case BillingActionUpdateCard:
		return "Update card: " + billingURL
	case BillingActionTopUp:
		return "Top up: " + billingURL
	case BillingActionContactSales:
		return "Need more devices at once? Talk to us: " + billingURL
	default:
		return "Review billing: " + billingURL
	}
}

// BillingSettingsURL is the app's billing settings page.
func BillingSettingsURL(devMode bool) string {
	return config.GetAppURL(devMode) + "/settings/billing"
}

// AddCardURL is the billing page that opens the add-card dialog directly, in
// the organization the CLI acts for when organizationID is known.
func AddCardURL(devMode bool, organizationID string) string {
	return billingDialogURL(devMode, "add_card", organizationID)
}

// UpdateCardURL is the billing page that opens the card update directly, in
// the organization the CLI acts for when organizationID is known.
func UpdateCardURL(devMode bool, organizationID string) string {
	return billingDialogURL(devMode, "update_card", organizationID)
}

func billingDialogURL(devMode bool, dialog string, organizationID string) string {
	query := dialog + "=1"
	if organizationID != "" {
		query += "&organization_id=" + url.QueryEscape(organizationID)
	}
	return BillingSettingsURL(devMode) + "?" + query + "&source=cli"
}

// EnterpriseContactURL is where an organization asks for more devices at once
// than Pro runs.
const EnterpriseContactURL = "https://scheduler.zoom.us/d/ijwhox6j/revyl-product-demo"

// AutoTopUp is the auto-recharge setting of an organization on Pro.
type AutoTopUp struct {
	Enabled      bool    `json:"enabled"`
	AmountUSD    float64 `json:"amount_usd"`
	ThresholdUSD float64 `json:"threshold_usd"`
	// StepsUp says the amount rises as the organization uses more; nil when
	// the backend did not say.
	StepsUp *bool `json:"steps_up,omitempty"`
}

// BillingPlanStatus is the organization's plan record with the prepaid
// amounts read at full precision, which the generated contract narrows.
type BillingPlanStatus struct {
	PlanInfo
	// AutoTopUp and PrepaidBalanceUSD are nil unless the organization pays as it goes.
	AutoTopUp         *AutoTopUp `json:"-"`
	PrepaidBalanceUSD *float64   `json:"-"`
}

// BillingPlanInfo returns the organization's full plan record.
func (c *Client) BillingPlanInfo(ctx context.Context) (*BillingPlanStatus, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/execution/billing/plan", nil)
	if err != nil {
		return nil, err
	}
	var result BillingPlanStatus
	body, err := parseResponseWithRaw(resp, &result.PlanInfo)
	if err != nil {
		return nil, err
	}
	var prepaid struct {
		AutoTopUp         *AutoTopUp `json:"auto_topup"`
		PrepaidBalanceUSD *float64   `json:"prepaid_balance_usd"`
	}
	_ = json.Unmarshal(body, &prepaid)
	result.AutoTopUp = prepaid.AutoTopUp
	result.PrepaidBalanceUSD = prepaid.PrepaidBalanceUSD
	return &result, nil
}

// BillingCredits returns the organization's credit balances.
func (c *Client) BillingCredits(ctx context.Context) (*CreditsBalanceResponse, error) {
	return getBillingJSON[CreditsBalanceResponse](ctx, c, "/api/v1/execution/billing/credits")
}

// BillingSummary returns the organization's billed usage this period.
func (c *Client) BillingSummary(ctx context.Context) (*BillingSummaryResponse, error) {
	return getBillingJSON[BillingSummaryResponse](ctx, c, "/api/v1/execution/billing/summary")
}

// BillingRates returns the per-minute device rates of the organization's plan.
func (c *Client) BillingRates(ctx context.Context) (*RateCardResponse, error) {
	return getBillingJSON[RateCardResponse](ctx, c, "/api/v1/execution/billing/rates")
}

// BillingUsageCap returns the organization's monthly spending cap.
func (c *Client) BillingUsageCap(ctx context.Context) (*UsageCap, error) {
	return getBillingJSON[UsageCap](ctx, c, "/api/v1/execution/billing/usage-cap")
}

func getBillingJSON[T any](ctx context.Context, c *Client, path string) (*T, error) {
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result T
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
