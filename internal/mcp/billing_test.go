package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestGetBillingStatusComposesPlanAndPrepaidFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/execution/billing/plan":
			_, _ = w.Write([]byte(`{"plan":"free","display_name":"Free","monthly_base":0,"free_credit_label":"x","payg_enrolled":true,"prepaid_balance_usd":0,"auto_topup":{"enabled":false,"amount_usd":20,"threshold_usd":5}}`))
		case "/api/v1/execution/billing/usage-cap":
			_, _ = w.Write([]byte(`{"monthly_cap_usd":25,"spend_this_period_usd":25}`))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(server.Close)
	srv := &Server{apiClient: api.NewClientWithBaseURL("test-key", server.URL)}

	_, status, err := srv.handleGetBillingStatus(context.Background(), nil, GetBillingStatusInput{})

	if err != nil {
		t.Fatal(err)
	}
	if !status.PaygEnrolled || status.PrepaidBalanceUSD == nil || *status.PrepaidBalanceUSD != 0 || status.AutoRecharge == nil || status.AutoRecharge.Enabled {
		t.Fatalf("prepaid state = %+v %+v", status.PrepaidBalanceUSD, status.AutoRecharge)
	}
	if status.SpendingLimitUSD == nil || *status.SpendingLimitUSD != 25 {
		t.Fatalf("spending limit = %v", status.SpendingLimitUSD)
	}
	if strings.Join(status.Unavailable, ",") != "credits,summary,rates" {
		t.Fatalf("unavailable = %v", status.Unavailable)
	}
	if status.SessionsThisPeriod != nil || status.BillingURL == "" || status.AddCardURL != "" {
		t.Fatalf("status = %+v", status)
	}
}

func TestStartDeviceSessionBillingDenialGivesTheLinkAndNoCommand(t *testing.T) {
	const billingURL = "https://app.example.test/settings/billing"
	for _, action := range []api.BillingAction{api.BillingActionUpdateCard, api.BillingActionTopUp, api.BillingActionAddCard} {
		t.Run(string(action), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/execution/start_device" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = w.Write([]byte(`{"detail":"Billing action is required.","code":"billing_denied","billing_url":"` + billingURL + `","billing_action":"` + string(action) + `"}`))
			}))
			t.Cleanup(server.Close)
			srv := &Server{sessionMgr: NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())}

			_, output, err := srv.handleStartDeviceSession(context.Background(), nil, StartDeviceSessionInput{
				Platform: "ios", NoOpen: true, DisableInheritedLaunchVars: true,
			})

			if err != nil {
				t.Fatal(err)
			}
			if output.Success || !strings.Contains(output.Error, api.BillingNextStep(action, billingURL)) {
				t.Fatalf("error = %q, want the %s step with its link", output.Error, action)
			}
			if strings.Contains(output.Error, "revyl ") {
				t.Fatalf("error = %q tells the agent to run a CLI command", output.Error)
			}
		})
	}
}

