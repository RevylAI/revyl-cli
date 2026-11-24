// Package auth provides authentication management for the Revyl CLI.
//
// This file implements the server-brokered device approval flow. Unlike the
// loopback browser flow it needs no local listening port and no browser on this
// machine, so it works identically on a laptop, in a cloud agent, over SSH, and
// inside a container. The CLI registers a pending authorization, shows the user
// a URL to open wherever they do have a browser, and polls until they decide.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/revyl/cli/internal/api"
)

const (
	deviceAuthorizationsPath = "/api/v1/entity/users/cli-device-authorizations"

	deviceCredentialsPath = "/api/v1/entity/users/cli-device-authorizations/credentials"

	// deviceRequestTimeout is the default HTTP client timeout for each call.
	deviceRequestTimeout = 15 * time.Second

	// defaultDevicePollInterval is used when the server does not state one.
	defaultDevicePollInterval = 5 * time.Second

	// maxDevicePollInterval caps the backoff, so a long approval keeps polling
	// often enough to finish promptly once the user acts.
	maxDevicePollInterval = 15 * time.Second

	// devicePollBackoffFactor slows polling while nobody is approving, which
	// keeps an unauthenticated endpoint cheap without hurting the common case.
	devicePollBackoffFactor = 1.5
)

// ErrDeviceAuthorizationDenied reports that the user refused the request.
var ErrDeviceAuthorizationDenied = errors.New("the authorization request was denied")

// ErrDeviceAuthorizationExpired reports that the authorization is no longer
// redeemable, because it timed out or was already completed.
var ErrDeviceAuthorizationExpired = errors.New("the authorization request expired; run the command again")

type DeviceAuthorization = api.CreateCLIDeviceAuthorizationResponse

// PollDeadline returns when this authorization stops being redeemable.
//
// Parameters:
//   - authorization: The pending request returned by CreateAuthorization.
//   - now: The current time, so callers can control the clock in tests.
//
// Returns:
//   - time.Time: The instant after which polling is pointless.
func PollDeadline(authorization *DeviceAuthorization, now time.Time) time.Time {
	if authorization == nil {
		return now
	}
	return now.Add(time.Duration(authorization.ExpiresIn) * time.Second)
}

func pollInterval(interval int) time.Duration {
	if interval <= 0 {
		return defaultDevicePollInterval
	}
	return time.Duration(interval) * time.Second
}

type DeviceAuthConfig struct {
	BackendURL string

	// ClientInstanceID is the stable per-install identifier used to rotate only
	// this machine's key.
	ClientInstanceID string

	// DeviceLabel is the human-readable label shown on the approval page.
	DeviceLabel string

	// ClientSource is the bounded launcher that started this login, when known.
	ClientSource *api.CLIClientSource

	// HTTPClient overrides the transport, primarily for tests.
	HTTPClient *http.Client
}

// ClientSourceFromEnv accepts only cursor_plugin as a hint for approval copy.
func ClientSourceFromEnv() *api.CLIClientSource {
	if strings.TrimSpace(os.Getenv("REVYL_CLIENT_SOURCE")) != string(api.CLIClientSourceCursorPlugin) {
		return nil
	}
	source := api.CLIClientSourceCursorPlugin
	return &source
}

type DeviceAuth struct {
	config DeviceAuthConfig
	client *http.Client

	// wait blocks between polls. Held as a field so tests can drive the polling
	// loop without spending the real interval on every state transition.
	wait func(ctx context.Context, d time.Duration) error
}

func NewDeviceAuth(config DeviceAuthConfig) *DeviceAuth {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: deviceRequestTimeout}
	}
	return &DeviceAuth{config: config, client: client, wait: sleepOrCancel}
}

