package asc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// BaseURL is the App Store Connect API base URL.
	BaseURL = "https://api.appstoreconnect.apple.com/v1"

	// defaultTimeout is the default HTTP request timeout.
	defaultTimeout = 30 * time.Second
)

// Client is an App Store Connect API client.
//
// It handles JWT-based authentication and provides methods for common
// App Store Connect operations like listing builds, managing TestFlight
// groups, and checking app review status.
type Client struct {
	httpClient *http.Client
	keyID      string
	issuerID   string
	privateKey *ecdsa.PrivateKey
}

// NewClient creates a new App Store Connect API client.
//
// Parameters:
//   - keyID: The App Store Connect API Key ID
//   - issuerID: The App Store Connect Issuer ID
//   - privateKeyPath: Path to the .p8 private key file
//
// Returns:
//   - *Client: The configured client
//   - error: If the private key cannot be loaded
func NewClient(keyID, issuerID, privateKeyPath string) (*Client, error) {
	key, err := LoadPrivateKey(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load private key: %w", err)
	}

	return &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		keyID:      keyID,
		issuerID:   issuerID,
		privateKey: key,
	}, nil
}

// doRequest executes an authenticated request against the ASC API.
//
// Parameters:
//   - ctx: Context for cancellation
//   - method: HTTP method (GET, POST, PATCH, DELETE)
//   - path: API path (appended to BaseURL, or full URL if starts with https://)
//   - body: Request body (can be nil)
//
// Returns:
//   - []byte: Response body
//   - error: If the request fails or returns a non-2xx status
func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	token, err := GenerateJWT(c.keyID, c.issuerID, c.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate JWT: %w", err)
	}

	url := path
	if len(path) > 0 && path[0] == '/' {
		url = BaseURL + path
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr APIError
		if json.Unmarshal(respBody, &apiErr) == nil && len(apiErr.Errors) > 0 {
			return nil, &apiErr
		}
		return nil, fmt.Errorf("ASC API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ListApps returns all apps accessible by the API key.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - []App: List of apps
//   - error: If the request fails
func (c *Client) ListApps(ctx context.Context) ([]App, error) {
	data, err := c.doRequest(ctx, http.MethodGet, "/apps", nil)
	if err != nil {
		return nil, err
	}

	var resp ListResponse[App]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse apps response: %w", err)
	}

	return resp.Data, nil
}

// GetApp returns a specific app by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The App Store Connect numeric app ID
//
// Returns:
//   - *App: The app resource
//   - error: If the request fails
func (c *Client) GetApp(ctx context.Context, appID string) (*App, error) {
	data, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/apps/%s", appID), nil)
	if err != nil {
		return nil, err
	}

	var resp Response[App]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse app response: %w", err)
	}

	return &resp.Data, nil
}

// ListBuilds returns builds for an app, sorted by upload date descending.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The App Store Connect numeric app ID
//   - limit: Maximum number of builds to return (1-200)
//
// Returns:
//   - []Build: List of builds (newest first)
//   - error: If the request fails
func (c *Client) ListBuilds(ctx context.Context, appID string, limit int) ([]Build, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 200 {
		limit = 200
	}

	path := fmt.Sprintf("/builds?filter[app]=%s&sort=-uploadedDate&limit=%d", appID, limit)
	data, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp ListResponse[Build]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse builds response: %w", err)
	}

	return resp.Data, nil
}

// GetBuild returns a specific build by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - buildID: The App Store Connect build ID
//
// Returns:
//   - *Build: The build resource
//   - error: If the request fails
func (c *Client) GetBuild(ctx context.Context, buildID string) (*Build, error) {
	data, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/builds/%s", buildID), nil)
	if err != nil {
		return nil, err
	}

	var resp Response[Build]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse build response: %w", err)
	}

	return &resp.Data, nil
}

// ListBetaGroups returns beta groups for an app.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The App Store Connect numeric app ID
//
// Returns:
//   - []BetaGroup: List of beta groups
//   - error: If the request fails
func (c *Client) ListBetaGroups(ctx context.Context, appID string) ([]BetaGroup, error) {
	path := fmt.Sprintf("/betaGroups?filter[app]=%s", appID)
	data, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp ListResponse[BetaGroup]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse beta groups response: %w", err)
	}

	return resp.Data, nil
}

