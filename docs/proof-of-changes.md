# Configure Proof of Changes

Proof of Changes is configured in `.revyl/config.yaml` under
`pr_review.proof_of_changes`:

```yaml
pr_review:
  proof_of_changes:
    enabled: true
    harness:
      kind: revyl
```

`harness.kind` accepts `revyl`, `cursor`, or `capy`:

- `revyl` runs Revyl's device proof agent.
- `cursor` launches a Cursor Cloud Agent proof thread. `model_id` is optional
  for this harness; after publishing the policy, run
  `revyl config authorize-cursor-proof`.
- `capy` launches a Capy proof thread using the configured Capy connection.
  Its optional `model_id` accepts `openai/gpt-6-luna`, `openai/gpt-6.1-sol`,
  `anthropic/claude-opus-5-5`, or `anthropic/claude-sonnet-5-5`. Omit it for
  GPT-6 Luna in fast mode with high reasoning.

The CLI rejects unknown model IDs and rejects `model_id` for `revyl` instead of
silently dropping it.
