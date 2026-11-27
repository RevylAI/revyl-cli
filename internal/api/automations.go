package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// AutomationsNotEnabledCode is the detail code every Automations route answers
// with while Revyl staff have not turned Automations on for the organization.
const AutomationsNotEnabledCode = "automations_not_enabled"

func (c *Client) ListAutomations(ctx context.Context, query url.Values) (*AutomationListResponse, error) {
	return automationRequest[AutomationListResponse](ctx, c, http.MethodGet, "/api/v1/automations/?"+query.Encode(), nil, nil)
}

func (c *Client) CreateAutomation(ctx context.Context, request AutomationCreateRequest) (*AutomationCreateResponse, error) {
	return automationRequest[AutomationCreateResponse](ctx, c, http.MethodPost, "/api/v1/automations/", request, nil)
}

func (c *Client) GetAutomation(ctx context.Context, automationID string) (*AutomationResponse, error) {
	return automationRequest[AutomationResponse](ctx, c, http.MethodGet, fmt.Sprintf("/api/v1/automations/%s", url.PathEscape(automationID)), nil, nil)
}

// PatchAutomation changes only the fields request sets. The generated model
// omits a nil description, so clearDescription sends the explicit null that
// clears one.
func (c *Client) PatchAutomation(ctx context.Context, automationID string, request AutomationPatchRequest, clearDescription bool) (*AutomationResponse, error) {
	var body any = request
	if clearDescription {
		encoded, err := json.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		fields := map[string]any{}
		if err := json.Unmarshal(encoded, &fields); err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		fields["description"] = nil
		body = fields
	}
	return automationRequest[AutomationResponse](ctx, c, http.MethodPatch, fmt.Sprintf("/api/v1/automations/%s", url.PathEscape(automationID)), body, nil)
}

func (c *Client) ReplaceAutomation(ctx context.Context, automationID string, request AutomationUpdateRequest) (*AutomationUpdateResponse, error) {
	return automationRequest[AutomationUpdateResponse](ctx, c, http.MethodPut, fmt.Sprintf("/api/v1/automations/%s", url.PathEscape(automationID)), request, nil)
}

func (c *Client) DeleteAutomation(ctx context.Context, automationID string) error {
	return automationDelete(ctx, c, fmt.Sprintf("/api/v1/automations/%s", url.PathEscape(automationID)))
}

func (c *Client) AddAutomationTrigger(ctx context.Context, automationID string, request AutomationCreateRequest_Triggers_Item) (*AutomationTriggerMutationResponse, error) {
	return automationRequest[AutomationTriggerMutationResponse](ctx, c, http.MethodPost, fmt.Sprintf("/api/v1/automations/%s/triggers", url.PathEscape(automationID)), request, nil)
}

// Trigger is the added or rotated trigger without its one-time webhook URL.
func (r AutomationTriggerMutationResponse) Trigger() AutomationTriggerResponse {
	return AutomationTriggerResponse{
		AutomationId:  r.AutomationId,
		CreatedAt:     r.CreatedAt,
		Id:            r.Id,
		NextFireAt:    r.NextFireAt,
		OrgId:         r.OrgId,
		TriggerConfig: AutomationTriggerResponse_TriggerConfig{union: r.TriggerConfig.union},
		TriggerType:   AutomationTriggerResponseTriggerType(r.TriggerType),
		UpdatedAt:     r.UpdatedAt,
	}
}

func (c *Client) GetAutomationTrigger(ctx context.Context, triggerID string) (*AutomationTriggerResponse, error) {
	return automationRequest[AutomationTriggerResponse](ctx, c, http.MethodGet, fmt.Sprintf("/api/v1/automations/triggers/%s", url.PathEscape(triggerID)), nil, nil)
}

func (c *Client) UpdateAutomationTrigger(ctx context.Context, triggerID string, request AutomationTriggerConfigUpdateRequest) (*AutomationTriggerResponse, error) {
	return automationRequest[AutomationTriggerResponse](ctx, c, http.MethodPatch, fmt.Sprintf("/api/v1/automations/triggers/%s", url.PathEscape(triggerID)), request, nil)
}

