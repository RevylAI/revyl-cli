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

`harness.kind` accepts `revyl`, `cursor`, `capy`, or `devin`:

- `revyl` runs Revyl's device proof agent.
- `cursor` launches a Cursor Cloud Agent proof thread. `model_id` is optional
  for this harness; after publishing the policy, run
  `revyl config authorize-cursor-proof`.
- `capy` launches a Capy proof thread using the configured Capy connection.
  Its optional `model_id` accepts `openai/gpt-6-luna`, `openai/gpt-6.1-sol`,
  `anthropic/claude-opus-5-5`, or `anthropic/claude-sonnet-5-5`. Omit it for
  GPT-6 Luna in fast mode with high reasoning. Its optional `capy_project_id`
  pins the Capy project the thread runs in. Omit it to use the first enabled
  Capy project that checks out the repository. A pinned project that is no
  longer enabled on the connection, or that does not check out the repository,
  fails the proof instead of falling back to another project.
- `devin` (alpha) starts a Devin session through the organization's
  team-default Devin connection. It takes no other fields. Devin is in alpha,
  and Revyl accepts it only for organizations it has enabled; to add yours,
  email [support@revyl.ai](mailto:support@revyl.ai). As with `capy`, the
  connection is the authorization: set the harness and each run checks that
  the Devin organization can see the repository. CLI 0.1.187 is the first
  release that accepts `devin`; older releases reject it.

The CLI rejects unknown model IDs, rejects `model_id` for `revyl` and `devin`,
and rejects `capy_project_id` for any harness other than `capy` instead of
silently dropping them.
