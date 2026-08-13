package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/auth"
)

// pendingFixture is a live approval a gate could offer.
func pendingFixture() *auth.DeviceAuthorization {
	return &auth.DeviceAuthorization{
		DeviceCode:              "device-secret-never-displayed",
		UserCode:                "ABCD-2345",
		VerificationURIComplete: "https://app.revyl.ai/cli/device?code=ABCD-2345",
		ExpiresIn:               600,
	}
}

// TestGateOffersTheCardOnlyWhenThereIsSomethingToApprove keeps the card from
// appearing on failures a user cannot resolve by authorizing.
func TestGateOffersTheCardOnlyWhenThereIsSomethingToApprove(t *testing.T) {
	withCard := authenticationGateResult(&devAuthenticationFailure{
		Code:          authenticationStateRequired,
		Authorization: pendingFixture(),
	})
	if !withCard.IsError {
		t.Fatal("gate result was not an error")
	}
	ui, ok := withCard.Meta["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != authAppURI {
		t.Fatalf("gate meta = %+v, want the authorization card", withCard.Meta)
	}

	withoutCard := authenticationGateResult(&devAuthenticationFailure{
		Code: authenticationStateCloudContextInvalid,
	})
	if withoutCard.Meta != nil {
		t.Fatalf("gate meta = %+v, want no card without an approval", withoutCard.Meta)
	}
}

// TestGateNeverPublishesTheDeviceCode pins the one secret in the flow.
//
// The device code is the bearer of the minted credential. It travels in poll
// request bodies only, so any appearance in a tool result, a message, or the
// card's HTML would hand redemption to whoever reads the transcript.
func TestGateNeverPublishesTheDeviceCode(t *testing.T) {
	authorization := pendingFixture()
	failure := &devAuthenticationFailure{
		Code:          authenticationStateRequired,
		Message:       authenticationFailureMessage(authenticationStateRequired, authorization),
		Authorization: authorization,
	}

	envelope, err := json.Marshal(failedAuthenticationOutcome(failure))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	published := string(envelope) + failure.Message + authAppHTML
	if strings.Contains(published, authorization.DeviceCode) {
		t.Fatal("the device code reached a surface the user can read")
	}
}

// TestGateEnvelopeCarriesTheApproval keeps the approval reachable on hosts that
// render no inline app, where the agent surfaces the URL from the envelope.
func TestGateEnvelopeCarriesTheApproval(t *testing.T) {
	authorization := pendingFixture()
	envelope := failedAuthenticationOutcome(&devAuthenticationFailure{
		Code:          authenticationStateRequired,
		Authorization: authorization,
	})
	if envelope.AuthorizationURL != authorization.VerificationURIComplete {
		t.Fatalf("envelope URL = %q, want the approval page", envelope.AuthorizationURL)
	}
	if envelope.AuthorizationCode != authorization.UserCode {
		t.Fatalf("envelope code = %q, want the confirmation code", envelope.AuthorizationCode)
	}
}

// TestPendingAuthorizationIsReusedUntilItNearsExpiry stops every blocked tool
// call from registering a new approval with a different code.
func TestPendingAuthorizationIsReusedUntilItNearsExpiry(t *testing.T) {
	pending := &pendingAuthorization{}
	if pending.current() != nil {
		t.Fatal("an empty cache returned an authorization")
	}

	authorization := pendingFixture()
	pending.store(authorization)
	if pending.current() != authorization {
		t.Fatal("a live authorization was not reused")
	}

	// One about to expire is worse than none: the user would be sent to approve
	// something that dies before they finish.
	pending.expiresAt = time.Now().Add(authorizationReuseMargin / 2)
	if pending.current() != nil {
		t.Fatal("an authorization too short-lived to approve was reused")
	}
}
