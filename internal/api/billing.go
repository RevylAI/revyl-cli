package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/revyl/cli/internal/config"
)

type BillingAction string

const (
	BillingActionAddCard       BillingAction = "add_card"
	BillingActionRaiseLimit    BillingAction = "raise_limit"
	BillingActionRequestAccess BillingAction = "request_access"
	BillingActionUpdateCard    BillingAction = "update_card"
	BillingActionTopUp         BillingAction = "top_up"
	BillingActionContactSales  BillingAction = "contact_sales"
)

type UsageNotice struct {
	Level                   string           `json:"level"`
	ThresholdPct            *int             `json:"threshold_pct,omitempty"`
	UsedMinutesIOS          *float64         `json:"used_minutes_ios,omitempty"`
	UsedMinutesAndroid      *float64         `json:"used_minutes_android,omitempty"`
	FreeMinutesIOS          *float64         `json:"free_minutes_ios,omitempty"`
	FreeMinutesAndroid      *float64         `json:"free_minutes_android,omitempty"`
	RateUSDPerMinuteIOS     *float64         `json:"rate_usd_per_minute_ios,omitempty"`
	RateUSDPerMinuteAndroid *float64         `json:"rate_usd_per_minute_android,omitempty"`
	BillingURL              string           `json:"billing_url"`
	Message                 string           `json:"message"`
	UpgradeBenefits         *UpgradeBenefits `json:"upgrade_benefits,omitempty"`
}

type BillingRefusal struct {
	Message         string
	BillingURL      string
	BillingAction   BillingAction
	UpgradeBenefits *UpgradeBenefits
	// Reported marks that the command took ownership of reporting the refusal as JSON.
	Reported bool
}

func (r *BillingRefusal) Error() string { return r.Message }

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

const AddCardCharge = "$20 credit, charged when you add a card"

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

func BillingSettingsURL(devMode bool) string {
	return config.GetAppURL(devMode) + "/settings/billing"
}

func AddCardURL(devMode bool, organizationID string) string {
	return billingDialogURL(devMode, "add_card", organizationID)
}

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

const EnterpriseContactURL = "https://cal.com/landseer-enga/book-a-demo"

type AutoTopUp struct {
	Enabled      bool    `json:"enabled"`
	AmountUSD    float64 `json:"amount_usd"`
	ThresholdUSD float64 `json:"threshold_usd"`
	StepsUp      *bool   `json:"steps_up,omitempty"`
}

type BillingPlanStatus struct {
	PlanInfo
	AutoTopUp         *AutoTopUp `json:"-"`
	PrepaidBalanceUSD *float64   `json:"-"`
}

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

func (c *Client) BillingCredits(ctx context.Context) (*CreditsBalanceResponse, error) {
	return getBillingJSON[CreditsBalanceResponse](ctx, c, "/api/v1/execution/billing/credits")
}

// BillingSummary returns the organization's billed usage for the current UTC calendar month.
func (c *Client) BillingSummary(ctx context.Context) (*BillingSummaryResponse, error) {
	return getBillingJSON[BillingSummaryResponse](ctx, c, "/api/v1/execution/billing/summary")
}

// BillingRates returns the per-minute device rate card for the organization (including offered Pro rates on Free plans).
func (c *Client) BillingRates(ctx context.Context) (*RateCardResponse, error) {
	return getBillingJSON[RateCardResponse](ctx, c, "/api/v1/execution/billing/rates")
}

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
