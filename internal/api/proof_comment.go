package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/revyl/cli/internal/backendheaders"
)

// proofCommentPath is excluded from generated models (scripts/openapi-excluded-paths.txt)
// because it authenticates via proof-run credentials or approved Cursor correlation.
const proofCommentPath = "/api/v1/scm/proof-runs/comment"

// ProofCommentRequest carries a PR proof write-up; run identity comes from proof credentials
// or approved agent correlation, Problem and Blocked are mutually exclusive, and omitting both reports a pass.
type ProofCommentRequest struct {
	Body    string `json:"body"`
	Problem string `json:"problem,omitempty"`
	Blocked string `json:"blocked,omitempty"`
}

// PublishProofComment posts a proof write-up for the run identified by the caller's proof credential
// or approved Cursor correlation. One-shot runs settle on write-up and reject later posts; reporting
// both problem and blocked or exceeding comment limits fails.
func (c *Client) PublishProofComment(ctx context.Context, body, problem, blocked string) error {
	payload, err := json.Marshal(ProofCommentRequest{
		Body:    body,
		Problem: problem,
		Blocked: blocked,
	})
	if err != nil {
		return fmt.Errorf("encode proof comment: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPut, c.baseURL+proofCommentPath, bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if apiKey := strings.TrimSpace(c.GetAPIKey()); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("X-Revyl-Client", "cli")
	backendheaders.SetCloudAgentConversationContext(req)
	setCIHeaders(req)
	setAgentHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("publish proof comment: %w", err)
	}
	recordUpcomingMinimumVersion(resp)

	return parseResponse(resp, nil)
}
