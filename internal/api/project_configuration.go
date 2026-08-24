package api

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const projectConfigurationRequestTimeout = 90 * time.Second

func (c *Client) projectConfigurationHTTPClient() *http.Client {
	client := *c.httpClient
	client.Timeout = projectConfigurationRequestTimeout
	return &client
}

// ListRepositoryProjects returns the bounded canonical project catalog after
// the backend verifies fresh provider access to the requested repository. The
// POST is a read-only query and therefore retains the client's bounded retry
// policy for transient provider and transport failures.
func (c *Client) ListRepositoryProjects(
	ctx context.Context,
	request RepositoryProjectCatalogQuery,
) (*RepositoryProjectCatalogResponse, error) {
	response, err := c.doRequestWithRetryClient(
		ctx,
		"POST",
		"/api/v1/projects/catalog",
		&request,
		c.projectConfigurationHTTPClient(),
	)
	if err != nil {
		return nil, err
	}
	var result RepositoryProjectCatalogResponse
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReadProjectConfiguration reads canonical server state after repository access
// verification. Read and validate requests retain the client's normal bounded
// retry policy because neither operation mutates state.
func (c *Client) ReadProjectConfiguration(
	ctx context.Context,
	projectID string,
	request ProjectConfigurationReadRequest,
) (*ProjectConfigurationReadResponse, error) {
	path := fmt.Sprintf("/api/v1/projects/%s/configuration/read", projectID)
	response, err := c.doRequestWithRetryClient(
		ctx, "POST", path, &request, c.projectConfigurationHTTPClient(),
	)
	if err != nil {
		return nil, err
	}
	var result ProjectConfigurationReadResponse
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ValidateProjectConfiguration validates complete candidate meaning without
// writing or changing configuration authority.
func (c *Client) ValidateProjectConfiguration(
	ctx context.Context,
	projectID string,
	request ProjectConfigurationValidateRequest,
) (*ProjectConfigurationValidateResponse, error) {
	path := fmt.Sprintf("/api/v1/projects/%s/configuration/validate", projectID)
	response, err := c.doRequestWithRetryClient(
		ctx, "POST", path, &request, c.projectConfigurationHTTPClient(),
	)
	if err != nil {
		return nil, err
	}
	var result ProjectConfigurationValidateResponse
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReplaceProjectConfiguration atomically replaces the complete aggregate.
// It intentionally uses one request attempt: retrying an ambiguous commit
// response with an observed-state precondition is not safe.
func (c *Client) ReplaceProjectConfiguration(
	ctx context.Context,
	projectID string,
	request ProjectConfigurationReplaceRequest,
) (*ProjectConfigurationReplaceResponse, error) {
	path := fmt.Sprintf("/api/v1/projects/%s/configuration", projectID)
	response, err := c.doRequestOnceWithClient(
		ctx, "PUT", path, &request, c.projectConfigurationHTTPClient(),
	)
	if err != nil {
		return nil, err
	}
	var result ProjectConfigurationReplaceResponse
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetProjectCursorProofAuthorization reads the current server-owned authorization.
func (c *Client) GetProjectCursorProofAuthorization(
	ctx context.Context,
	projectID string,
) (*ProjectCursorProofAuthorizationResponse, error) {
	path := fmt.Sprintf("/api/v1/projects/%s/cursor-proof-authorization", projectID)
	response, err := c.doRequestWithRetryClient(
		ctx, "GET", path, nil, c.projectConfigurationHTTPClient(),
	)
	if err != nil {
		return nil, err
	}
	var result ProjectCursorProofAuthorizationResponse
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AuthorizeProjectCursorProof records one human authorization after the backend
// rechecks the project's live Cursor repository access. It intentionally uses
// one request attempt because retrying an ambiguous write could restamp state.
func (c *Client) AuthorizeProjectCursorProof(
	ctx context.Context,
	projectID string,
) (*ProjectCursorProofAuthorizationResponse, error) {
	path := fmt.Sprintf("/api/v1/projects/%s/cursor-proof-authorization", projectID)
	response, err := c.doRequestOnceWithClient(
		ctx, "PUT", path, nil, c.projectConfigurationHTTPClient(),
	)
	if err != nil {
		return nil, err
	}
	var result ProjectCursorProofAuthorizationResponse
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