func sleepOrCancel(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// CreateAuthorization requires a device code and a complete verification URL in the response.
func (d *DeviceAuth) CreateAuthorization(ctx context.Context) (*DeviceAuthorization, error) {
	body := api.CreateCLIDeviceAuthorizationRequest{}
	if d.config.ClientInstanceID != "" {
		body.ClientInstanceId = &d.config.ClientInstanceID
	}
	if d.config.DeviceLabel != "" {
		body.DeviceLabel = &d.config.DeviceLabel
	}
	if d.config.ClientSource != nil {
		body.ClientSource = d.config.ClientSource
	}

	var authorization DeviceAuthorization
	if err := d.post(ctx, deviceAuthorizationsPath, body, &authorization); err != nil {
		return nil, err
	}
	if authorization.DeviceCode == "" || authorization.VerificationUriComplete == "" {
		return nil, errors.New("the server returned an unusable authorization request")
	}
	return &authorization, nil
}

// WaitForApproval backs off on pending responses and returns the approved key in
// the browser login result shape so both flows can share credential storage.
func (d *DeviceAuth) WaitForApproval(
	ctx context.Context,
	authorization *DeviceAuthorization,
) (*BrowserAuthResult, error) {
	interval := pollInterval(authorization.Interval)
	deadline := PollDeadline(authorization, time.Now())

	for {
		// Checked before waiting so an already-elapsed request costs no delay and
		// no doomed request, and compared with !Before so that reaching the
		// deadline exactly counts as expired. A strictly-after comparison would
		// depend on the clock advancing between two adjacent reads, which is not
		// guaranteed on platforms with coarse timer granularity.
		if !time.Now().Before(deadline) {
			return nil, ErrDeviceAuthorizationExpired
		}

		if err := d.wait(ctx, interval); err != nil {
			return nil, err
		}

		credential, err := d.redeem(ctx, authorization.DeviceCode)
		if err != nil {
			return nil, err
		}

		switch credential.State {
		case api.CLIDeviceAuthorizationStateApproved:
			if credential.ApiKeyToken == nil || *credential.ApiKeyToken == "" {
				return nil, errors.New("the approval did not include a usable credential")
			}
			return &BrowserAuthResult{
				Token:      *credential.ApiKeyToken,
				Email:      derefString(credential.Email),
				OrgID:      derefString(credential.OrgId),
				UserID:     derefString(credential.UserId),
				APIKeyID:   derefString(credential.ApiKeyId),
				AuthMethod: "api_key",
			}, nil
		case api.CLIDeviceAuthorizationStateDenied:
			return nil, ErrDeviceAuthorizationDenied
		case api.CLIDeviceAuthorizationStatePending:
			interval = nextPollInterval(interval)
		default:
			return nil, fmt.Errorf("the server reported an unknown authorization state %q", credential.State)
		}
	}
}

func nextPollInterval(current time.Duration) time.Duration {
	next := time.Duration(float64(current) * devicePollBackoffFactor)
	if next > maxDevicePollInterval {
		return maxDevicePollInterval
	}
	return next
}

// redeem performs one credential poll.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - deviceCode: The secret issued when the authorization was created.
//
// Returns:
//   - *api.RedeemCLIDeviceCredentialResponse: The observed state, with the
//     credential when approved.
//   - error: ErrDeviceAuthorizationExpired when the server no longer knows this
//     request, or a transport error.
func (d *DeviceAuth) redeem(ctx context.Context, deviceCode string) (*api.RedeemCLIDeviceCredentialResponse, error) {
	var credential api.RedeemCLIDeviceCredentialResponse
	err := d.post(ctx, deviceCredentialsPath, api.RedeemCLIDeviceCredentialRequest{DeviceCode: deviceCode}, &credential)
	if err != nil {
		var statusErr *deviceStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return nil, ErrDeviceAuthorizationExpired
		}
		return nil, err
	}
	return &credential, nil
}

type deviceStatusError struct {
	StatusCode int
	Detail     string
}

func (e *deviceStatusError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("authorization request failed (%d): %s", e.StatusCode, e.Detail)
	}
	return fmt.Sprintf("authorization request failed with status %d", e.StatusCode)
}

// post sends a JSON request without an Authorization header. Non-2xx responses
// return a deviceStatusError with the server's decoded detail, if any.
func (d *DeviceAuth) post(ctx context.Context, path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to encode authorization request: %w", err)
	}

	endpoint := strings.TrimSuffix(d.config.BackendURL, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to build authorization request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := d.client.Do(request)
	if err != nil {
		return fmt.Errorf("failed to reach Revyl: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &deviceStatusError{
			StatusCode: response.StatusCode,
			Detail:     decodeDetail(response.Body),
		}
	}

	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to read the authorization response: %w", err)
	}
	return nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func decodeDetail(body io.Reader) string {
	var parsed struct {
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(body).Decode(&parsed); err != nil {
		return ""
	}
	return parsed.Detail
}