// AddBuildToBetaGroup adds a build to a TestFlight beta group.
//
// This makes the build available to all testers in the specified group.
//
// Parameters:
//   - ctx: Context for cancellation
//   - betaGroupID: The beta group ID
//   - buildID: The build ID to add
//
// Returns:
//   - error: If the request fails
func (c *Client) AddBuildToBetaGroup(ctx context.Context, betaGroupID, buildID string) error {
	body := AddBuildToBetaGroupRequest{
		Data: []ResourceRef{
			{
				Type: "builds",
				ID:   buildID,
			},
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	_, err = c.doRequest(ctx, http.MethodPost,
		fmt.Sprintf("/betaGroups/%s/relationships/builds", betaGroupID),
		bytes.NewReader(bodyJSON))
	return err
}

// SetWhatsNewForBuild sets the "What to Test" text for a build.
//
// Parameters:
//   - ctx: Context for cancellation
//   - buildID: The build ID
//   - whatsNew: The "What to Test" text
//   - locale: The locale (e.g., "en-US")
//
// Returns:
//   - error: If the request fails
func (c *Client) SetWhatsNewForBuild(ctx context.Context, buildID, whatsNew, locale string) error {
	if locale == "" {
		locale = "en-US"
	}

	body := CreateBetaBuildLocalizationRequest{
		Data: CreateBetaBuildLocalizationData{
			Type: "betaBuildLocalizations",
			Attributes: CreateBetaBuildLocalizationAttributes{
				WhatsNew: whatsNew,
				Locale:   locale,
			},
			Relationships: CreateBetaBuildLocalizationRelationships{
				Build: RelationshipData{
					Data: ResourceRef{
						Type: "builds",
						ID:   buildID,
					},
				},
			},
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	_, err = c.doRequest(ctx, http.MethodPost, "/betaBuildLocalizations", bytes.NewReader(bodyJSON))
	return err
}

// ListAppStoreVersions returns the App Store versions for an app.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The App Store Connect numeric app ID
//
// Returns:
//   - []AppStoreVersion: List of App Store versions
//   - error: If the request fails
func (c *Client) ListAppStoreVersions(ctx context.Context, appID string) ([]AppStoreVersion, error) {
	path := fmt.Sprintf("/apps/%s/appStoreVersions", appID)
	data, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp ListResponse[AppStoreVersion]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse app store versions response: %w", err)
	}

	return resp.Data, nil
}

// WaitForBuildProcessing polls until a build finishes processing or times out.
//
// Parameters:
//   - ctx: Context for cancellation
//   - buildID: The build ID to watch
//   - pollInterval: How often to check (e.g., 30s)
//   - timeout: Maximum time to wait
//
// Returns:
//   - *Build: The final build state
//   - error: If the build fails processing or timeout is exceeded
func (c *Client) WaitForBuildProcessing(ctx context.Context, buildID string, pollInterval, timeout time.Duration) (*Build, error) {
	deadline := time.Now().Add(timeout)

	for {
		build, err := c.GetBuild(ctx, buildID)
		if err != nil {
			return nil, fmt.Errorf("failed to check build status: %w", err)
		}

		switch build.Attributes.ProcessingState {
		case ProcessingStateValid:
			return build, nil
		case ProcessingStateFailed, ProcessingStateInvalid:
			return build, fmt.Errorf("build processing failed with state: %s", build.Attributes.ProcessingState)
		}

		if time.Now().After(deadline) {
			return build, fmt.Errorf("timed out waiting for build processing (state: %s)", build.Attributes.ProcessingState)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
			// continue polling
		}
	}
}

// FindBetaGroupByName finds a beta group by name within an app.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The App Store Connect numeric app ID
//   - groupName: The beta group name to find
//
// Returns:
//   - *BetaGroup: The matching beta group, or nil if not found
//   - error: If the request fails
func (c *Client) FindBetaGroupByName(ctx context.Context, appID, groupName string) (*BetaGroup, error) {
	groups, err := c.ListBetaGroups(ctx, appID)
	if err != nil {
		return nil, err
	}

	for _, g := range groups {
		if g.Attributes.Name == groupName {
			return &g, nil
		}
	}

	return nil, nil
}

// FindAppByBundleID finds an app by its bundle identifier.
//
// Parameters:
//   - ctx: Context for cancellation
//   - bundleID: The iOS bundle identifier (e.g., "com.nof1.experiments")
//
// Returns:
//   - *App: The matching app, or nil if not found
//   - error: If the request fails
func (c *Client) FindAppByBundleID(ctx context.Context, bundleID string) (*App, error) {
	path := fmt.Sprintf("/apps?filter[bundleId]=%s", bundleID)
	data, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp ListResponse[App]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse apps response: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, nil
	}

	return &resp.Data[0], nil
}