func TestStartDeviceSessionConcurrencyRefusalPassesUpgradeBenefits(t *testing.T) {
	const message = "All 2 devices are busy.\nUpgrade to Pro to run 10 at once ($20 credit, charged when you add a card): https://app.example.test/settings/billing?add_card=1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/execution/start_device" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"cancelled","platform":"ios","retry_allowed":true,"error":` + strconv.Quote(message) + `,"billing_url":"https://app.example.test/settings/billing?add_card=1","billing_action":"add_card","upgrade_benefits":{"concurrent_devices_now":2,"concurrent_devices_payg":10}}`))
	}))
	t.Cleanup(server.Close)
	srv := &Server{sessionMgr: NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())}

	_, output, err := srv.handleStartDeviceSession(context.Background(), nil, StartDeviceSessionInput{
		Platform: "ios", NoOpen: true, DisableInheritedLaunchVars: true,
	})

	if err != nil {
		t.Fatal(err)
	}
	if output.Success || !strings.HasSuffix(output.Error, message) || strings.Count(output.Error, "https://") != 1 {
		t.Fatalf("error = %q, want the backend's message once", output.Error)
	}
	if output.BillingAction != api.BillingActionAddCard || output.BillingURL == "" || output.UpgradeBenefits == nil || *output.UpgradeBenefits != (api.UpgradeBenefits{ConcurrentDevicesNow: 2, ConcurrentDevicesPayg: 10}) {
		t.Fatalf("billing fields = %q %q %+v", output.BillingURL, output.BillingAction, output.UpgradeBenefits)
	}
}

func TestStartDeviceSessionReturnsTheQueueNoticeOfAStartThatWaited(t *testing.T) {
	const message = "Queued: all 2 devices are busy. This starts when one is free.\nUpgrade to Pro to run 10 at once ($20 credit, charged when you add a card): https://app.example.test/settings/billing?add_card=1"
	for _, tc := range []struct {
		name        string
		queueNotice string
		wantStep    string
	}{
		{"queued", `,"queue_notice":{"devices":2,"action":"add_card","billing_url":"https://app.example.test/settings/billing?add_card=1","message":` + strconv.Quote(message) + `}`, "Agents: this run starts when a device is free. If the task needs more in parallel, recommend upgrading to Pro, which raises devices at once, and share this notice's price and link. Do not add a card yourself."},
		{"not queued", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newStartSessionIDServer(t, `{"workflow_run_id":"`+startIDWorkflowRunID+`","session_id":"`+startIDSessionID+`"`+tc.queueNotice+`}`, &startIDCallCounts{})
			t.Cleanup(server.Close)
			srv := &Server{sessionMgr: NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())}

			_, output, err := srv.handleStartDeviceSession(context.Background(), nil, StartDeviceSessionInput{
				Platform: "ios", NoOpen: true, DisableInheritedLaunchVars: true,
			})

			if err != nil || !output.Success {
				t.Fatalf("start failed: %+v %v", output, err)
			}
			srv.sessionMgr.StopIdleTimer(output.SessionIndex)
			if tc.wantStep == "" {
				if output.QueueNotice != nil {
					t.Fatalf("queue_notice = %+v for a start that did not queue", output.QueueNotice)
				}
				return
			}
			if output.QueueNotice == nil || output.QueueNotice.Message != message || output.QueueNotice.Devices != 2 || output.QueueNotice.Action != api.BillingActionAddCard || output.QueueNotice.NextStep != tc.wantStep {
				t.Fatalf("queue_notice = %+v", output.QueueNotice)
			}
		})
	}
}

func TestStartDeviceSessionRefusalNamesTheAgentsNextStepPerAction(t *testing.T) {
	for action, want := range map[api.BillingAction]string{
		api.BillingActionAddCard:    "Agents: stop. Recommend upgrading to Pro. It keeps this work running and raises devices at once from 2 to 10. Share the link and the price. Do not retry, work around the limit, or add a card yourself.",
		api.BillingActionUpdateCard: "Agents: stop. Tell the person to update the card at the link. Do not retry or work around it.",
	} {
		t.Run(string(action), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"cancelled","platform":"ios","error":"All 2 devices are busy.","billing_url":"https://app.example.test/settings/billing","billing_action":"` + string(action) + `","upgrade_benefits":{"concurrent_devices_now":2,"concurrent_devices_payg":10}}`))
			}))
			t.Cleanup(server.Close)
			srv := &Server{sessionMgr: NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())}

			_, output, err := srv.handleStartDeviceSession(context.Background(), nil, StartDeviceSessionInput{
				Platform: "ios", NoOpen: true, DisableInheritedLaunchVars: true,
			})

			if err != nil || output.Success || output.BillingNextStep != want {
				t.Fatalf("billing_next_step = %q (err %v), want %q", output.BillingNextStep, err, want)
			}
		})
	}
}
