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
	// deviceAuthorizationsPath is the collection the CLI registers against.
	deviceAuthorizationsPath = "/api/v1/entity/users/cli-device-authorizations"

	// deviceCredentialsPath is where an approved authorization is redeemed.
	deviceCredentialsPath = "/api/v1/entity/users/cli-device-authorizations/credentials"

	// deviceRequestTimeout bounds each individual call, so a hung server stalls
	// one poll rather than the whole login.
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

// DeviceAuthorization is a pending request the user has yet to decide.
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

// pollInterval returns the first wait between polls.
func pollInterval(interval int) time.Duration {
	if interval <= 0 {
		return defaultDevicePollInterval
	}
	return time.Duration(interval) * time.Second
}

// DeviceAuthConfig configures the device approval flow.
type DeviceAuthConfig struct {
	// BackendURL is the base URL of the Revyl API.
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

// ClientSourceFromEnv returns the bounded launcher stamped by the plugin hook.
//
// Only `cursor_plugin` is accepted. Any other value is treated as ordinary CLI
// so an unknown or spoofed REVYL_CLIENT_SOURCE cannot change approval copy.
//
// Returns:
//   - *api.CLIClientSource: The cursor_plugin enum when the environment stamps
//     that source, otherwise nil.
func ClientSourceFromEnv() *api.CLIClientSource {
	if strings.TrimSpace(os.Getenv("REVYL_CLIENT_SOURCE")) != string(api.CLIClientSourceCursorPlugin) {
		return nil
	}
	source := api.CLIClientSourceCursorPlugin
	return &source
}

// DeviceAuth runs the server-brokered approval flow.
type DeviceAuth struct {
	config DeviceAuthConfig
	client *http.Client

	// wait blocks between polls. Held as a field so tests can drive the polling
	// loop without spending the real interval on every state transition.
	wait func(ctx context.Context, d time.Duration) error
}

// NewDeviceAuth creates a device approval handler.
//
// Parameters:
//   - config: Backend URL and the identity of the requesting device.
//
// Returns:
//   - *DeviceAuth: A handler ready to create and poll authorizations.
func NewDeviceAuth(config DeviceAuthConfig) *DeviceAuth {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: deviceRequestTimeout}
	}
	return &DeviceAuth{config: config, client: client, wait: sleepOrCancel}
}

// sleepOrCancel waits out one poll interval unless the caller cancels first.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - d: How long to wait.
//
// Returns:
//   - error: ctx.Err() when cancelled during the wait, otherwise nil.
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

// CreateAuthorization registers a pending approval request.
//
// Parameters:
//   - ctx: Context for cancellation.
//
// Returns:
//   - *DeviceAuthorization: The device code, user code, and approval URLs.
//   - error: A transport error, or a server error that never includes a secret.
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

// WaitForApproval polls until the user decides or the request expires.
//
// Polling backs off while the request stays pending, so an approval a user
// leaves for minutes does not hammer an unauthenticated endpoint, and returns
// promptly once they act.
//
// Parameters:
//   - ctx: Context for cancellation; a cancelled context stops polling at once.
//   - authorization: The pending request returned by CreateAuthorization.
//
// Returns:
//   - *BrowserAuthResult: The minted credential, shaped like the browser flow's
//     result so both paths persist credentials through one code path.
//   - error: ErrDeviceAuthorizationDenied when refused,
//     ErrDeviceAuthorizationExpired when it timed out or was already redeemed,
//     ctx.Err() when cancelled, or a transport error.
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

// nextPollInterval backs off one step, bounded by the cap.
//
// Parameters:
//   - current: The interval just waited.
//
// Returns:
//   - time.Duration: The next wait, never above maxDevicePollInterval.
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

// deviceStatusError is a non-2xx response from the authorization endpoints.
type deviceStatusError struct {
	StatusCode int
	Detail     string
}

// Error describes the failure without echoing any request body.
func (e *deviceStatusError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("authorization request failed (%d): %s", e.StatusCode, e.Detail)
	}
	return fmt.Sprintf("authorization request failed with status %d", e.StatusCode)
}

// post sends one JSON request and decodes a JSON response.
//
// These endpoints are unauthenticated by design, so no credential is attached.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - path: Backend path to call.
//   - body: Request payload to encode.
//   - out: Destination for the decoded response.
//
// Returns:
//   - error: A *deviceStatusError for non-2xx responses, otherwise a transport
//     or decoding error. No error text contains the device code.
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

// derefString returns the pointed-to string, or empty when the pointer is nil.
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// decodeDetail extracts a FastAPI error detail, tolerating any other shape.
//
// Parameters:
//   - body: The error response body.
//
// Returns:
//   - string: The detail string, or empty when the body is not a detail object.
func decodeDetail(body io.Reader) string {
	var parsed struct {
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(body).Decode(&parsed); err != nil {
		return ""
	}
	return parsed.Detail
}
