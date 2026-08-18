package api

import (
	"context"
	"fmt"
)

const (
	cliDeviceAuthorizationsPath = "/api/v1/entity/users/cli-device-authorizations"
	cliDeviceCredentialsPath    = "/api/v1/entity/users/cli-device-authorizations/credentials"
)

// CreateCLIDeviceAuthorization registers a pending browser-approval request.
//
// The call is unauthenticated: the CLI has no credential yet. The returned
// device code is the only secret that can later redeem the minted key.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - request: Optional device identity and launcher stamped on the request.
//
// Returns:
//   - *CreateCLIDeviceAuthorizationResponse: Device code, user code, and URLs.
//   - error: Transport or API error. Error text never includes the device code.
func (c *Client) CreateCLIDeviceAuthorization(
	ctx context.Context,
	request CreateCLIDeviceAuthorizationRequest,
) (*CreateCLIDeviceAuthorizationResponse, error) {
	resp, err := c.doRequestOnce(ctx, "POST", cliDeviceAuthorizationsPath, request)
	if err != nil {
		return nil, fmt.Errorf("failed to start device authorization: %w", err)
	}

	var result CreateCLIDeviceAuthorizationResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RedeemCLIDeviceCredential polls one authorization by its device code.
//
// The call is unauthenticated and authorized by the device code alone.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - request: The device code issued when the authorization was created.
//
// Returns:
//   - *RedeemCLIDeviceCredentialResponse: Observed state, with the credential
//     on the redeeming poll.
//   - error: APIError with StatusCode 404 when the request is gone, or a
//     transport error. Error text never includes the device code.
func (c *Client) RedeemCLIDeviceCredential(
	ctx context.Context,
	request RedeemCLIDeviceCredentialRequest,
) (*RedeemCLIDeviceCredentialResponse, error) {
	resp, err := c.doRequestOnce(ctx, "POST", cliDeviceCredentialsPath, request)
	if err != nil {
		return nil, fmt.Errorf("failed to poll device authorization: %w", err)
	}

	var result RedeemCLIDeviceCredentialResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
