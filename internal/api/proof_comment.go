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

// proofCommentPath relays a proof agent's write-up onto the pull request it is
// proving. Deliberately absent from the generated model surface (see
// scripts/openapi-excluded-paths.txt): it authenticates with a proof-run
// runtime credential rather than an ordinary API key, so a generated client
// would advertise it to callers who can never use it.
const proofCommentPath = "/api/v1/scm/proof-runs/comment"

// ProofCommentRequest is one write-up for the pull request under proof.
//
// It names no run: the backend reads that from the credential, so an agent can
// only ever write into the comment for the pull request it was launched on.
//
// Problem and Blocked are the two ways a run ends other than well, and they
// are mutually exclusive: an agent that never observed the app cannot also
// have found a bug in it. Sending neither claims a pass.
type ProofCommentRequest struct {
	Body    string `json:"body"`
	Problem string `json:"problem,omitempty"`
	Blocked string `json:"blocked,omitempty"`
}

// PublishProofComment sends a proof agent's write-up to the pull request.
//
// Each call replaces the previous write-up rather than appending, so an agent
// can post early and post again as it learns more.
//
// Parameters:
//   - ctx: cancellation context.
//   - body: markdown authored by the agent.
//   - problem: one sentence naming what is broken about the pull request.
//     Empty unless the agent found something, which is the normal case.
//   - blocked: one sentence naming what stopped the agent from observing the
//     change at all. Empty unless the run never got to see the app behave.
//
// Returns:
//   - An error when the credential is not a proof-run credential, the run has
//     already finished, both outcomes were reported at once, or the body
//     exceeds what a comment can hold.
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

	return parseResponse(resp, nil)
}