func (c *Client) RemoveAutomationTrigger(ctx context.Context, triggerID string) error {
	return automationDelete(ctx, c, fmt.Sprintf("/api/v1/automations/triggers/%s", url.PathEscape(triggerID)))
}

func (c *Client) RotateAutomationIncomingWebhookURL(ctx context.Context, triggerID string) (*AutomationTriggerMutationResponse, error) {
	return automationRequest[AutomationTriggerMutationResponse](ctx, c, http.MethodPost, fmt.Sprintf("/api/v1/automations/triggers/%s/rotate-incoming-webhook-url", url.PathEscape(triggerID)), nil, nil)
}

// StartAutomationRun starts a run through the automation's manual trigger. Like
// every write here it is sent once and never retried, since a retry without an
// idempotency key could start a second run.
func (c *Client) StartAutomationRun(ctx context.Context, automationID, idempotencyKey string) (*AutomationManualRunResponse, error) {
	var headers map[string]string
	if idempotencyKey != "" {
		headers = map[string]string{"Idempotency-Key": idempotencyKey}
	}
	return automationRequest[AutomationManualRunResponse](ctx, c, http.MethodPost, fmt.Sprintf("/api/v1/automations/%s/runs", url.PathEscape(automationID)), nil, headers)
}

func (c *Client) GetAutomationRun(ctx context.Context, runID string) (*AutomationRunDetailResponse, error) {
	return automationRequest[AutomationRunDetailResponse](ctx, c, http.MethodGet, fmt.Sprintf("/api/v1/automations/runs/%s", url.PathEscape(runID)), nil, nil)
}

// GetAutomationRunOnce reads a run with a single request, for a caller that
// retries failed reads on its own schedule.
func (c *Client) GetAutomationRunOnce(ctx context.Context, runID string) (*AutomationRunDetailResponse, error) {
	response, err := c.doRequestOnce(ctx, http.MethodGet, fmt.Sprintf("/api/v1/automations/runs/%s", url.PathEscape(runID)), nil)
	if err != nil {
		return nil, err
	}
	var run AutomationRunDetailResponse
	if err := parseResponse(response, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (c *Client) ListAutomationRuns(ctx context.Context, query url.Values) (*AutomationRunListResponse, error) {
	return automationRequest[AutomationRunListResponse](ctx, c, http.MethodGet, "/api/v1/automations/runs?"+query.Encode(), nil, nil)
}

func (c *Client) CancelAutomationRun(ctx context.Context, runID string) (*AutomationRunDetailResponse, error) {
	return automationRequest[AutomationRunDetailResponse](ctx, c, http.MethodPost, fmt.Sprintf("/api/v1/automations/runs/%s/cancel", url.PathEscape(runID)), nil, nil)
}

func (c *Client) ListAutomationTriggerTypes(ctx context.Context) ([]AutomationTriggerTypeAvailability, error) {
	result, err := automationRequest[[]AutomationTriggerTypeAvailability](ctx, c, http.MethodGet, "/api/v1/automations/trigger-types", nil, nil)
	if err != nil {
		return nil, err
	}
	return *result, nil
}

func (c *Client) ListAutomationActionTypes(ctx context.Context) ([]AutomationActionTypeResponse, error) {
	result, err := automationRequest[[]AutomationActionTypeResponse](ctx, c, http.MethodGet, "/api/v1/automations/action-types", nil, nil)
	if err != nil {
		return nil, err
	}
	return *result, nil
}

// automationRequest retries a read's transient failures and sends a write once.
func automationRequest[Result any](ctx context.Context, c *Client, method, path string, body any, headers map[string]string) (*Result, error) {
	var (
		response *http.Response
		err      error
	)
	if method == http.MethodGet {
		response, err = c.doRequestWithRetry(ctx, method, path, body)
	} else {
		response, err = c.doRequestOnce(ctx, method, path, body, headers)
	}
	if err != nil {
		return nil, err
	}
	var result Result
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func automationDelete(ctx context.Context, c *Client, path string) error {
	response, err := c.doRequestOnce(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	return parseResponse(response, nil)
}
