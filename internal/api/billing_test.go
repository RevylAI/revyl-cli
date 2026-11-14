package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testBillingURL = "https://app.example.test/settings/billing?add_card=1"

func TestPaymentRequiredWithBillingLinkNamesTheStepOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action BillingAction
		detail string
		want   string
	}{
		{"add card, link in detail", BillingActionAddCard, "Free minutes used up for this month.\\nUpgrade to Pro to keep running ($20 credit, charged when you add a card): " + testBillingURL, ""},
		{"add card", BillingActionAddCard, "Billing access is required.", "Upgrade to Pro to keep running ($20 credit, charged when you add a card): " + testBillingURL},
		{"top up", BillingActionTopUp, "Billing access is required.", "Top up: " + testBillingURL},
		{"update card", BillingActionUpdateCard, "Billing access is required.", "Update card: " + testBillingURL},
		{"raise limit", BillingActionRaiseLimit, "Billing access is required.", "Raise the limit: " + testBillingURL},
		{"request access", BillingActionRequestAccess, "Billing access is required.", "Send this link to an owner or admin: " + testBillingURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"detail":` + quoteJSON(tc.detail) + `,"code":"billing_denied","billing_url":"` + testBillingURL + `","billing_action":"` + string(tc.action) + `"}`

			apiErr := parseAPIErrorBody(http.StatusPaymentRequired, []byte(body))

			if apiErr.BillingURL != testBillingURL || apiErr.BillingAction != tc.action {
				t.Fatalf("billing fields = %q %q", apiErr.BillingURL, apiErr.BillingAction)
			}
			if apiErr.Hint != tc.want {
				t.Fatalf("hint = %q, want %q", apiErr.Hint, tc.want)
			}
			message := apiErr.Error()
			if strings.Count(message, testBillingURL) != 1 {
				t.Fatalf("error = %q, want the link exactly once", message)
			}
			if strings.Contains(message, "revyl ") {
				t.Fatalf("error = %q, want a link and no CLI command", message)
			}
		})
	}
}

func TestStartDeviceCarriesBillingNoticeAndConcurrencyLink(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		wantError string
	}{
		{
			name:      "notice on an accepted start",
			body:      `{"status":"initializing","platform":"ios","billing_notice":{"level":"warning","threshold_pct":80,"used_minutes_ios":480,"free_minutes_ios":600,"rate_usd_per_minute_ios":0.15,"billing_url":"` + testBillingURL + `","message":"80% of this month's free minutes used (480 of 600 iOS min)."}}`,
			wantError: "",
		},
		{
			name:      "concurrency message already links",
			body:      `{"status":"cancelled","platform":"ios","error":"All 2 devices are busy.\nUpgrade to Pro to run 10 at once ($20 credit, charged when you add a card): ` + testBillingURL + `","billing_url":"` + testBillingURL + `","billing_action":"add_card","upgrade_benefits":{"concurrent_devices_now":2,"concurrent_devices_payg":10}}`,
			wantError: "All 2 devices are busy.\nUpgrade to Pro to run 10 at once ($20 credit, charged when you add a card): " + testBillingURL,
		},
		{
			name:      "concurrency message without the link",
			body:      `{"status":"cancelled","platform":"ios","error":"Concurrency limit reached.","billing_url":"` + testBillingURL + `"}`,
			wantError: "Concurrency limit reached.\nUpgrade to Pro to run more devices at once ($20 credit, charged when you add a card): " + testBillingURL,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/execution/start_device" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)

			result, err := NewClientWithBaseURL("test-key", server.URL).StartDevice(context.Background(), &StartDeviceRequest{Platform: "ios"})

			if err != nil {
				t.Fatal(err)
			}
			if tc.wantError == "" {
				notice := result.UsageNotice
				if notice == nil || *notice.ThresholdPct != 80 || *notice.UsedMinutesIOS != 480 || notice.UsedMinutesAndroid != nil || notice.BillingURL != testBillingURL {
					t.Fatalf("billing notice = %+v", notice)
				}
				return
			}
			if result.Error == nil || *result.Error != tc.wantError {
				t.Fatalf("error = %v, want %q", result.Error, tc.wantError)
			}
			if result.BillingURL != testBillingURL {
				t.Fatalf("billing url = %q", result.BillingURL)
			}
			if strings.Contains(tc.body, "upgrade_benefits") && (result.BillingAction != BillingActionAddCard || result.UpgradeBenefits == nil || result.UpgradeBenefits.ConcurrentDevicesNow != 2 || result.UpgradeBenefits.ConcurrentDevicesPayg != 10) {
				t.Fatalf("refusal = %q %+v", result.BillingAction, result.UpgradeBenefits)
			}
		})
	}
}

func TestBillingPlanInfoReadsPrepaidFieldsWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		wantPrepaid bool
	}{
		{"card added", `{"plan":"free","display_name":"Free","monthly_base":0,"free_credit_label":"x","prepaid_balance_usd":12.5,"auto_topup":{"enabled":true,"amount_usd":20,"threshold_usd":5}}`, true},
		{"older backend", `{"plan":"free","display_name":"Free","monthly_base":0,"free_credit_label":"x"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)

			plan, err := NewClientWithBaseURL("test-key", server.URL).BillingPlanInfo(context.Background())

			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantPrepaid {
				if plan.PrepaidBalanceUSD != nil || plan.AutoTopUp != nil {
					t.Fatalf("prepaid fields = %v %v, want none", plan.PrepaidBalanceUSD, plan.AutoTopUp)
				}
				return
			}
			if plan.PrepaidBalanceUSD == nil || *plan.PrepaidBalanceUSD != 12.5 || plan.AutoTopUp == nil || !plan.AutoTopUp.Enabled || plan.AutoTopUp.AmountUSD != 20 || plan.AutoTopUp.ThresholdUSD != 5 {
				t.Fatalf("prepaid fields = %v %+v", plan.PrepaidBalanceUSD, plan.AutoTopUp)
			}
		})
	}
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func TestPaymentRequiredCarriesUpgradeBenefits(t *testing.T) {
	body := `{"detail":"Free minutes used up for this month.\nUpgrade to Pro to keep running, with 10 devices at once instead of 2 ($20 credit, charged when you add a card): ` + testBillingURL + `","billing_url":"` + testBillingURL + `","billing_action":"add_card","upgrade_benefits":{"concurrent_devices_now":2,"concurrent_devices_payg":10}}`

	refusal, ok := AsBillingRefusal(parseAPIErrorBody(http.StatusPaymentRequired, []byte(body)))

	if !ok || refusal.BillingAction != BillingActionAddCard || refusal.UpgradeBenefits == nil || *refusal.UpgradeBenefits != (UpgradeBenefits{ConcurrentDevicesNow: 2, ConcurrentDevicesPayg: 10}) {
		t.Fatalf("refusal = %+v", refusal)
	}
	if strings.Count(refusal.Message, testBillingURL) != 1 {
		t.Fatalf("message = %q, want the link once", refusal.Message)
	}
}
