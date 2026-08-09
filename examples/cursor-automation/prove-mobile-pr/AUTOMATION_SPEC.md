# Automation spec — Prove mobile PR (BYO-CI)

Copy-paste companion for [PROMPT.md](PROMPT.md). Create the Automation at
[cursor.com/automations](https://cursor.com/automations).

## Triggers

| Preference | Trigger | When to use |
| --- | --- | --- |
| Preferred | **CI completed** / **Workflow run completed** | Fires after your GitHub Actions (or other CI) finishes. Pair with the upload job in `examples/ci-github-actions/upload-for-cursor-proof.yml` so the Revyl build exists before proof starts. |
| Fallback | **Pull request pushed** / **Pull request updated** | Use only when a CI-completed trigger is unavailable. Expect occasional "build not found yet" comments until upload finishes; the prompt already brief-waits and retries. |

Scope the preferred trigger to the workflow (or job) that uploads the artifact to Revyl, not every CI workflow in the repo.

## Tools

Enable at least:

| Tool | Purpose |
| --- | --- |
| **Comment on pull request** | Create or edit the `## Revyl device proof` comment. Required. |
| **Revyl MCP** (plugin / `revyl mcp serve`) | List builds, start/stop devices, screenshots, validation when exposed. |
| Shell / terminal (if offered) | Run `revyl build list`, `revyl device start --build-version-id …`, `session publish` / `session share` when MCP does not cover a step. |

Do **not** rely on `gh` or raw GitHub API tokens for commenting. The native Comment tool posts as the Automation identity.

## Secrets and variables

| Name | Kind | Required | Notes |
| --- | --- | --- | --- |
| `REVYL_API_KEY` | Secret | Yes | Personal or org API key from Revyl **Account → Personal API Keys**. Same key the CI upload job uses is fine. |
| `REVYL_APP_ID` (or platform-specific app ids) | Variable | Yes | Revyl app UUID that receives CI uploads. Pass into the prompt context / env so the agent can call `revyl build list --app …`. |

Build secrets (signing keys, Expo tokens, store credentials) stay in **your** CI. This Automation never builds the app and does not need a Cursor API key in GitHub.

## Identity

- Comments appear as the Cursor Automation / agent identity configured for the Automation, not as the engineer whose laptop last touched the repo.
- Device sessions and uploaded evidence are attributed to the Revyl org behind `REVYL_API_KEY`.
- Reviewers open the shareable session link without a Revyl login; do not rewrite that URL's origin.

## Repository prerequisites

1. CI uploads a simulator `.app` / installable `.apk` with `revyl build upload` so GitHub Actions metadata (`scm_head_sha`, PR number, etc.) is stamped — see `upload-for-cursor-proof.yml`.
2. Revyl MCP (or CLI on `PATH`) is available to the Automation with `REVYL_API_KEY`.
3. Optional: `.revyl/config.yaml` with `auth_bypass` / `before_session` when the app needs a signed-in session.

## Twin path (Revyl GitHub App)

If the team later connects the Revyl GitHub App, the same upload step works with `use_existing_ci: true` and `proof_harness: { kind: cursor }`. Revyl then launches Cursor proof instead of (or in addition to) this Automation. See the Mintlify page `integrations/cursor-proof` and `integrations/github` ("Use your own CI").
